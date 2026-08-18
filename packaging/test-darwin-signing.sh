#!/usr/bin/env bash
set -euo pipefail

scripts=$(cd "$(dirname "$0")" && pwd)
temporary_dir=$(mktemp -d)
trap 'rm -rf "$temporary_dir"' EXIT

expect_failure() {
  if "$@" > "$temporary_dir/expected-failure.log" 2>&1; then
    echo "Unexpected success: $*" >&2
    exit 1
  fi
}

printf 'int main(void) { return 0; }\n' > "$temporary_dir/main.c"
for arch in arm64 x86_64; do
  clang -arch "$arch" "$temporary_dir/main.c" -o "$temporary_dir/$arch"
done
# Reproduce the old Go/linker state: ad-hoc arm64, unsigned x86_64.
codesign --force --sign - --identifier a.out --timestamp=none "$temporary_dir/arm64"
codesign --remove-signature "$temporary_dir/x86_64"
lipo -create "$temporary_dir/arm64" "$temporary_dir/x86_64" -output "$temporary_dir/universal"
binary="$temporary_dir/universal"

expect_failure bash "$scripts/verify-darwin.sh" snapshot "$binary"
before=$(shasum -a 256 "$binary")
expect_failure env -u MACOS_SIGN_IDENTITY bash "$scripts/sign-darwin.sh" release "$binary"
expect_failure env MACOS_SIGN_IDENTITY=- bash "$scripts/sign-darwin.sh" release "$binary"
[[ "$before" == "$(shasum -a 256 "$binary")" ]]

bash "$scripts/sign-darwin.sh" snapshot "$binary"
expect_failure bash "$scripts/verify-darwin.sh" release "$binary"

# A valid signature with the wrong identifier must also be rejected.
codesign --force --sign - --identifier wrong.identifier --timestamp=none "$binary"
expect_failure bash "$scripts/verify-darwin.sh" snapshot "$binary"
expect_failure bash "$scripts/sign-darwin.sh" snapshot "$temporary_dir/arm64"
expect_failure bash "$scripts/sign-darwin.sh" invalid "$binary"

bash "$scripts/sign-darwin.sh" snapshot "$binary"
echo "Darwin universal signing regression tests passed"
