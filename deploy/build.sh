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

build() {
  os="$1"; arch="$2"; extra="$3"; out="$4"
  echo "== building $out"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" $extra go build -trimpath -ldflags "-s -w" -o "dist/$out" ./cmd/mawg
  if [ -n "$UPX" ]; then
    echo "== packing with upx"
    "$UPX" --best --lzma -f "dist/$out" -o "dist/$out.upx" >/dev/null
    mv "dist/$out.upx" "dist/$out"
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
if [ "$TARGET" = "386" ] || [ "$TARGET" = "both" ]; then
  build linux 386 "" mawg-386
fi
