#!/bin/sh
# Fetch the TortoiseBotsManager addon at the pinned commit into .cache/ (ignored by git)
# and print the directory. The addon's code is not vendored; bump the commit on purpose.
# Exit 3 when it is not cached and cannot be downloaded.
set -eu

ADDON_COMMIT=6a3802f641414ad0f8d3b0fb40ee3c0b5a468534
ADDON_REPO=Sagiroth/TortoiseBotsManager

root=$(cd "$(dirname "$0")/../.." && pwd)
dir=${TBM_ADDON_DIR:-$root/.cache/tbmcontract/addon-$ADDON_COMMIT}
if [ -f "$dir/TortoiseBotsManager.toc" ]; then
	echo "$dir"
	exit 0
fi
command -v curl >/dev/null 2>&1 || { echo "tbm-contract: curl missing, cannot fetch the addon" >&2; exit 3; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
if ! curl -fsSL --max-time 60 -o "$tmp/addon.tar.gz" "https://github.com/$ADDON_REPO/archive/$ADDON_COMMIT.tar.gz"; then
	echo "tbm-contract: cannot download $ADDON_REPO@$ADDON_COMMIT (offline?)" >&2
	exit 3
fi
mkdir "$tmp/x"
tar -xzf "$tmp/addon.tar.gz" -C "$tmp/x" --strip-components=1
mkdir -p "$(dirname "$dir")"
rm -rf "$dir"
mv "$tmp/x" "$dir"
echo "$dir"
