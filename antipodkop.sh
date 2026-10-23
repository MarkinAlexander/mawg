#!/bin/sh
# antipodkop.sh - полный выпил podkop (и его окружения) с OpenWrt
# с восстановлением работающего DNS.
#
# Использование:
#   sh <(wget -qO - https://raw.githubusercontent.com/MarkinAlexander/mawg/master/antipodkop.sh)
#   # с удалением getdomains (itdoginfo) их официальным анинсталлом:
#   sh <(wget -qO - .../antipodkop.sh) --getdomains
#   # посмотреть, что будет сделано, ничего не выполняя:
#   sh antipodkop.sh --dry-run
#
# Скрипт:
#   - останавливает и сносит пакеты podkop (+i18n), https-dns-proxy,
#     stubby/dnscrypt (если стоят);
#   - БЕРЕЖЁТ lx-ядро sing-box (версия с "-lx.", ставится панелью mawg):
#     если /usr/bin/sing-box - lx, пакет sing-box удаляется из базы opkg
#     БЕЗ удаления файла;
#   - чистит dnsmasq от podkop-перехвата и возвращает DNS: на MagiTrickle
#     (127.0.0.1#3553), если стоит mawg, иначе на 8.8.8.8/1.1.1.1;
#   - удаляет nft-таблицы, файлы и cron-строки podkop;
#   - всё, что меняет, предварительно бэкапит в /root/antipodkop-backup-*.

set -u

DRY=0
WITH_GETDOMAINS=0
for a in "$@"; do
    case "$a" in
        --dry-run) DRY=1 ;;
        --getdomains) WITH_GETDOMAINS=1 ;;
        *) echo "неизвестный аргумент: $a (есть --dry-run, --getdomains)"; exit 2 ;;
    esac
done

say() { echo "==> $*"; }
warn() { echo "    ! $*"; }
run() {
    if [ "$DRY" = "1" ]; then echo "    [dry-run] $*"; return 0; fi
    "$@"
}

[ "$(id -u)" = "0" ] || { echo "запускай под root"; exit 1; }

TS=$(date +%Y%m%d-%H%M%S)
BK="/root/antipodkop-backup-$TS"
mkdir -p "$BK"
uci export dhcp > "$BK/dhcp.export" 2>/dev/null
cp -a /etc/config "$BK/etc-config" 2>/dev/null
say "бэкап: $BK"

# ---------------------------------------------------------------- пакеты ---

say "остановка podkop"
run /etc/init.d/podkop stop 2>/dev/null
run /etc/init.d/podkop disable 2>/dev/null

say "удаление пакетов podkop"
PKGS=""
for p in $(opkg list-installed 2>/dev/null | awk '{print $1}'); do
    case "$p" in
        podkop|luci-app-podkop|luci-i18n-podkop-*|https-dns-proxy|luci-app-https-dns-proxy|luci-i18n-https-dns-proxy-*|stubby|dnscrypt-proxy2)
            PKGS="$PKGS $p" ;;
    esac
done
if [ -n "$PKGS" ]; then
    run opkg remove --force-depends $PKGS
else
    echo "    podkop-пакетов в opkg не найдено"
fi

# ------------------------------------------------------- getdomains (opt) --

if [ -f /usr/bin/getdomains ] || [ -f /etc/init.d/getdomains ]; then
    if [ "$WITH_GETDOMAINS" = "1" ]; then
        say "getdomains: запуск официального анинсталла itdoginfo"
        run sh -c 'wget -qO - https://raw.githubusercontent.com/itdoginfo/domain-routing-openwrt/refs/heads/master/getdomains-uninstall.sh | sh'
    else
        warn "найден getdomains - запусти с --getdomains, чтобы вычистить и его"
    fi
fi

# ------------------------------------------------- sing-box: беречь lx ----

LX=0
if [ -x /usr/bin/sing-box ]; then
    if /usr/bin/sing-box version 2>/dev/null | grep -q -- "-lx."; then
        LX=1
        say "sing-box = lx-ядро (панель mawg) - СОХРАНЯЮ файл, вычищаю только запись opkg"
        if opkg list-installed 2>/dev/null | grep -q '^sing-box '; then
            # opkg remove снёс бы НАШ бинарь - снимаю пакет с базы руками
            run rm -f /usr/lib/opkg/info/sing-box.*
            run sed -i '/^Package: sing-box$/,/^$/d' /usr/lib/opkg/status
            warn "запись opkg о sing-box удалена; НЕ делай 'opkg upgrade sing-box' - затрёт lx"
        fi
    else
        say "sing-box - upstream (не lx) - удаляю пакетом"
        run opkg remove --force-depends sing-box 2>/dev/null
    fi
