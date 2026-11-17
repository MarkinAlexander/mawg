#!/bin/sh
# uninstall.sh - чистый снос mawg + MagiTrickle с роутера
# использование: sh uninstall.sh [--keep-data]
#   по умолчанию удаляет ВСЁ: mawg (бинарь, сервис, лог, данные), движки
#   sing-box-lx/mihomo (поставленные mawg'ом), MagiTrickle (пакет, сервис,
#   состояние, iptables-цепочки MT_*), возвращает dnsmasq с MT:3553 на
#   прежние серверы. --keep-data сохраняет /etc/mawg (пулы, настройки
#   панели) на случай «передумал».
# Платформы: OpenWrt 24 (opkg) и 25 (apk), Keenetic с Entware (/opt).
# Пулы удаляйте в ПАНЕЛИ ДО запуска - uci-интерфейсы скрипт не сносит:
# имена секций могут совпадать с вашими собственными wireguard-интерфейсами.
# Скрипт не трогает: системные пакеты WireGuard/AmneziaWG, mt-c, ваши
# iptables-правила вне цепочек MT_*.

set -u

KEEP_DATA=0
for arg in "$@"; do
    case "$arg" in
        --keep-data) KEEP_DATA=1;;
        -h|--help) sed -n '2,13p' "$0"; exit 0;;
        *) echo "ОШИБКА: неизвестный аргумент $arg (см. --help)" >&2; exit 1;;
    esac
done

say() { echo "== $*"; }
warn() { echo "-- ВНИМАНИЕ: $*" >&2; }

if [ -d /opt/etc ] && [ -x /opt/bin/opkg ]; then
    PLATFORM=keenetic
elif [ -f /etc/openwrt_release ]; then
    PLATFORM=openwrt
else
    echo "ОШИБКА: это не OpenWrt и не Keenetic с Entware" >&2
    exit 1
fi

# OpenWrt 25 - apk, 24.10 и раньше - opkg (как и Entware)
if command -v apk >/dev/null 2>&1; then
    PKG=apk
elif command -v opkg >/dev/null 2>&1; then
    PKG=opkg
else
    echo "ОШИБКА: не найден ни apk (OpenWrt 25), ни opkg (OpenWrt 24)" >&2
    exit 1
fi

case "$PLATFORM" in
    openwrt)
        BIN=/usr/bin/mawg; INIT=/etc/init.d/mawg; DATA=/etc/mawg; LOG=/var/log/mawg.log
        MT_INIT=/etc/init.d/magitrickle
        ;;
    keenetic)
        BIN=/opt/bin/mawg; INIT=/opt/etc/init.d/S99mawg; DATA=/opt/etc/mawg; LOG=/tmp/mawg.log
        MT_INIT=/opt/etc/init.d/S99magitrickle
        ;;
esac

pkg_installed() {
    case "$PKG" in
        apk) apk info -e "$1" >/dev/null 2>&1;;
        *) opkg list-installed 2>/dev/null | grep -q "^$1 - ";;
    esac
}

pkg_remove() {
    say "удаление пакета $1"
    case "$PKG" in
        apk) apk del "$1" >/dev/null 2>&1;;
        *) opkg remove "$1" >/dev/null 2>&1;;
    esac
}

# restore_dnsmasq - вернуть dnsmasq с MT:3553 на прежние серверы ДО сноса
# MagiTrickle: панель в режиме «через MT» прячет прежний список в
# mawg_server_bak; пусто = прежнего списка не было (дефолт WAN). Без
# возврата после сноса MT вся LAN остаётся с мёртвым DNS.
restore_dnsmasq() {
    srv=$(uci show dhcp.@dnsmasq[0].server 2>/dev/null)
    case "$srv" in
        *127.0.0.1#3553*)
            uci -q delete dhcp.@dnsmasq[0].server
            bak=$(uci show dhcp.@dnsmasq[0].mawg_server_bak 2>/dev/null | sed -E "s/^.*='(.*)'$/\1/")
            for s in $bak; do uci add_list dhcp.@dnsmasq[0].server="$s"; done
            uci -q delete dhcp.@dnsmasq[0].mawg_server_bak
            uci commit dhcp
            /etc/init.d/dnsmasq restart >/dev/null 2>&1
            say "dnsmasq возвращён с MT:3553 на прежние серверы${bak:+: $bak}"
            ;;
    esac
}

