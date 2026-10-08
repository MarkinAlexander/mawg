#!/bin/sh
# mawg - Multi-AWG Changer: установщик и обновлялка
# использование: sh install.sh [-u|--update] [-r|--remove] [--purge]
#   [--with-magitrickle] [--without-magitrickle] [--with-awg3]
# по умолчанию: определить платформу, проверить зависимости (спросить при
# необходимости), скачать бинарник последнего релиза, поставить сервис.
# MAWG_DEBUG=1 - печатать тайминги шагов (в stderr).
# Обновление (-u) не вызывает apk/opkg вовсе: скачивание существующим
# curl или wget; пакетный менеджер нужен только если fetcher-а нет вообще.

set -u

REPO="${MAWG_REPO:-MarkinAlexander/mawg}"
DL_BASE="https://github.com/${REPO}/releases/latest/download"
TMP="/tmp/mawg-install.$$"
MODE="install"
WANT_MT="ask"
WANT_AWG3="no"
DEBUG="${MAWG_DEBUG:-0}"
PKG_MANAGER=""
FETCHER=""
START_TS=$(date +%s)
STEP_TS=$START_TS

say() { echo "== $*"; }
warn() { echo "-- ВНИМАНИЕ: $*" >&2; }
die() { echo "ОШИБКА: $*" >&2; rm -rf "$TMP"; exit 1; }
mark() {
    [ "$DEBUG" = 1 ] || return 0
    now=$(date +%s)
    echo "[debug] $* : $((now - STEP_TS))с (всего $((now - START_TS))с)" >&2
    STEP_TS=$now
}

for arg in "$@"; do
    case "$arg" in
        -u|--update) MODE="update";;
        -r|--remove) MODE="remove";;
        --purge) MODE="purge";;
        --with-magitrickle) WANT_MT="yes";;
        --without-magitrickle) WANT_MT="no";;
        --with-awg3) WANT_AWG3="yes";;
        -h|--help) sed -n '2,9p' "$0"; exit 0;;
        *) die "неизвестный аргумент $arg (см. --help)";;
    esac
done

detect_pkg_manager() {
    if [ "$PLATFORM" = keenetic ]; then
        echo /opt/bin/opkg
    elif test -x /bin/opkg; then
        echo /bin/opkg
    elif command -v apk >/dev/null 2>&1; then
        echo apk
    elif command -v opkg >/dev/null 2>&1; then
        echo opkg
    else
        die "не найден пакетный менеджер apk или opkg"
    fi
}

need_pkg() {
    [ -n "$PKG_MANAGER" ] && return 0
    PKG_MANAGER=$(detect_pkg_manager)
}

pkg_update() { "$PKG_MANAGER" update; }

pkg_install() {
    case "$PKG_MANAGER" in
        apk) apk add "$@";;
        *) "$PKG_MANAGER" install "$@";;
    esac
}

pkg_installed() {
    case "$PKG_MANAGER" in
        apk) apk info -e "$1" >/dev/null 2>&1;;
        *) "$PKG_MANAGER" list-installed 2>/dev/null | grep -q "^$1 - ";;
    esac
}

pick_fetcher() {
    if command -v curl >/dev/null 2>&1; then
        echo curl
    elif command -v wget >/dev/null 2>&1; then
        echo wget
    fi
}

do_fetch() {
    if [ "$FETCHER" = curl ]; then
        curl -fsSL "$1" -o "$2"
    else
        wget -qO "$2" "$1"
    fi
}

ensure_fetcher() {
    FETCHER=$(pick_fetcher)
    if [ -n "$FETCHER" ]; then
        mark "fetcher: $FETCHER"
        return 0
    fi
    need_pkg
    say "нет curl и wget, ставлю curl через $PKG_MANAGER (один $PKG_MANAGER update)"
    pkg_update >/dev/null 2>&1
    pkg_install curl >/dev/null 2>&1
    FETCHER=$(pick_fetcher)
    [ "$FETCHER" = curl ] || die "нужен curl или wget с https (установка curl через $PKG_MANAGER не удалась)"
    mark "установка curl"
}

ask() {
    printf '%s [y/N]: ' "$1"
    if [ -t 0 ]; then
        read -r answer
        [ "$answer" = "y" ] || [ "$answer" = "Y" ] || [ "$answer" = "д" ] || [ "$answer" = "yes" ]
    else
        echo "n (нет терминала, по умолчанию)"
        return 1
    fi
}

