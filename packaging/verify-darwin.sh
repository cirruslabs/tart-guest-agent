#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 2 || ( "$1" != snapshot && "$1" != release ) ]]; then
  echo "Usage: $0 <snapshot|release> <universal-binary>" >&2
  exit 1
fi

mode=$1
binary=$2
identifier=org.cirruslabs.tart-guest-agent

lipo "$binary" -verify_arch arm64 x86_64
codesign --verify --all-architectures --strict --verbose=2 "$binary"

# Require the same stable identity on both slices. A native-only check misses
# the unsigned x86_64 slice produced by the old release pipeline.
for arch in arm64 x86_64; do
  codesign --verify --strict --arch "$arch" --verbose=2 "$binary"
  details=$(codesign --display --arch "$arch" --verbose=4 "$binary" 2>&1)
  if ! grep -Fqx "Identifier=$identifier" <<< "$details"; then
    echo "$arch has an unexpected code-signing identifier" >&2
    exit 1
  fi
  if [[ "$mode" == snapshot ]]; then
    if ! grep -Fqx 'Signature=adhoc' <<< "$details"; then
      echo "$arch snapshot is not ad-hoc signed" >&2
      exit 1
    fi
  else
    # Apple Developer ID Application, issued to Cirrus Labs. Certificate
    # rotation within this team does not change the designated identity.
    codesign --verify --strict --arch "$arch" \
      -R '=anchor apple generic and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists and certificate leaf[subject.OU] = "9M2P8L4D89"' \
      "$binary"
  fi
  printf '%s signature:\n' "$arch"
  grep -E '^(Identifier|Signature|Authority|TeamIdentifier)=' <<< "$details"
done
