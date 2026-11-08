#!/bin/sh
# Build a .deb with plain dpkg-deb (no nfpm needed). Usage: packaging/mkdeb.sh VERSION
set -eu
VERSION=${1:-0.1.0}
ARCH=$(dpkg --print-architecture)
ROOT=$(mktemp -d)
trap 'rm -rf "$ROOT"' EXIT
chmod 755 "$ROOT"

install -Dm755 bin/drift                  "$ROOT/usr/bin/drift"
install -Dm755 bin/drift-render           "$ROOT/usr/bin/drift-render"
install -Dm644 packaging/drift.service    "$ROOT/usr/lib/systemd/user/drift.service"
install -Dm644 cmd/drift/drift.conf.example "$ROOT/etc/drift/drift.conf"
install -Dm644 README.md                  "$ROOT/usr/share/doc/drift/README.md"
install -Dm644 LICENSE                    "$ROOT/usr/share/doc/drift/LICENSE"
mkdir -p "$ROOT/usr/share/drift/savers" "$ROOT/usr/share/doc/drift/examples/savers"
cp examples/savers/* "$ROOT/usr/share/doc/drift/examples/savers/"

mkdir -p "$ROOT/DEBIAN"
cat > "$ROOT/DEBIAN/control" <<CTRL
Package: drift
Version: ${VERSION#v}
Section: x11
Priority: optional
Architecture: $ARCH
Depends: libc6, libgl1, libx11-6, libxrandr2, libxcursor1, libxinerama1, libxi6, libxxf86vm1
Recommends: mpv
Maintainer: Kyle <noreply@archavist.com>
Description: moving screensavers for OLED burn-in protection
 Classic-style animated savers, your own images as moving sprites, and
 looped videos via mpv. Works with GNOME and KDE on Wayland or X11.
CTRL
echo /etc/drift/drift.conf > "$ROOT/DEBIAN/conffiles"
install -m755 packaging/postinstall.sh "$ROOT/DEBIAN/postinst"
install -m755 packaging/preremove.sh   "$ROOT/DEBIAN/prerm"

mkdir -p dist
dpkg-deb --root-owner-group --build "$ROOT" "dist/drift_${VERSION#v}_${ARCH}.deb"