detect_platform() {
    if [ -f /etc/openwrt_release ]; then
        echo openwrt
        return
    fi
    if [ -x /bin/ndmc ] || [ -x /opt/bin/ndmc ]; then
        echo keenetic
        return
    fi
    echo ""
}

detect_arch() {
    m=$(uname -m 2>/dev/null || echo unknown)
    case "$m" in
        aarch64|arm64) echo arm64; return;;
        armv7*|armv6*|armv5*|arm) echo arm; return;;
        i[3-6]86|x86) echo 386; return;;
        x86_64|amd64) echo amd64; return;;
        riscv64) echo riscv64; return;;
    esac
    if command -v apk >/dev/null 2>&1; then
        case "$(apk --print-arch 2>/dev/null)" in
            mipsel*|mipsle*) echo mipsle; return;;
            mips|mips_*) echo mips; return;;
        esac
    fi
    if command -v opkg >/dev/null 2>&1; then
        archs=$(opkg print-architecture 2>/dev/null | awk '{print $2}')
        if echo "$archs" | grep -q '^mipsel'; then echo mipsle; return; fi
        if echo "$archs" | grep -qx 'mips'; then echo mips; return; fi
    fi
    warn "энддиан mips не определен (нет opkg), предполагаю little-endian"
    echo mipsle
}

PLATFORM=$(detect_platform)
[ -n "$PLATFORM" ] || die "платформа не распознана (поддерживаются OpenWrt и Keenetic c Entware)"
ARCH=$(detect_arch)
[ -n "$ARCH" ] || die "архитектура $(uname -m) не поддерживается"

case "$PLATFORM" in
    openwrt)
        BIN=/usr/bin/mawg
        INIT=/etc/init.d/mawg
        DATA=/etc/mawg
        LOG=/var/log/mawg.log
        ;;
    keenetic)
        [ -x /opt/bin/opkg ] || die "Entware не найдена. Установите Entware на USB (https://help.keenetic.com), затем запустите установщик снова."
        BIN=/opt/bin/mawg
        INIT=/opt/etc/init.d/S99mawg
        DATA=/opt/etc/mawg
        LOG=/tmp/mawg.log
        ;;
esac

svc_stop() {
    if [ "$PLATFORM" = openwrt ]; then
        /etc/init.d/mawg stop >/dev/null 2>&1
    else
        /opt/etc/init.d/S99mawg stop >/dev/null 2>&1
    fi
}

svc_start() {
    if [ "$PLATFORM" = openwrt ]; then
        /etc/init.d/mawg enable >/dev/null 2>&1
        /etc/init.d/mawg start >/dev/null 2>&1
    else
        chmod +x "$INIT"
        "$INIT" start >/dev/null 2>&1
    fi
}

write_init_openwrt() {
    cat > "$INIT" <<'EOF'
#!/bin/sh /etc/rc.common

START=99
USE_PROCD=1

start_service() {
    procd_open_instance
    procd_set_param command /usr/bin/mawg
    procd_set_param respawn 3600 5 0
    procd_set_param stdout 1
    procd_set_param stderr 1
    procd_set_param pidfile /var/run/mawg.pid
    procd_close_instance
}
EOF
    chmod +x "$INIT"
}

write_init_keenetic() {
    cat > "$INIT" <<'EOF'
#!/bin/sh

case "$1" in
  start)
    start-stop-daemon -S -b -m -p /opt/var/run/mawg.pid -x /opt/bin/mawg -- -base /opt/etc/mawg
    ;;
  stop)
    start-stop-daemon -K -p /opt/var/run/mawg.pid -x /opt/bin/mawg
    ;;
  restart)
    "$0" stop
    sleep 1
    "$0" start
    ;;
  status)
    if [ -f /opt/var/run/mawg.pid ] && kill -0 "$(cat /opt/var/run/mawg.pid)" 2>/dev/null; then
      echo running
    else
      echo stopped
    fi
    ;;
  *)
    echo "Usage: $0 {start|stop|restart|status}"
    ;;
esac
EOF
    chmod +x "$INIT"
}

