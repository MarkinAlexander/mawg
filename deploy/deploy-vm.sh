#!/bin/sh
# build and deploy mawg to the OpenWrt VM
# пароль/ключ берутся из окружения: MAWG_VM_PW (или MAWG_VM_KEY - путь к
# приватному ключу PuTTY). В репозитории паролей нет.
set -e
TARGET="${MAWG_VM_HOST:?Set MAWG_VM_HOST (for example root@router.example)}"
cd "$(dirname "$0")/.."
AUTH=""
if [ -n "${MAWG_VM_KEY:-}" ]; then AUTH="-i $MAWG_VM_KEY"; fi
PWARG=""
if [ -n "${MAWG_VM_PW:-}" ]; then PWARG="-pw $MAWG_VM_PW"; fi
GOOS=linux GOARCH=386 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o dist/mawg-386 ./cmd/mawg
sh deploy/build.sh 386 >/dev/null && "/c/Program Files/PuTTY/pscp" -scp -batch $AUTH $PWARG dist/mawg-386 "$TARGET:/tmp/mawg"
"/c/Program Files/PuTTY/plink" -ssh -batch $AUTH $PWARG "$TARGET" "
  cp /tmp/mawg /usr/bin/mawg && chmod +x /usr/bin/mawg &&
  cp /tmp/mawg-init.sh /etc/init.d/mawg 2>/dev/null || true
  /etc/init.d/mawg restart 2>/dev/null || /usr/bin/mawg -base /etc/mawg &
"
echo deployed
