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
# Set SHELLTER_SIGN_IDENTITY to the name of a code-signing certificate in your
# keychain to sign with it instead of ad hoc; see the signature step below.
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

    <!-- Why the shell in a terminal reaches into protected folders. macOS
         attributes whatever the shell and its programs open to this app, so
         a .zshrc that sources a file in ~/Downloads is Shellter asking for
         ~/Downloads. These are the words in the prompt that asks. -->
    <key>NSDesktopFolderUsageDescription</key>
    <string>Programs you run in Shellter can read and write files on your Desktop.</string>
    <key>NSDocumentsFolderUsageDescription</key>
    <string>Programs you run in Shellter can read and write files in your Documents folder.</string>
    <key>NSDownloadsFolderUsageDescription</key>
    <string>Programs you run in Shellter can read and write files in your Downloads folder.</string>
    <key>NSNetworkVolumesUsageDescription</key>
    <string>Programs you run in Shellter can read and write files on network volumes.</string>
    <key>NSRemovableVolumesUsageDescription</key>
    <string>Programs you run in Shellter can read and write files on removable volumes.</string>
</dict>
</plist>
PLIST

# The signature is the app's identity to macOS, and the permissions database
# (Downloads, Documents, Full Disk Access) remembers a grant against it.
#
# Ad hoc is the default because it needs nothing set up, but an ad-hoc
# identity is the hash of the binary: every rebuild is, to macOS, a different
# app, and a grant made to the last build does not carry over. A certificate -
# even a self-signed one made in Keychain Access - is an identity that
# survives rebuilds. Neither gets the app past Gatekeeper on another Mac;
# that takes a Developer ID.
codesign --force --sign "${SHELLTER_SIGN_IDENTITY:--}" "$APP" >/dev/null 2>&1 || true

# Finder caches icons per bundle path, and will happily show the old one.
touch "$APP"

echo "built $APP"
