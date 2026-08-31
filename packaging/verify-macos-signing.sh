#!/bin/sh
set -eu

# Run on the final signed executable, before GoReleaser archives it. Do not
# modify its signature: image builders depend on its signed TCC identity.
binary=$1
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT

codesign --verify --all-architectures --strict "$binary"
for arch in $(lipo -archs "$binary"); do
  codesign --display --verbose=4 --arch "$arch" "$binary" 2>&1 \
    | grep -Eq 'flags=.*runtime' || {
      echo "Hardened Runtime is missing for $arch" >&2
      exit 1
    }
done

# A harmless ad-hoc-signed library exercises the same loading requirements as
# guest-installed compatibility libraries, without needing Metal hardware.
cat > "$work_dir/probe.c" <<'EOF'
#include <stdio.h>
__attribute__((constructor)) static void loaded(void) {
  fputs("tart-guest-agent signing probe loaded\n", stderr);
}
EOF
xcrun clang -dynamiclib "$work_dir/probe.c" -o "$work_dir/probe.dylib"
codesign --force --sign - "$work_dir/probe.dylib"
DYLD_INSERT_LIBRARIES="$work_dir/probe.dylib" "$binary" --version \
  > "$work_dir/version" 2> "$work_dir/probe.log"
if ! grep -Fxq 'tart-guest-agent signing probe loaded' "$work_dir/probe.log"; then
  echo 'The signed guest agent did not load the injection probe' >&2
  exit 1
fi

for arch in $(lipo -archs "$binary"); do
  codesign --display --arch "$arch" --entitlements - --xml "$binary" \
    > "$work_dir/entitlements.plist"
  for key in allow-dyld-environment-variables disable-library-validation; do
    value=$(/usr/libexec/PlistBuddy -c "Print :com.apple.security.cs.$key" \
      "$work_dir/entitlements.plist")
    test "$value" = true || {
      echo "Missing $key entitlement for $arch" >&2
      exit 1
    }
  done
done
echo 'Signed guest-agent library loading verified'
