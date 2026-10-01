#!/bin/sh
# build and deploy mawg to the OpenWrt VM
set -e
cd "$(dirname "$0")/.."
GOOS=linux GOARCH=386 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/mawg-386 ./cmd/mawg
sh deploy/build.sh 386 >/dev/null && "/c/Program Files/PuTTY/pscp" -scp -batch -pw REDACTED-PASSWORD dist/mawg-386 root@192.168.0.62:/tmp/mawg
"/c/Program Files/PuTTY/plink" -ssh -batch -pw REDACTED-PASSWORD root@192.168.0.62 "
  cp /tmp/mawg /usr/bin/mawg && chmod +x /usr/bin/mawg &&
  cp /tmp/mawg-init.sh /etc/init.d/mawg 2>/dev/null || true
  /etc/init.d/mawg restart 2>/dev/null || /usr/bin/mawg -base /etc/mawg &
"
echo deployed