fi

# ------------------------------------------------------------- dnsmasq ----

say "чистка dnsmasq от podkop-перехвата"
# цель: MagiTrickle если стоит mawg, иначе публичные
TARGET="8.8.8.8"
if [ -x /usr/bin/magitrickled ] || [ -f /etc/init.d/magitrickle ] || [ -f /opt/etc/init.d/S99magitrickle ]; then
    TARGET="127.0.0.1#3553"
    say "найден MagiTrickle - DNS роутера направлю в MT (127.0.0.1#3553)"
else
    say "MagiTrickle не найден - DNS роутера направлю на 8.8.8.8"
fi

if [ "$DRY" = "1" ]; then
    echo "    [dry-run] uci: delete server*; delete noresolv; delete cachesize; add server=$TARGET; commit"
else
    while uci -q get dhcp.@dnsmasq[0].server >/dev/null 2>&1; do
        uci -q delete dhcp.@dnsmasq[0].server
    done
    uci -q delete dhcp.@dnsmasq[0].noresolv
    uci -q delete dhcp.@dnsmasq[0].cachesize
    uci add_list dhcp.@dnsmasq[0].server="$TARGET"
    if [ "$TARGET" = "8.8.8.8" ]; then
        uci -q add_list dhcp.@dnsmasq[0].server="1.1.1.1"
    fi
    uci commit dhcp
fi

# --------------------------------------------------- nft / файлы / cron ---

say "удаление nft-таблиц podkop/sing-box"
if command -v nft >/dev/null 2>&1; then
    for t in $(nft list tables 2>/dev/null | grep -iE 'podkop|sing-?box'); do
        run nft delete table "$t"
    done
fi

say "удаление файлов podkop"
run rm -f /etc/config/podkop
run rm -rf /usr/share/podkop /tmp/sing-box /etc/init.d/podkop

if [ "$LX" = "1" ]; then
    # lx стоит: каталог /etc/sing-box - рабочее место общего режима, но
    # подкоповый config.json бит без своих rule-set'ов - проверяю и чиню
    if [ -f /etc/sing-box/config.json ] && ! /usr/bin/sing-box check -c /etc/sing-box/config.json >/dev/null 2>&1; then
        say "чужой config.json невалиден без подкопа - заменяю на минимальную базу (clash_api 2291)"
        run mv /etc/sing-box/config.json "$BK/config.json.podkop-backup"
        cat > /etc/sing-box/config.json <<'EOF'
{
  "log": { "level": "warn", "timestamp": false },
  "experimental": {
    "clash_api": { "external_controller": "127.0.0.1:2291" },
    "cache_file": { "enabled": true, "path": "/etc/sing-box/cache.db" }
  }
}
EOF
    fi
else
    run rm -rf /etc/sing-box /etc/config/sing-box
fi

say "чистка cron-строк podkop"
if [ -f /etc/crontabs/root ] && grep -qiE 'podkop|getdomains' /etc/crontabs/root; then
    if [ "$DRY" = "1" ]; then
        echo "    [dry-run] grep -viE 'podkop|getdomains' /etc/crontabs/root"
    else
        grep -viE 'podkop|getdomains' /etc/crontabs/root > /etc/crontabs/root.tmp
        mv /etc/crontabs/root.tmp /etc/crontabs/root
        /etc/init.d/cron restart
    fi
fi

# --------------------------------------------------------- рестарт/чек ----

say "рестарт dnsmasq + синхронизация времени"
run /etc/init.d/dnsmasq restart
run ntpd -q -p ptbtime1.ptb.de 2>/dev/null

if [ -f /etc/init.d/mawg ]; then
    say "рестарт mawg"
    run /etc/init.d/mawg restart
fi

say "проверка DNS через роутер"
if [ "$DRY" = "1" ]; then
    echo "    [dry-run] nslookup пропущен"
elif nslookup google.com 127.0.0.1 >/dev/null 2>&1; then
    echo "    DNS роутера работает"
else
    warn "DNS через роутер НЕ отвечает - проверь: uci show dhcp.@dnsmasq[0]; logread | tail"
    warn "бэкап dhcp: $BK/dhcp.export"
fi

say "готово. остальное о podkop: $BK (бэкапы). "
[ "$LX" = "1" ] && echo "    помни: обновление sing-box делай через панель mawg, не opkg."
exit 0
