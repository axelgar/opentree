#!/usr/bin/env bash
# Gates total statement coverage against a floor, and nags when the floor is
# stale.
#
# The floor is a ratchet, not a target: it exists so a refactor cannot quietly
# delete a package's tests. When real coverage climbs comfortably past it, raise
# the number in .github/coverage-floor in the same PR that earned it.
#
# Usage: check-coverage.sh [coverage-profile] [floor-file]
set -euo pipefail

PROFILE=${1:-coverage.out}
FLOOR_FILE=${2:-.github/coverage-floor}
# How far above the floor real coverage may drift before this script suggests
# ratcheting. Wide enough that ordinary churn does not trip it.
SLACK=3.0

if [ ! -s "$PROFILE" ]; then
	echo "check-coverage: $PROFILE is missing or empty — did 'go test -coverprofile' run?" >&2
	exit 1
fi

floor=$(tr -d '[:space:]' <"$FLOOR_FILE")
actual=$(go tool cover -func="$PROFILE" | tail -1 | awk '{print $NF}' | tr -d '%')

echo "check-coverage: ${actual}% covered (floor ${floor}%)"

# awk rather than bc: awk is in POSIX and on every runner, bc is not.
awk -v a="$actual" -v f="$floor" -v s="$SLACK" '
BEGIN {
	if (a + 0 < f + 0) {
		printf "\ncheck-coverage: FAIL — coverage fell from %.1f%% to %.1f%%.\n", f, a
		print  "Add tests for what you changed, or lower .github/coverage-floor deliberately"
		print  "and say why in the commit message."
		exit 1
	}
	if (a + 0 > f + s + 0) {
		printf "\ncheck-coverage: the floor is stale — %.1f%% covered against a %.1f%% floor.\n", a, f
		printf "Raise .github/coverage-floor to %.1f to lock the gain in.\n", a - 1
	}
	exit 0
}'

# Per-package numbers, worst first: the useful half of the report, and the thing
# a reviewer actually wants when the total barely moved.
#
# Read straight from the profile rather than `go tool cover -func`, because
# -func reports one line per function and averaging those weights a three-line
# helper the same as a 200-line Update loop.
echo
echo "check-coverage: least-covered packages"
awk 'NR > 1 {
	blocks = $2; count = $3
	pkg = $1
	# String regex, not /.../ — a slash inside an awk regex literal ends it,
	# character class or not.
	sub("/[^/]*$", "", pkg)
	total[pkg] += blocks
	if (count + 0 > 0) hit[pkg] += blocks
}
END { for (p in total) printf "  %6.1f%%  %s\n", 100 * hit[p] / total[p], p }' "$PROFILE" |
	sort -n | head -8
