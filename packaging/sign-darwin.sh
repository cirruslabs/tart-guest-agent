#!/usr/bin/env bash
set -euo pipefail

if [[ $# != 2 ]]; then
  echo "Usage: $0 <snapshot|release> <universal-binary>" >&2
  exit 1
fi

mode=$1
binary=$2
identifier=org.cirruslabs.tart-guest-agent

# Never accidentally sign an individual slice or a Linux executable.
lipo "$binary" -verify_arch arm64 x86_64

case "$mode" in
  snapshot)
    codesign --force --sign - --identifier "$identifier" --timestamp=none "$binary"
    ;;
  release)
    : "${MACOS_SIGN_IDENTITY:?Developer ID signing identity is required for releases}"
    if [[ "$MACOS_SIGN_IDENTITY" == - ]]; then
      echo "Ad-hoc signing is not allowed for releases" >&2
      exit 1
    fi
    sign_args=(--force --sign "$MACOS_SIGN_IDENTITY" --identifier "$identifier" --timestamp)
    if [[ -n "${MACOS_SIGN_KEYCHAIN:-}" ]]; then
      sign_args+=(--keychain "$MACOS_SIGN_KEYCHAIN")
    fi
    codesign "${sign_args[@]}" "$binary"
    ;;
  *)
    echo "Unknown signing mode: $mode" >&2
    exit 1
    ;;
esac

bash "$(dirname "$0")/verify-darwin.sh" "$mode" "$binary"
