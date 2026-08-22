#!/usr/bin/env bash
# Runs the built binary the way a user first meets it.
#
# cmd/opentree is 0% covered and cmd/opentree/cmd barely more, because command
# wiring is exactly the code that unit tests skip: it is all init(), flag
# registration and cobra plumbing. That code still fails — cobra panics on a
# duplicate flag or a repeated command name, and it does it at process start, so
# the failure is not "one subcommand is broken", it is "the binary does not run".
#
# Every command's help is also its documentation. Rendering all of it proves the
# tree is wired, the flags are registered once, and nothing panics on the way up.
#
# Usage: smoke.sh [path-to-binary]
set -euo pipefail

BIN=${1:-./opentree}

if [ ! -x "$BIN" ]; then
	echo "smoke: $BIN is not an executable — run 'make build' first" >&2
	exit 1
fi

fail=0
check() {
	local what=$1
	shift
	if out=$("$@" 2>&1); then
		printf '  ok    %s\n' "$what"
	else
		printf '  FAIL  %s\n' "$what" >&2
		printf '%s\n' "$out" | sed 's/^/          /' >&2
		fail=1
		return
	fi
	# A command whose help renders empty is wired but undocumented, which reads
	# to a user exactly like a broken command.
	if [ -z "$(printf '%s' "$out" | tr -d '[:space:]')" ]; then
		printf '  FAIL  %s printed nothing\n' "$what" >&2
		fail=1
	fi
}

echo "smoke: $BIN"

check "--version" "$BIN" --version
check "--help" "$BIN" --help

# Ask the binary what it can do rather than hardcoding a list here; a new
# command is then covered the moment it is added, which is the only way a check
# like this stays true.
commands=$("$BIN" --help | awk '
	/^Available Commands:/ { in_list = 1; next }
	/^[A-Za-z]/ && !/^Available Commands:/ { in_list = 0 }
	in_list && NF { print $1 }' | grep -vE '^(help|completion)$' || true)

if [ -z "$commands" ]; then
	echo "smoke: FAIL — could not read the command list out of --help" >&2
	exit 1
fi

for cmd in $commands; do
	check "$cmd --help" "$BIN" "$cmd" --help
done

# The version has to be a version. A binary that reports "dev" is one whose
# ldflags wiring broke, and it is the release artifact that matters.
version_line=$("$BIN" --version)
case "$version_line" in
	"opentree "*) ;;
	*)
		echo "smoke: FAIL — --version printed '$version_line', expected 'opentree <version>'" >&2
		fail=1
		;;
esac

# An unknown command must fail rather than silently doing nothing.
if "$BIN" definitely-not-a-command >/dev/null 2>&1; then
	echo "smoke: FAIL — an unknown command exited 0" >&2
	fail=1
else
	echo "  ok    unknown command exits non-zero"
fi

if [ "$fail" -ne 0 ]; then
	echo >&2
	echo "smoke: the binary does not come up cleanly" >&2
	exit 1
fi

echo "smoke: OK — $(printf '%s\n' "$commands" | wc -l | tr -d ' ') commands respond"
