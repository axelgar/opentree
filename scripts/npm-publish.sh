#!/usr/bin/env bash
# Copies goreleaser-built binaries into the npm platform packages, updates all
# package.json versions, and publishes every package to the npm registry.
#
# Usage: npm-publish.sh <version>
#   version  Git tag, e.g. v1.2.3 (the leading 'v' is stripped for npm)
#
# Required env:
#   NODE_AUTH_TOKEN  npm publish token (set by actions/setup-node)
set -euo pipefail

VERSION=${1:?Usage: npm-publish.sh <version>}
NPM_VERSION="${VERSION#v}"   # strip leading 'v'
DIST_DIR="dist"
NPM_DIR="npm"

# Map goreleaser os_arch directory prefix -> npm package directory name
declare -A PLATFORMS=(
  ["linux_amd64"]="opentree-linux-x64"
  ["linux_arm64"]="opentree-linux-arm64"
  ["darwin_amd64"]="opentree-darwin-x64"
  ["darwin_arm64"]="opentree-darwin-arm64"
)

echo "Publishing opentree ${NPM_VERSION} to npm..."

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

  # Bump version
  jq --arg v "${NPM_VERSION}" '.version = $v' "${pkg_dir}/package.json" > "${pkg_dir}/package.json.tmp"
  mv "${pkg_dir}/package.json.tmp" "${pkg_dir}/package.json"
done

# ── Bump version + optionalDependencies in the main package ───────────────────
jq --arg v "${NPM_VERSION}" '
  .version = $v |
  .optionalDependencies = (
    .optionalDependencies | to_entries | map(.value = $v) | from_entries
  )
' "${NPM_DIR}/opentree/package.json" > "${NPM_DIR}/opentree/package.json.tmp"
mv "${NPM_DIR}/opentree/package.json.tmp" "${NPM_DIR}/opentree/package.json"

# ── Publish platform packages first ───────────────────────────────────────────
for key in "${!PLATFORMS[@]}"; do
  pkg="${PLATFORMS[$key]}"
  echo "  Publishing @axelgar/${pkg}@${NPM_VERSION}..."
  npm publish "${NPM_DIR}/${pkg}" --access public
done

# ── Publish the main package ──────────────────────────────────────────────────
echo "  Publishing @axelgar/opentree@${NPM_VERSION}..."
npm publish "${NPM_DIR}/opentree" --access public

echo "Done. @axelgar/opentree@${NPM_VERSION} is live on npm."
