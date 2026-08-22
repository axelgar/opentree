#!/usr/bin/env bash
# Validates the npm publishing surface before a tag can reach the registry.
#
# npm-publish.sh publishes five packages in sequence: four platform packages
# holding the binaries, then the launcher package that depends on them. There is
# no transaction. If the launcher's optionalDependencies name a package that was
# never published — a typo, a platform added to goreleaser but not to npm/ — the
# four platform packages are already live and immutable, and every install of
# the launcher fails with "could not find binary for platform". The only fix is
# a new version.
#
# So: check the whole shape here, on every PR, where it is free to be wrong.
#
# Usage: check-npm-packages.sh [expected-version]
#   expected-version  optional, e.g. 1.2.3 — asserts every package.json matches
set -euo pipefail

EXPECTED=${1:-}
NPM_DIR=npm
MAIN=$NPM_DIR/opentree
LAUNCHER=$MAIN/bin/opentree
fail=0

note() { echo "check-npm: $*"; }
bad() {
	echo "check-npm: FAIL — $*" >&2
	fail=1
}

command -v jq >/dev/null || {
	echo "check-npm: jq is required (brew install jq)" >&2
	exit 1
}
command -v node >/dev/null || {
	echo "check-npm: node is required" >&2
	exit 1
}

# ── The launcher, goreleaser and npm/ must agree on one set of platforms ──────
# Three independent lists of the same four platforms is three chances to drift.
launcher_platforms=$(node -e '
	const s = require("fs").readFileSync(process.argv[1], "utf8");
	const m = s.match(/PLATFORM_PACKAGES = \{([^}]*)\}/s);
	if (!m) { console.error("could not parse PLATFORM_PACKAGES"); process.exit(1); }
	const names = [...m[1].matchAll(/"@axelgar\/([^"]+)"/g)].map(x => x[1]);
	console.log(names.sort().join("\n"));
' "$LAUNCHER")

dir_platforms=$(find "$NPM_DIR" -mindepth 1 -maxdepth 1 -type d ! -name opentree -exec basename {} \; | sort)

optional_deps=$(jq -r '.optionalDependencies | keys[]' "$MAIN/package.json" | sed 's|@axelgar/||' | sort)

# goreleaser's matrix, expanded to the npm naming (x64 rather than amd64).
# A YAML parser would be sturdier, but goos/goarch are flat scalar lists and
# pulling in a dependency to read six lines is a worse trade.
goreleaser_platforms=$(awk '
	/^[[:space:]]*goos:[[:space:]]*$/  { mode = "os";   next }
	/^[[:space:]]*goarch:[[:space:]]*$/ { mode = "arch"; next }
	/^[[:space:]]*-[[:space:]]*[a-z0-9]+[[:space:]]*$/ {
		if (mode == "os")        oses[++no] = $2
		else if (mode == "arch") arches[++na] = $2
		next
	}
	{ mode = "" }
	END {
		npm["amd64"] = "x64"; npm["arm64"] = "arm64"
		if (no == 0 || na == 0) { print "PARSE-FAILED"; exit }
		for (i = 1; i <= no; i++)
			for (j = 1; j <= na; j++)
				print "opentree-" oses[i] "-" (npm[arches[j]] ? npm[arches[j]] : arches[j])
	}' .goreleaser.yml | sort)

if [ "$goreleaser_platforms" = "PARSE-FAILED" ]; then
	bad "could not read goos/goarch out of .goreleaser.yml — the build matrix moved"
	goreleaser_platforms=$dir_platforms # do not also report a bogus set difference
fi

if [ "$launcher_platforms" != "$dir_platforms" ]; then
	bad "bin/opentree and npm/ list different platforms"
	diff <(echo "$launcher_platforms") <(echo "$dir_platforms") | sed 's/^/    /' >&2 || true
fi
if [ "$optional_deps" != "$dir_platforms" ]; then
	bad "optionalDependencies and npm/ list different platforms"
	diff <(echo "$optional_deps") <(echo "$dir_platforms") | sed 's/^/    /' >&2 || true
fi
if [ "$goreleaser_platforms" != "$dir_platforms" ]; then
	bad ".goreleaser.yml builds a different platform set than npm/ publishes"
	diff <(echo "$goreleaser_platforms") <(echo "$dir_platforms") | sed 's/^/    /' >&2 || true
fi
[ "$fail" -eq 0 ] && note "platform sets agree: $(echo "$dir_platforms" | tr '\n' ' ')"

# ── Every package.json must be publishable and on one version ────────────────
versions=""
for pkg_dir in "$MAIN" $(find "$NPM_DIR" -mindepth 1 -maxdepth 1 -type d ! -name opentree | sort); do
	manifest="$pkg_dir/package.json"
	[ -f "$manifest" ] || {
		bad "$manifest is missing"
		continue
	}
	jq -e . "$manifest" >/dev/null 2>&1 || {
		bad "$manifest is not valid JSON"
		continue
	}

	name=$(jq -r '.name' "$manifest")
	version=$(jq -r '.version' "$manifest")
	versions="$versions$version\n"

	[ "$(jq -r '.license // empty' "$manifest")" = "MIT" ] || bad "$name: license should be MIT"
	[ "$(jq -r '.publishConfig.access // empty' "$manifest")" = "public" ] ||
		bad "$name: publishConfig.access must be \"public\" or the scoped publish is rejected"
	# npm provenance needs a repository field it can match against the building repo.
	jq -e '.repository' "$manifest" >/dev/null || bad "$name: no repository field (npm provenance requires one)"
	jq -e '.files | index("bin/opentree")' "$manifest" >/dev/null ||
		bad "$name: \"files\" must include bin/opentree or the tarball ships without the binary"

	if [ "$pkg_dir" != "$MAIN" ]; then
		jq -e '.os and .cpu' "$manifest" >/dev/null ||
			bad "$name: needs os/cpu so npm installs only the matching optional dependency"
	fi

	if [ -n "$EXPECTED" ] && [ "$version" != "$EXPECTED" ]; then
		bad "$name is at $version, expected $EXPECTED"
	fi
done

if [ "$(printf "$versions" | sort -u | wc -l)" -ne 1 ]; then
	bad "package versions disagree: $(printf "$versions" | sort -u | tr '\n' ' ')"
fi

# The launcher's optionalDependencies must be pinned to the same version, or npm
# resolves an old binary against a new launcher.
if [ -n "$EXPECTED" ]; then
	jq -e --arg v "$EXPECTED" 'all(.optionalDependencies[]; . == $v)' "$MAIN/package.json" >/dev/null ||
		bad "optionalDependencies are not all pinned to $EXPECTED"
fi

# ── The launcher has to at least parse ───────────────────────────────────────
node --check "$LAUNCHER" || bad "bin/opentree is not valid JavaScript"
[ -x "$LAUNCHER" ] || bad "bin/opentree is not executable (npm preserves the mode bit from git)"

if [ "$fail" -ne 0 ]; then
	echo >&2
	echo "check-npm: the npm publish would produce a broken install." >&2
	exit 1
fi

note "OK — all five packages are consistent and publishable"
