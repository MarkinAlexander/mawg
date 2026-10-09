#!/bin/sh
# build release binaries for both routers, packed with upx when available
# usage: sh deploy/build.sh [mipsle|386|both]
set -e
cd "$(dirname "$0")/.."
TARGET="${1:-both}"
UPX=""
for c in ./tools/upx-*/upx.exe ./tools/upx-*/upx upx; do
  if command -v "$c" >/dev/null 2>&1; then UPX="$c"; break; fi
done
# MAWG_UPX - явный путь к upx (например, при сборке в worktree без tools/)
if [ -z "$UPX" ] && [ -n "$MAWG_UPX" ]; then UPX="$MAWG_UPX"; fi

VERSION="$(git describe --tags 2>/dev/null || echo dev)"

build() {
  os="$1"; arch="$2"; extra="$3"; out="$4"
  echo "== building $out ($VERSION)"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" $extra go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "dist/$out" ./cmd/mawg
  if [ -n "$UPX" ]; then
    echo "== packing with upx"
    if "$UPX" --best --lzma -f "dist/$out" -o "dist/$out.upx" >/dev/null && [ -s "dist/$out.upx" ]; then
      mv "dist/$out.upx" "dist/$out"
    else
      rm -f "dist/$out.upx"
      echo "ОШИБКА: upx не смог упаковать dist/$out - распакованный бинарь в релиз не пойдёт; проверьте MAWG_UPX/антивирус" >&2
      exit 1
    fi
  else
    echo "warning: upx not found, binary left unpacked"
  fi
  ls -la "dist/$out"
}

[ "$TARGET" = "mipsle" ] || [ "$TARGET" = "both" ] && GOMIPS=softfloat true
if [ "$TARGET" = "mipsle" ] || [ "$TARGET" = "both" ]; then
  export GOMIPS=softfloat
  build linux mipsle "" mawg-mipsle
  unset GOMIPS
fi
if [ "$TARGET" = "386" ] || [ "$TARGET" = "both" ] || [ "$TARGET" = "all" ]; then
  build linux 386 "" mawg-386
fi
if [ "$TARGET" = "arm64" ] || [ "$TARGET" = "all" ]; then
  build linux arm64 "" mawg-arm64
fi