report() {
    ip=$(ip -4 addr show 2>/dev/null | grep -o 'inet 192\.[0-9.]*' | head -1 | awk '{print $2}')
    [ -n "$ip" ] || ip=$(ifconfig 2>/dev/null | grep -o 'addr:192\.[0-9.]*' | head -1 | cut -d: -f2)
    echo
    echo "==============================="
    echo " mawg установлен"
    echo " платформа:   $PLATFORM ($ARCH)"
    echo " бинарник:    $BIN"
    echo " данные:      $DATA"
    echo " лог:         $LOG"
    echo " веб:         http://${ip:-<ip-роутера>}:8090"
    echo "==============================="
    if [ -f "$DATA/first-auth.txt" ]; then
        echo
        echo "-- ВНИМАНИЕ: включилась авторизация веб-панели"
        cat "$DATA/first-auth.txt"
        echo " смените пароль в веб-интерфейсе (Настройки) - подсказка удалится"
        echo " сброс из терминала: $(basename "$BIN") -reset-auth"
    fi
}

if [ "$MODE" = remove ] || [ "$MODE" = purge ]; then
    say "удаление mawg"
    svc_stop
    rm -f "$BIN" "$INIT"
    if [ "$MODE" = purge ]; then
        rm -rf "$DATA"
        say "данные $DATA удалены"
    else
        say "данные $DATA сохранены (для полного удаления: --purge)"
    fi
    exit 0
fi

if [ "$MODE" = install ]; then
    need_pkg
    say "платформа: $PLATFORM, архитектура: $ARCH, пакеты: $PKG_MANAGER"
else
    say "платформа: $PLATFORM, архитектура: $ARCH, обновление"
fi
mark "определение платформы"

ensure_fetcher
mkdir -p "$TMP" || die "не создать $TMP"

if [ "$MODE" = install ]; then
    pkg_update_ok=0
    say "$PKG_MANAGER update"
    pkg_update >/dev/null 2>&1 && pkg_update_ok=1
    [ "$pkg_update_ok" = 1 ] || warn "$PKG_MANAGER update не удался, продолжаю без него"
    mark "$PKG_MANAGER update"

    if [ "$PLATFORM" = keenetic ]; then
        say "проверка компонента WireGuard"
        if /bin/ndmc -c 'show version' 2>/dev/null | grep -qi 'wireguard'; then
            echo "   компонент WireGuard уже установлен"
        else
            say "установка системного компонента WireGuard"
            /bin/ndmc -c 'components install wireguard' >/dev/null 2>&1 \
                && /bin/ndmc -c 'components commit' >/dev/null 2>&1 \
                || warn "не удалось поставить компонент WireGuard автоматически. Установите его в Web UI: Обновления - Компоненты - WireGuard."
        fi
    else
        say "проверка пакетов WireGuard"
        if ! pkg_installed wireguard-tools; then
            [ "$pkg_update_ok" = 1 ] || die "нет wireguard-tools и $PKG_MANAGER update не работал"
            pkg_install kmod-wireguard wireguard-tools luci-proto-wireguard >/dev/null 2>&1 \
                || warn "не удалось установить пакеты WireGuard"
        else
            echo "   wireguard-tools уже установлен"
        fi
        say "проверка AmneziaWG"
        if pkg_installed amneziawg-tools; then
            echo "   amneziawg-tools уже установлен"
        else
            [ "$pkg_update_ok" = 1 ] && pkg_install kmod-amneziawg amneziawg-tools >/dev/null 2>&1 \
                || warn "в стоковом репозитории нет amneziawg. Для AWG 2.x/3.x серверов запустите позже: sh $0 --with-awg3"
        fi
        if [ "$WANT_AWG3" = yes ]; then
            say "обновление до AmneziaWG 3.1 (репозиторий Slava-Shchipunov)"
            warn "заменяет kmod, после установки потребуется перезагрузка роутера"
            if ask "продолжить обновление AmneziaWG до 3.1"; then
                do_fetch "https://raw.githubusercontent.com/2Grey/awg-openwrt/refs/heads/master/amneziawg-install.sh" "$TMP/awg.sh" \
                    && sh "$TMP/awg.sh" -e -n < /dev/null \
                    || warn "обновление AWG не удалось, смотрите вывод выше"
                warn "перезагрузите роутер после установки"
            fi
        fi
    fi

    mt_installed=0
    if pkg_installed magitrickle; then
        mt_installed=1
        echo "   magitrickle уже установлен"
    fi
    if [ "$mt_installed" = 0 ]; then
        do_mt=no
        if [ "$WANT_MT" = yes ]; then
            do_mt=yes
        elif [ "$WANT_MT" = ask ]; then
            say "MagiTrickle не найден: маршрутизация по доменам работать не будет"
            if ask "добавить репозиторий bin.magitrickle.dev и установить MagiTrickle"; then
                do_mt=yes
            fi
        fi
        if [ "$do_mt" = yes ] && [ "$pkg_update_ok" = 1 ]; then
            say "установка MagiTrickle"
            do_fetch "http://bin.magitrickle.dev/packages/add_repo.sh" "$TMP/mt.sh" || warn "не удалось скачать add_repo.sh"
            if [ -s "$TMP/mt.sh" ]; then
                if ! sh "$TMP/mt.sh" >/dev/null 2>&1 || ! pkg_update >/dev/null 2>&1; then
                    warn "не удалось добавить или обновить репозиторий MagiTrickle"
                elif [ "$PLATFORM" = keenetic ]; then
                    pkg_install magitrickle socat >/dev/null 2>&1 \
                        && chmod +x /opt/etc/init.d/S99magitrickle 2>/dev/null \
                        && /opt/etc/init.d/S99magitrickle start >/dev/null 2>&1 \
                        || warn "magitrickle не установился"
                else
                    pkg_install magitrickle >/dev/null 2>&1 \
                        && /etc/init.d/magitrickle enable >/dev/null 2>&1 \
                        && /etc/init.d/magitrickle start >/dev/null 2>&1 \
                        || warn "magitrickle не установился"
                fi
            fi
        fi
    fi
