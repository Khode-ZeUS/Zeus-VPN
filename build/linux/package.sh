#!/bin/sh
# Packages build/bin/ZeusVPN (from `wails build`) as a .deb and a .tar.gz.
#   build/linux/package.sh <version> <arch: amd64|arm64>
set -eu
VERSION="$1"
ARCH="$2"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="$ROOT/build/bin"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

install -Dm755 "$OUT/ZeusVPN" "$STAGE/usr/bin/zeusvpn"
install -Dm644 "$ROOT/build/linux/zeusvpn.desktop" "$STAGE/usr/share/applications/zeusvpn.desktop"
install -Dm644 "$ROOT/build/appicon.png" "$STAGE/usr/share/icons/hicolor/1024x1024/apps/zeusvpn.png"

# .deb (Debian, Ubuntu, Mint, ...)
mkdir -p "$STAGE/DEBIAN"
cat > "$STAGE/DEBIAN/control" <<CONTROL
Package: zeusvpn
Version: $VERSION
Architecture: $ARCH
Maintainer: Zeus VPN <noreply@github.com>
Depends: libgtk-3-0t64 | libgtk-3-0, libwebkit2gtk-4.1-0, pkexec | policykit-1
Section: net
Priority: optional
Homepage: https://github.com/Khode-ZeUS/Zeus-VPN
Description: Mozilla VPN client
 Sign in with a free Mozilla account, pick a country and connect.
 System proxy and full-system VPN (TUN) modes.
CONTROL
dpkg-deb --root-owner-group --build "$STAGE" "$OUT/zeusvpn_${VERSION}_${ARCH}.deb"
rm -rf "$STAGE/DEBIAN"

# .tar.gz for other distributions: extract to / or run usr/bin/zeusvpn.
tar -C "$STAGE" -czf "$OUT/ZeusVPN-linux-$ARCH.tar.gz" usr
