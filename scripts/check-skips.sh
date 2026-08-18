#!/usr/bin/env bash
# Fails the build when a test skipped itself because an external tool was missing.
#
# Most of what opentree does is shell out: 43 `git` call sites, 56 `tmux`, 5 `gh`.
# The integration tests that cover those paths each guard themselves with a
# t.Skip when the binary is absent. That is the right behaviour on a
# contributor's laptop and exactly the wrong behaviour in CI, where a missing
# tmux turns pkg/tmux, pkg/workspace and pkg/worktree into a green tick that
# proved nothing. This script makes that failure loud.
#
# Only tool-absence skips are fatal. A tmux too old for extended keys, a `gh`
# that is present when the test wanted it absent, a port already in use — those
# are legitimate conditional skips, reported but tolerated.
#
# Usage: check-skips.sh <go-test-json-file>
set -euo pipefail

JSON=${1:?Usage: check-skips.sh <go-test-json-file>}

if [ ! -s "$JSON" ]; then
	echo "check-skips: $JSON is missing or empty — did 'go test -json' run?" >&2
	exit 1
fi

# The messages that mean "CI's environment was not set up, so this package went
# untested". An explicit list rather than a loose /not available/, so that adding
# an ordinary conditional skip does not silently start failing the build.
#
# "gh installed but not authenticated" earns its place: runners ship gh, so the
# binary check passes and only the auth gate fires. Without it in this list,
# pkg/github's integration tests skip themselves on every CI run and the package
# stays effectively untested while the build goes green.
FATAL='(git|tmux|gh) not available|gh installed but not authenticated'

# go test -json reports the skip verdict and the skip reason in separate
# records, so pair them: collect the tests that ended in "skip", then keep only
# their own output lines. Grepping the file flat would report every t.Logf in
# the suite as a skip reason.
reasons=$(awk -v RS='\n' '
	function field(key,   m) {
		if (match($0, "\"" key "\":\"[^\"]*\"") == 0) return ""
		m = substr($0, RSTART, RLENGTH)
		return substr(m, length(key) + 5, length(m) - length(key) - 5)
	}
	NR == FNR {
		if (index($0, "\"Action\":\"skip\"")) skipped[field("Package") "\t" field("Test")] = 1
		next
	}
	{
		if (!index($0, "\"Action\":\"output\"")) next
		key = field("Package") "\t" field("Test")
		if (!(key in skipped)) next
		out = field("Output")
		if (match(out, "_test\\.go:[0-9]+: ") == 0) next
		out = substr(out, RSTART + RLENGTH)
		sub(/\\n$/, "", out) # Output is JSON-escaped; drop the trailing newline
		print key "\t" out
	}' "$JSON" "$JSON" | sort -u)

total_skips=$(grep -c '"Action":"skip"' "$JSON" || true)
echo "check-skips: ${total_skips:-0} skipped test(s) in this run"
if [ -n "$reasons" ]; then
	echo "$reasons" | awk -F'\t' '{printf "  %s: %s\n", $2, $3}'
fi

fatal=$(printf '%s\n' "$reasons" | grep -E "$FATAL" || true)

if [ -n "$fatal" ]; then
	echo >&2
	echo "check-skips: FAIL — tests skipped because an external tool was missing." >&2
	echo "CI must have git, tmux and gh installed; without them these are untested:" >&2
	echo >&2
	printf '%s\n' "$fatal" | awk -F'\t' '
		$1 != last { print "  " $1; last = $1 }
		{ printf "    %s: %s\n", $2, $3 }' >&2
	echo >&2
	echo "Install the missing tool on the runner, or drop the test's skip guard." >&2
	exit 1
fi

echo "check-skips: OK — no test skipped for a missing git/tmux/gh"
