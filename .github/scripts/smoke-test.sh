#!/usr/bin/env bash
#
# Starts a netmon binary in an empty directory and checks that it serves the
# web UI and the API. It doesn't need root: when the collector can't start (it
# needs access to conntrack or BPF), netmon logs it and keeps serving.
#
# Usage: [SMOKE_TEST_RUNNER=qemu-aarch64-static] smoke-test.sh path/to/netmon
#
# SMOKE_TEST_RUNNER is a command to run the binary with, for binaries of
# another architecture.

set -euo pipefail

if [ $# -ne 1 ]; then
	echo "usage: $0 path/to/netmon" >&2
	exit 2
fi

binary="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
workdir="$(mktemp -d)"
pid=

cleanup() {
	status=$?
	if [ -n "$pid" ]; then
		kill "$pid" 2>/dev/null || true
		wait "$pid" 2>/dev/null || true
	fi
	if [ "$status" -ne 0 ] && [ -f "$workdir/netmon.log" ]; then
		echo "--- netmon.log"
		cat "$workdir/netmon.log"
	fi
	rm -rf "$workdir"
	exit "$status"
}
trap cleanup EXIT

cd "$workdir"
if [ -n "${SMOKE_TEST_RUNNER:-}" ]; then
	# shellcheck disable=SC2086 # the runner may come with arguments
	$SMOKE_TEST_RUNNER "$binary" >netmon.log 2>&1 &
else
	"$binary" >netmon.log 2>&1 &
fi
pid=$!

base=http://127.0.0.1:2137
for _ in $(seq 1 30); do
	if ! kill -0 "$pid" 2>/dev/null; then
		echo "netmon exited before it started serving" >&2
		exit 1
	fi
	if curl -fsS -o /dev/null "$base/api/stats" 2>/dev/null; then
		break
	fi
	sleep 1
done

echo "GET /api/stats"
curl -fsS "$base/api/stats"
echo
echo "GET /"
curl -fsS "$base/" | grep -qi "<html"
echo "ok"
