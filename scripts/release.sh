#!/bin/sh
#
# Releases Shellter: tests it, builds the app for Intel and Apple Silicon,
# packs it in a disk image, tags the version and publishes it on GitHub.
#
#   ./scripts/release.sh 0.1.0             -> tag v0.1.0, a GitHub release
#   ./scripts/release.sh 0.1.0 --dry-run   -> build/Shellter.dmg, nothing published
#
# The disk image is always called Shellter.dmg, so the newest release is
# always at
#
#   https://github.com/n-kitx/shellter/releases/latest/download/Shellter.dmg
#
# which is the link the website and the README give.
#
set -eu

cd "$(dirname "$0")/.."

usage() {
    echo "usage: $0 <version, like 0.1.0> [--dry-run]" >&2
    exit 2
}

VERSION="${1:-}"
DRY_RUN=""
case "${2:-}" in
    "") ;;
    --dry-run) DRY_RUN=1 ;;
    *) usage ;;
esac
case "$VERSION" in
    [0-9]*.[0-9]*.[0-9]*) ;;
    *) usage ;;
esac
TAG="v$VERSION"
DMG="build/Shellter.dmg"

fail() {
    echo "release: $*" >&2
    exit 1
}

# A release is built from what is on GitHub, not from whatever happens to be
# in this checkout.
if [ -z "$DRY_RUN" ]; then
    [ -z "$(git status --porcelain)" ] || fail "the working tree has changes; commit or stash them"
    [ "$(git rev-parse --abbrev-ref HEAD)" = main ] || fail "not on main"
    git fetch -q origin main
    [ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || fail "main is not the same as origin/main; push or pull first"
    if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null || git ls-remote --exit-code --tags origin "$TAG" >/dev/null; then
        fail "$TAG already exists"
    fi
    command -v gh >/dev/null || fail "the GitHub CLI (gh) is needed to publish"
fi

echo "== testing"
GOTOOLCHAIN=local CGO_ENABLED=1 go vet ./...
GOTOOLCHAIN=local CGO_ENABLED=1 go test -count=1 ./...

echo "== building Shellter $VERSION for Intel and Apple Silicon"
SHELLTER_VERSION="$VERSION" SHELLTER_UNIVERSAL=1 ./scripts/bundle.sh

# The disk image: the app beside a link to /Applications, so installing is
# one drag.
#
# The disk it opens as wears Shellter's icon: .VolumeIcon.icns at its root,
# and the root marked as having a custom icon. That flag can only be set on a
# mounted, writable image, so the image is made writable first and compressed
# after. (The .dmg file's own icon cannot be given: a browser drops it on
# download.)
echo "== packing $DMG"
STAGE="$(mktemp -d)"
cp -R build/Shellter.app "$STAGE/"
ln -s /Applications "$STAGE/Applications"
cp build/Shellter.app/Contents/Resources/Shellter.icns "$STAGE/.VolumeIcon.icns"
RW="$(mktemp -d)/Shellter-rw.dmg"
hdiutil create -quiet -volname "Shellter $VERSION" -srcfolder "$STAGE" \
    -fs HFS+ -format UDRW -ov "$RW"
rm -rf "$STAGE"
MOUNT="$(hdiutil attach -nobrowse -noautoopen "$RW" | tail -1 | cut -f3)"
SetFile -a C "$MOUNT"
hdiutil detach -quiet "$MOUNT"
rm -f "$DMG"
hdiutil convert -quiet "$RW" -format UDZO -o "$DMG"
rm -rf "$(dirname "$RW")"
SHA256="$(shasum -a 256 "$DMG" | cut -d' ' -f1)"
echo "$DMG  $(du -h "$DMG" | cut -f1)  sha256 $SHA256"

if [ -n "$DRY_RUN" ]; then
    echo "dry run: not tagged, not published"
    exit 0
fi

echo "== publishing $TAG"
NOTES="$(mktemp)"
cat > "$NOTES" <<NOTES
## Install

1. Download **Shellter.dmg** below and open it.
2. Drag **Shellter** onto **Applications**.

Shellter runs on macOS 13 Ventura or later, on Apple Silicon and Intel Macs.

### The first time you open it

Shellter is not yet signed with an Apple Developer ID, so macOS will say it
cannot be opened. Either:

- open **System Settings → Privacy & Security** and click **Open Anyway**, or
- run \`xattr -dr com.apple.quarantine /Applications/Shellter.app\` in Terminal.

You only need to do this once.

**SHA-256** of Shellter.dmg: \`$SHA256\`
NOTES

git tag -a "$TAG" -m "Shellter $VERSION"
git push -q origin "$TAG"
gh release create "$TAG" "$DMG" --title "Shellter $VERSION" --notes-file "$NOTES" --verify-tag
rm -f "$NOTES"

echo "released: https://github.com/n-kitx/shellter/releases/tag/$TAG"
