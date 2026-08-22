#!/usr/bin/env bash
# Asserts the active Go toolchain is at least the one go.mod pins.
#
# go.mod pins `toolchain` because reaching the network drags crypto/tls,
# encoding/asn1 and net/url into govulncheck's view, and older patch releases
# carry advisories against them. That pin only protects anyone if the toolchain
# actually in use honours it — GOTOOLCHAIN=local on a stale install silently
# builds with the vulnerable standard library instead, and the release binary is
# the one that matters. This turns that into a build failure.
#
# Usage: check-toolchain.sh
set -euo pipefail

pinned=$(awk '/^toolchain /{print $2; exit}' go.mod)
if [ -z "$pinned" ]; then
	echo "check-toolchain: go.mod has no toolchain directive — nothing to enforce"
	exit 0
fi

active=$(go version | awk '{print $3}')

echo "check-toolchain: active ${active}, go.mod pins ${pinned}"

# sort -V orders go1.26.6 after go1.25.0 and after go1.9 — a plain string
# compare gets both of those wrong.
oldest=$(printf '%s\n%s\n' "$pinned" "$active" | sort -V | head -1)
if [ "$active" != "$pinned" ] && [ "$oldest" = "$active" ]; then
	echo >&2
	echo "check-toolchain: FAIL — building with ${active}, older than the pinned ${pinned}." >&2
	echo "GOTOOLCHAIN is probably set to 'local' against a stale install. Unset it, or" >&2
	echo "install ${pinned} (go install golang.org/dl/${pinned}@latest)." >&2
	exit 1
fi

echo "check-toolchain: OK"
