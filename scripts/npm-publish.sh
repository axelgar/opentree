#!/usr/bin/env bash
# Copies goreleaser-built binaries into the npm platform packages, updates all
# package.json versions, and publishes every package to the npm registry.
#
# Usage: npm-publish.sh <version>
#   version  Git tag, e.g. v1.2.3 (the leading 'v' is stripped for npm)
#
# Required env:
#   NODE_AUTH_TOKEN  npm publish token (set by actions/setup-node)
#
# Publishing five packages is not a transaction. Once the four platform
# packages are live they are immutable, so anything that can be checked has to
# be checked before the first `npm publish` rather than between them: the
# binaries exist, they are the right architecture, the native one reports the
# tag's version, every manifest agrees, and the registry accepts a dry run.
set -euo pipefail

VERSION=${1:?Usage: npm-publish.sh <version>}
NPM_VERSION="${VERSION#v}" # strip leading 'v'
DIST_DIR="dist"
NPM_DIR="npm"

# npm publishes to the `latest` dist-tag unless told otherwise, and `latest` is
# what a bare `npm install @axelgar/opentree` resolves to. A v1.2.0-rc.1 tag
# would therefore hand every new user a release candidate, and there is no way
# to take it back — dist-tags can be moved, but anyone who installed in the
# meantime already has it.
NPM_TAG=latest
case "${NPM_VERSION}" in
*-*) NPM_TAG=next ;;
esac
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

# Map goreleaser os_arch directory prefix -> npm package directory name
declare -A PLATFORMS=(
	["linux_amd64"]="opentree-linux-x64"
	["linux_arm64"]="opentree-linux-arm64"
	["darwin_amd64"]="opentree-darwin-x64"
	["darwin_arm64"]="opentree-darwin-arm64"
)

# Expected `file` output per target, so a goreleaser matrix change that quietly
# writes the same amd64 binary into every package cannot reach the registry.
declare -A EXPECT_ARCH=(
	["linux_amd64"]="x86-64"
	["linux_arm64"]="ARM aarch64"
	["darwin_amd64"]="x86_64"
	["darwin_arm64"]="arm64"
)

echo "Publishing opentree ${NPM_VERSION} to npm (dist-tag: ${NPM_TAG})..."

# ── Copy binaries and bump versions in platform packages ──────────────────────
for key in "${!PLATFORMS[@]}"; do
	pkg="${PLATFORMS[$key]}"
	pkg_dir="${NPM_DIR}/${pkg}"

	# GoReleaser names the build output directory: opentree_<os>_<arch>[_<variant>]
	bin_dir=$(find "${DIST_DIR}" -maxdepth 1 -type d -name "opentree_${key}*" | sort | head -1)

	if [[ -z "${bin_dir}" ]]; then
		echo "ERROR: could not find goreleaser binary directory for ${key} (looked in ${DIST_DIR}/opentree_${key}*)"
		exit 1
	fi

	echo "  Copying ${bin_dir}/opentree -> ${pkg_dir}/bin/opentree"
	mkdir -p "${pkg_dir}/bin"
	cp "${bin_dir}/opentree" "${pkg_dir}/bin/opentree"
	chmod +x "${pkg_dir}/bin/opentree"

	# A macOS user installing an x86-64 binary gets "cannot execute binary file"
	# and no way to tell why. Confirm each package holds the architecture its
	# `cpu` field promises.
	described=$(file -b "${pkg_dir}/bin/opentree")
	if [[ "${described}" != *"${EXPECT_ARCH[$key]}"* ]]; then
		echo "ERROR: ${pkg} holds the wrong binary."
		echo "  expected: ${EXPECT_ARCH[$key]}"
		echo "  actual:   ${described}"
		exit 1
	fi

	# Bump version
	jq --arg v "${NPM_VERSION}" '.version = $v' "${pkg_dir}/package.json" >"${pkg_dir}/package.json.tmp"
	mv "${pkg_dir}/package.json.tmp" "${pkg_dir}/package.json"
done

# ── The binary must report the version it is being published as ───────────────
# goreleaser injects the version through -ldflags. If that wiring breaks, every
# build still succeeds and every binary says "dev" — silently, forever, because
# nothing else looks. Only the runner's own platform can be executed here.
native="${NPM_DIR}/opentree-$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m | sed 's/x86_64/x64/;s/aarch64/arm64/')/bin/opentree"
if [[ -x "${native}" ]]; then
	reported=$("${native}" --version)
	if [[ "${reported}" != *"${NPM_VERSION}"* ]]; then
		echo "ERROR: the built binary reports '${reported}' but is being published as ${NPM_VERSION}."
		echo "  The -ldflags '-X main.version=...' wiring in .goreleaser.yml is broken."
		exit 1
	fi
	echo "  Version check: ${reported}"
else
	echo "  WARNING: no binary for this runner's platform (${native}); skipping the version check"
fi

# ── Copy README into main package so it appears on npmjs.com ──────────────────
cp README.md "${NPM_DIR}/opentree/README.md"

# ── Bump version + optionalDependencies in the main package ───────────────────
jq --arg v "${NPM_VERSION}" '
  .version = $v |
  .optionalDependencies = (
    .optionalDependencies | to_entries | map(.value = $v) | from_entries
  )
' "${NPM_DIR}/opentree/package.json" >"${NPM_DIR}/opentree/package.json.tmp"
mv "${NPM_DIR}/opentree/package.json.tmp" "${NPM_DIR}/opentree/package.json"

# ── Everything must be consistent before anything is published ────────────────
"${SCRIPT_DIR}/check-npm-packages.sh" "${NPM_VERSION}"

# ── Pack every tarball, then dry-run the lot ──────────────────────────────────
# npm pack --json reports the filename it wrote. Parsing the human output with
# `tail -1` breaks the moment npm prints a deprecation notice after it.
declare -a TARBALLS=()
declare -a PKG_NAMES=()
for key in "${!PLATFORMS[@]}"; do
	pkg="${PLATFORMS[$key]}"
	tarball=$(npm pack "./${NPM_DIR}/${pkg}" --pack-destination /tmp --json | jq -r '.[0].filename')
	TARBALLS+=("/tmp/${tarball}")
	PKG_NAMES+=("@axelgar/${pkg}")
done
main_tarball=$(npm pack "./${NPM_DIR}/opentree" --pack-destination /tmp --json | jq -r '.[0].filename')
TARBALLS+=("/tmp/${main_tarball}")
PKG_NAMES+=("@axelgar/opentree")

# A dry run exercises auth, scope access and manifest validation against the
# real registry without writing anything. If the token is wrong, this is where
# it fails — with nothing published yet.
echo "  Dry run against the registry..."
for tarball in "${TARBALLS[@]}"; do
	npm publish "${tarball}" --access public --tag "${NPM_TAG}" --dry-run
done

# ── Publish: platform packages first, launcher last ───────────────────────────
# Order matters. The launcher's optionalDependencies must already resolve when
# it goes live, or every install between the two publishes is broken.
for i in "${!TARBALLS[@]}"; do
	echo "  Publishing ${PKG_NAMES[$i]}@${NPM_VERSION}..."
	npm publish "${TARBALLS[$i]}" --access public --tag "${NPM_TAG}"
done

echo "Done. @axelgar/opentree@${NPM_VERSION} is live on npm."
