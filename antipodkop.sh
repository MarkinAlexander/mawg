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
if [ "$DRY" = "1" ]; then
    say "dry-run: изменений и бэкапа не будет"
else
    if ! mkdir -p "$BK"; then
        echo "ОШИБКА: не создать каталог бэкапа $BK (флеш полна или read-only) - НИЧЕГО не удаляю"; exit 1
    fi
    BKOK=0
    uci export dhcp > "$BK/dhcp.export" 2>/dev/null && BKOK=1
    cp -a /etc/config "$BK/etc-config" 2>/dev/null && BKOK=1
    # покрываем и то, что скрипт удаляет ниже
    [ -d /etc/sing-box ] && cp -a /etc/sing-box "$BK/etc-sing-box" 2>/dev/null && BKOK=1
    [ -f /etc/crontabs/root ] && cp -a /etc/crontabs/root "$BK/crontabs-root" 2>/dev/null
    cp -a /usr/lib/opkg/status "$BK/opkg-status" 2>/dev/null
    cp -a /usr/lib/opkg/info/sing-box.* "$BK/" 2>/dev/null || true
    if [ "$BKOK" != "1" ]; then
        echo "ОШИБКА: ни один бэкап не сохранился - НИЧЕГО не удаляю"; exit 1
    fi
    say "бэкап: $BK"
fi

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
    uci -q delete 'dhcp.@dnsmasq[0].server'
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
    # вывод "table <family> <name>" - читаем построчно, иначе разрыв по словам
    nft list tables 2>/dev/null | grep -iE 'podkop|sing-?box' | while read -r _ fam name; do
        [ -n "$name" ] || continue
        run nft delete table "$fam" "$name"
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
        if [ "$DRY" = "1" ]; then
            echo "    [dry-run] mv /etc/sing-box/config.json -> $BK/config.json.podkop-backup + запись минимальной базы"
        elif mv /etc/sing-box/config.json "$BK/config.json.podkop-backup"; then
            cat > /etc/sing-box/config.json.new <<'EOF'
{
  "log": { "level": "warn", "timestamp": false },
  "experimental": {
    "clash_api": { "external_controller": "127.0.0.1:2291" },
    "cache_file": { "enabled": true, "path": "/etc/sing-box/cache.db" }
  }
}
EOF
            mv /etc/sing-box/config.json.new /etc/sing-box/config.json
        else
            warn "старый config.json не сохранился - базу НЕ записываю"
        fi
    fi
else
    run rm -rf /etc/sing-box /etc/config/sing-box
fi

say "чистка cron-строк podkop"
if [ "$WITH_GETDOMAINS" = "1" ]; then FILT='podkop|getdomains'; else FILT='podkop'; fi
if [ -f /etc/crontabs/root ] && grep -qiE "$FILT" /etc/crontabs/root; then
    if [ "$DRY" = "1" ]; then
        echo "    [dry-run] убрать из /etc/crontabs/root строки по фильтру: $FILT"
    else
        grep -viE "$FILT" /etc/crontabs/root > /etc/crontabs/root.tmp
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
