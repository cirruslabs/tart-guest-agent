#!/usr/bin/env bash
# Run only in the protected, tagged-release job. Never print secret values.
set +x
set -euo pipefail

: "${MACOS_SIGN_P12:?Missing MACOS_SIGN_P12 in the publish environment}"
: "${MACOS_SIGN_PASSWORD:?Missing MACOS_SIGN_PASSWORD in the publish environment}"
: "${RUNNER_TEMP:?}"
: "${GITHUB_ENV:?}"

umask 077
certificate=$(mktemp "$RUNNER_TEMP/tart-guest-agent-signing.XXXXXX")
trap 'rm -f "$certificate"' EXIT
keychain="$RUNNER_TEMP/tart-guest-agent-signing.keychain-db"
keychain_password=$(openssl rand -hex 24)

printf '%s' "$MACOS_SIGN_P12" | base64 --decode > "$certificate"
security create-keychain -p "$keychain_password" "$keychain"
security set-keychain-settings -lut 21600 "$keychain"
security unlock-keychain -p "$keychain_password" "$keychain"
security import "$certificate" -k "$keychain" -P "$MACOS_SIGN_PASSWORD" \
  -T /usr/bin/codesign >/dev/null
security set-key-partition-list -S apple-tool:,apple:,codesign: -s \
  -k "$keychain_password" "$keychain" >/dev/null

identity=$(security find-identity -v -p codesigning "$keychain" |
  sed -nE 's/^[[:space:]]*[0-9]+\) ([[:xdigit:]]{40}) "Developer ID Application: Cirrus Labs, Inc\. \(9M2P8L4D89\)"$/\1/p')
if [[ ! "$identity" =~ ^[[:xdigit:]]{40}$ ]]; then
  echo "Expected exactly one valid Cirrus Labs Developer ID Application identity" >&2
  exit 1
fi

printf 'MACOS_SIGN_KEYCHAIN=%s\nMACOS_SIGN_IDENTITY=%s\n' \
  "$keychain" "$identity" >> "$GITHUB_ENV"