# flush_mt_chains - снять jump-правила в родительских цепочках и вычистить
# MT_* (remap MT_DNSOR + группы правил); без промывки DNAT клиентов на
# :3553 остаётся висеть на мёртвый порт после удаления MT
flush_mt_chains() {
    if [ "$PLATFORM" = openwrt ]; then IPT=/usr/sbin/iptables; else IPT=/opt/sbin/iptables; fi
    [ -x "$IPT" ] || IPT=$(command -v iptables 2>/dev/null) || IPT=""
    if [ -z "$IPT" ]; then
        warn "iptables не найден: проверьте остатки MT-правил в nft (nft list ruleset | grep MT_)"
        return 0
    fi
    for t in nat mangle filter; do
        "$IPT" -t "$t" -S 2>/dev/null | grep ' -j MT_' | sed 's/^-A/-D/' | while read -r rule; do
            # разделение по пробелам осознанное: правила без кавычек-комментов
            "$IPT" -t "$t" $rule >/dev/null 2>&1
        done
        for c in $("$IPT" -t "$t" -S 2>/dev/null | grep -oE '^-N MT_[A-Za-z0-9_-]+' | awk '{print $2}'); do
            "$IPT" -t "$t" -F "$c" >/dev/null 2>&1
            "$IPT" -t "$t" -X "$c" >/dev/null 2>&1
        done
    done
}

# движковые бинари (sing-box-lx, mihomo) ставились mawg'ом: маркер-каталог
# - признак «поставлено mawg», без него бинарь не трогаем (вдруг пользователь
# ставил свой sing-box в тот же путь). Пути литералами по платформам
remove_engines() {
    if [ "$PLATFORM" = openwrt ]; then
        if [ -d /etc/sing-box-lx ]; then
            killall sing-box 2>/dev/null; rm -f /usr/bin/sing-box; rm -rf /etc/sing-box-lx
            say "движок sing-box удалён"
        fi
        if [ -d /etc/mihomo-mawg ]; then
            killall mihomo 2>/dev/null; rm -f /usr/bin/mihomo; rm -rf /etc/mihomo-mawg
            say "движок mihomo удалён"
        fi
    else
        if [ -d /opt/etc/sing-box-lx ]; then
            killall sing-box 2>/dev/null; rm -f /opt/bin/sing-box; rm -rf /opt/etc/sing-box-lx
            say "движок sing-box удалён"
        fi
        if [ -d /opt/etc/mihomo-mawg ]; then
            killall mihomo 2>/dev/null; rm -f /opt/bin/mihomo; rm -rf /opt/etc/mihomo-mawg
            say "движок mihomo удалён"
        fi
    fi
}

remove_magitrickle() {
    if [ "$PLATFORM" = openwrt ]; then
        /etc/init.d/magitrickle stop >/dev/null 2>&1
    else
        /opt/etc/init.d/S99magitrickle stop >/dev/null 2>&1
    fi
    killall magitrickled 2>/dev/null
    if pkg_installed magitrickle; then
        pkg_remove magitrickle
    fi
    rm -f "$MT_INIT"
    flush_mt_chains
    # apk/opkg не сносят изменённые каталоги состояния (живой кейс 17.11
    # на 0.62: /etc/magitrickle/state выжил после remove пакета)
    if [ "$PLATFORM" = openwrt ]; then rm -rf /etc/magitrickle; else rm -rf /opt/etc/magitrickle; fi
    say "MagiTrickle удалён (пакет, сервис, состояние, iptables-цепочки MT_*)"
}

pool_cleanup_hint() {
    secs=$(uci show network 2>/dev/null | grep -E "\.proto='(wireguard|amneziawg)'" | sed -E "s/^network\.([^.]+)\..*/\1/" | tr '\n' ' ')
    if [ -n "$secs" ]; then
        warn "остались uci-секции туннелей: $secs - если это пулы mawg (а не ваши собственные), удалите: uci delete network.<имя> для каждого, затем uci commit network && /etc/init.d/network reload"
    fi
    if [ "$PLATFORM" = keenetic ]; then
        warn "слоты ndm (WireguardN) от пулов не удаляются автоматически - удалите пулы в панели ДО uninstall или снесите слоты в веб-интерфейсе Keenetic"
    fi
}

say "платформа: $PLATFORM, пакетный менеджер: $PKG"
say "удаление mawg"
if [ -f "$INIT" ]; then "$INIT" stop >/dev/null 2>&1; fi
# mawg и его движковые пулы могли остаться процессами при мёртвом init
killall mawg sing-box mihomo 2>/dev/null
restore_dnsmasq
rm -f "$BIN" "$INIT" "$LOG"
remove_engines
remove_magitrickle
if [ "$KEEP_DATA" = 1 ]; then
    say "данные $DATA сохранены (--keep-data)"
else
    rm -rf "$DATA"
    say "данные $DATA удалены"
fi
pool_cleanup_hint
say "готово"
