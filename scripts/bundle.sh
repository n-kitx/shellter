#!/bin/sh
#
# Builds Shellter.app: the binary, the icon, and the Info.plist that makes
# macOS treat the three as one application.
#
# A bare Go binary has no icon and no name of its own - the Dock draws a blank
# executable and the menu bar says whatever the file is called. A bundle is
# just a directory with a fixed shape, so this is a copy and a plist, not a
# build system.
#
#   ./scripts/bundle.sh          -> build/Shellter.app
#   open build/Shellter.app
#
set -eu

cd "$(dirname "$0")/.."

APP="build/Shellter.app"
ART="assets/Shellter.png"
VERSION="0.1.0"

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

# cgo and a pinned toolchain: the same incantation the README uses.
GOTOOLCHAIN=local CGO_ENABLED=1 go build -o "$APP/Contents/MacOS/shellter" ./cmd/shellter

# The icon. .icns is a container of the same picture at every size the system
# asks for, from the 16pt one beside a filename to the 512pt one in Finder's
# gallery view. iconutil builds it from a directory named *.iconset, where the
# file names are the sizes.
ICONSET="$(mktemp -d)/Shellter.iconset"
mkdir -p "$ICONSET"
for size in 16 32 128 256 512; do
    sips -s format png -z "$size" "$size" "$ART" \
        --out "$ICONSET/icon_${size}x${size}.png" >/dev/null
    sips -s format png -z "$((size * 2))" "$((size * 2))" "$ART" \
        --out "$ICONSET/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/Shellter.icns"
rm -rf "$(dirname "$ICONSET")"

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleName</key>              <string>Shellter</string>
    <key>CFBundleDisplayName</key>       <string>Shellter</string>
    <key>CFBundleIdentifier</key>        <string>com.thakurankit7.shellter</string>
    <key>CFBundleExecutable</key>        <string>shellter</string>
    <key>CFBundleIconFile</key>          <string>Shellter</string>
    <key>CFBundlePackageType</key>       <string>APPL</string>
    <key>CFBundleShortVersionString</key><string>$VERSION</string>
    <key>CFBundleVersion</key>           <string>$VERSION</string>
    <key>LSMinimumSystemVersion</key>    <string>11.0</string>
    <key>NSHighResolutionCapable</key>   <true/>
    <key>NSPrincipalClass</key>          <string>NSApplication</string>
</dict>
</plist>
PLIST

# Ad-hoc signature. Not a Developer ID - it does not get the app past
# Gatekeeper on another Mac - but it gives the bundle a stable identity here,
# which is what the keychain and the permissions database key off.
codesign --force --sign - "$APP" >/dev/null 2>&1 || true

# Finder caches icons per bundle path, and will happily show the old one.
touch "$APP"

echo "built $APP"
