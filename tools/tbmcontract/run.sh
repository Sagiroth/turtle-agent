#!/bin/sh
# Replay TBM: line files (or events files) through the real TortoiseBotsManager addon.
#
#   run.sh [--events events.json]... 
#
# Without arguments: every fixtures/ok-*.txt must match, every fixtures/bad-*.txt must
# fail. With --events: extract the TBM: lines of each events file (tools/tbmlines) and
# require a match. Skips (exit 0, clear message) when lua5.1 or the addon is unavailable,
# unless TBM_CONTRACT_STRICT=1 is set.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
GO=${GO:-$HOME/.local/go/bin/go}
LUA=${LUA:-lua5.1}

skip() {
	echo "tbm-contract: SKIPPED, $1" >&2
	[ "${TBM_CONTRACT_STRICT:-0}" = 1 ] && exit 1
	exit 0
}

command -v "$LUA" >/dev/null 2>&1 || skip "$LUA not found (apt install lua5.1)"
addon=$("$here/fetch.sh") || skip "addon not cached and not downloadable"
echo "tbm-contract: addon $(basename "$addon") ($(ls "$addon"/*.lua | wc -l) lua files), $("$LUA" -v 2>&1)"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fail=0

replay() { # file; prints the driver output, returns its status
	"$LUA" "$here/contract.lua" "$addon" "$1"
}

if [ "${1:-}" = --events ]; then
	n=0
	while [ "${1:-}" = --events ]; do
		src=$(cd "$(dirname "$2")" && pwd)/$(basename "$2")
		shift 2
		n=$((n + 1))
		(cd "$root" && "$GO" run ./tools/tbmlines -o "$work/events$n.txt" "$src") || { echo "tbm-contract: no lines from $src" >&2; exit 1; }
		replay "$work/events$n.txt" || fail=1
	done
	[ "$fail" = 0 ] || { echo "tbm-contract: FAILED" >&2; exit 1; }
	echo "tbm-contract: ok"
	exit 0
fi

for f in "$here"/fixtures/ok-*.txt; do
	replay "$f" || { echo "tbm-contract: $f should match but does not" >&2; fail=1; }
done
# A bad fixture states "# expect-mismatches: N" and must fail with exactly N, so a crash of the
# driver or a check that stopped working does not pass for a failure.
for f in "$here"/fixtures/bad-*.txt; do
	want=$(sed -n 's/^# expect-mismatches: *\([0-9][0-9]*\)$/\1/p' "$f")
	[ -n "$want" ] || { echo "tbm-contract: $f lacks '# expect-mismatches: N'" >&2; fail=1; continue; }
	if out=$(replay "$f" 2>&1); then
		echo "tbm-contract: $f is deliberately broken but passed" >&2
		fail=1
		continue
	fi
	got=$(echo "$out" | sed -n 's/^FAIL: \([0-9][0-9]*\) mismatch.*/\1/p')
	if [ "$got" = "$want" ]; then
		echo "$(basename "$f"): fails as it must ($got mismatches)"
	else
		echo "$out" >&2
		echo "tbm-contract: $f: expected $want mismatches, got ${got:-none}" >&2
		fail=1
	fi
done
[ "$fail" = 0 ] || { echo "tbm-contract: FAILED" >&2; exit 1; }
echo "tbm-contract: ok"
