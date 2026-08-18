#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 2 || ( "$1" != snapshot && "$1" != release ) ]]; then
  echo "Usage: $0 <snapshot|release> <dist-directory>" >&2
  exit 1
fi

mode=$1
dist=$(cd "$2" && pwd)
archive=tart-guest-agent-darwin-all.tar.gz
temporary_dir=$(mktemp -d)
trap 'rm -rf "$temporary_dir"' EXIT

# Verify the actual archive and its checksum, not just the loose build output.
(
  cd "$dist"
  shasum -a 256 --check ./*_checksums.txt
)
tar -xzf "$dist/$archive" -C "$temporary_dir" tart-guest-agent
cmp "$dist/tart-guest-agent_darwin_all/tart-guest-agent" "$temporary_dir/tart-guest-agent"
bash "$(dirname "$0")/verify-darwin.sh" "$mode" "$temporary_dir/tart-guest-agent"