fi

say "загрузка mawg-linux-$ARCH"
if [ -n "${MAWG_BINARY:-}" ]; then
    cp "$MAWG_BINARY" "$TMP/mawg" || die "не удалось скопировать MAWG_BINARY=$MAWG_BINARY"
else
    if ! do_fetch "$DL_BASE/mawg-linux-$ARCH" "$TMP/mawg"; then
        rm -f "$TMP/mawg"
        if [ "$MODE" = install ] && [ "$FETCHER" = wget ] && [ -z "${MAWG_BINARY:-}" ]; then
            warn "wget не смог скачать (возможно, не умеет https), ставлю curl и пробую снова"
            need_pkg
            pkg_update >/dev/null 2>&1
            pkg_install curl >/dev/null 2>&1
            FETCHER=$(pick_fetcher)
            do_fetch "$DL_BASE/mawg-linux-$ARCH" "$TMP/mawg" \
                || die "не удалось скачать mawg-linux-$ARCH (релизы: https://github.com/$REPO/releases)"
        else
            die "не удалось скачать mawg-linux-$ARCH wget-ом (релизы: https://github.com/$REPO/releases). Если wget не умеет https - поставьте curl и запустите снова"
        fi
    fi
    mark "скачивание бинарника"
    if do_fetch "$DL_BASE/sha256sum.txt" "$TMP/sha256sum.txt" 2>/dev/null; then
        want=$(grep "mawg-linux-$ARCH\$" "$TMP/sha256sum.txt" | awk '{print $1}')
        got=$(sha256sum "$TMP/mawg" | awk '{print $1}')
        if [ -z "$want" ]; then
            warn "в sha256sum.txt нет строки для mawg-linux-$ARCH"
        elif [ "$want" != "$got" ]; then
            die "контрольная сумма не совпала: $got, ожидалось $want"
        else
            say "checksum ok"
        fi
    else
        warn "sha256sum.txt недоступен, проверка суммы пропущена"
    fi
    mark "проверка checksum"
fi
size=$(wc -c < "$TMP/mawg" 2>/dev/null || echo 0)
[ "$size" -gt 500000 ] || die "скачанный файл подозрительно мал ($size байт)"

say "установка $BIN"
svc_stop
mark "остановка сервиса"
mv "$TMP/mawg" "$BIN" || die "не удалось записать $BIN"
chmod +x "$BIN"
mkdir -p "$DATA"

if [ "$PLATFORM" = openwrt ]; then
    write_init_openwrt
else
    write_init_keenetic
fi

svc_start
sleep 1
mark "запуск сервиса"
if [ "$PLATFORM" = openwrt ]; then
    pgrep -f /usr/bin/mawg >/dev/null 2>&1 || warn "mawg не запустился, смотрите $LOG"
else
    pgrep -f /opt/bin/mawg >/dev/null 2>&1 || warn "mawg не запустился, смотрите $LOG"
fi

rm -rf "$TMP"
report
