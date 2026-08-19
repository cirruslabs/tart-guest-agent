#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 2 || ( "$1" != snapshot && "$1" != release ) ]]; then
  echo "Usage: $0 <snapshot|release> <darwin-archive>" >&2
  exit 1
fi

mode=$1
temporary_dir=$(mktemp -d)
trap 'rm -rf "$temporary_dir"' EXIT

tar -xzf "$2" -C "$temporary_dir" tart-guest-agent
binary="$temporary_dir/tart-guest-agent"
lipo "$binary" -verify_arch arm64 x86_64
codesign --verify --all-architectures --strict --verbose=2 "$binary"

for arch in arm64 x86_64; do
  codesign --verify --strict --arch "$arch" "$binary"
  details=$(codesign --display --arch "$arch" --verbose=4 "$binary" 2>&1)
  grep -Fqx 'Identifier=tart-guest-agent' <<< "$details"
  if [[ "$mode" == snapshot ]]; then
    grep -Fqx 'Signature=adhoc' <<< "$details"
  else
    # Require an Apple-issued Developer ID Application certificate for Cirrus Labs.
    codesign --verify --strict --arch "$arch" \
      -R '=anchor apple generic and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists and certificate leaf[subject.OU] = "9M2P8L4D89"' \
      "$binary"
  fi
  printf '%s signature:\n' "$arch"
  grep -E '^(Identifier|Signature|Authority|TeamIdentifier)=' <<< "$details"
done
