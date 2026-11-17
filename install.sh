#!/bin/sh
# mawg - Multi-AWG Changer: установщик и обновлялка
# использование: sh install.sh [-u|--update] [-r|--remove] [--purge]
#   [--with-magitrickle] [--without-magitrickle] [--with-awg3] [--port N]
# по умолчанию: определить платформу, проверить зависимости (спросить при
# необходимости), скачать бинарник последнего релиза, поставить сервис.
# -r/--remove: остановить и снять mawg (бинарь, сервис, лог); вернуть
#   dnsmasq с MT:3553; движки и MagiTrickle - спросит (yes в флагах
#   --with/--without-magitrickle - без вопросов). --purge ещё и данные
#   стирает. Пулы удаляйте в ПАНЕЛИ до uninstall - интерфейсы роутера
#   скрипт не сносит (имена могут совпадать с вашими собственными).
# --port N - порт веб-панели (по умолчанию 8090; занятый порт установщик
# заметит и предложит другой). MAWG_DEBUG=1 - печатать тайминги шагов.
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
die() {
    echo "ОШИБКА: $*" >&2
    rm -rf "$TMP"
    if [ "${SERVICE_STOPPED:-}" = 1 ]; then
        echo "восстанавливаю сервис прежней версии" >&2
        svc_start 2>/dev/null
    fi
    exit 1
}
mark() {
    [ "$DEBUG" = 1 ] || return 0
    now=$(date +%s)
    echo "[debug] $* : $((now - STEP_TS))с (всего $((now - START_TS))с)" >&2
    STEP_TS=$now
}

PENDING=""
for arg in "$@"; do
    if [ "$PENDING" = "port" ]; then
        PORT_ARG="$arg"
        PENDING=""
        continue
    fi
    case "$arg" in
        -u|--update) MODE="update";;
        -r|--remove) MODE="remove";;
        --purge) MODE="purge";;
        --with-magitrickle) WANT_MT="yes";;
        --without-magitrickle) WANT_MT="no";;
        --with-awg3) WANT_AWG3="yes";;
        --port) PENDING="port";;
        -h|--help) sed -n '2,13p' "$0"; exit 0;;
        *) die "неизвестный аргумент $arg (см. --help)";;
    esac
done
[ -n "$PENDING" ] && die "--port указан без значения"
if [ -n "${PORT_ARG:-}" ]; then
    case "$PORT_ARG" in
        ''|*[!0-9]*) die "порт должен быть числом 1-65535";;
    esac
    [ "$PORT_ARG" -ge 1 ] && [ "$PORT_ARG" -le 65535 ] || die "порт 1-65535, получено $PORT_ARG"
    # сервисные порты MagiTrickle - панели их отдавать нельзя (слово владельца)
    [ "$PORT_ARG" = "8080" ] && die "8080 - веб-интерфейс/API MagiTrickle, выберите другой порт"
    [ "$PORT_ARG" = "3553" ] && die "3553 - dns-proxy MagiTrickle, выберите другой порт"
fi

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
    elif command -v uclient-fetch >/dev/null 2>&1; then
        echo uclient-fetch
    fi
}

do_fetch() {
    if [ "$FETCHER" = curl ]; then
        curl -fsSL "$1" -o "$2"
    elif [ "$FETCHER" = uclient-fetch ]; then
        uclient-fetch -qO "$2" "$1"
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

# restore_dnsmasq - вернуть dnsmasq с MT:3553 на прежние серверы ДО сноса
# MagiTrickle: панель в режиме «через MT» прячет прежний список в
# mawg_server_bak (dnsmgr.SetDnsmasqTarget); без возврата LAN остаётся с
# мёртвым DNS - резолверить некому (кейс «поставил и не понравилось»).
# Пустой bak = прежнего списка не было (дефолт WAN) - просто убираем MT.
restore_dnsmasq() {
    [ "$PLATFORM" = openwrt ] || return 0
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

# remove_engines - движковые бинари (sing-box-lx, mihomo) ставились mawg'ом:
# маркер-каталог из lxinstall/MihomoInstall - признак «поставлено mawg», без
# него бинарь не трогаем (вдруг пользователь ставил свой sing-box в тот же путь)
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

# flush_mt_chains - снять jump-правила в родительские цепочки и вычистить
# MT_* (remap MT_DNSOR + группы правил): stop MagiTrickle чистит сам,
# это страховка на kill -9/сбой; без промывки DNAT клиентов на :3553
# остаётся висеть на мёртвый порт
flush_mt_chains() {
    if [ "$PLATFORM" = openwrt ]; then IPT=/usr/sbin/iptables; else IPT=/opt/sbin/iptables; fi
    [ -x "$IPT" ] || IPT=$(command -v iptables 2>/dev/null) || return 0
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

# remove_magitrickle - «не понравилось ни то ни другое» (вопрос владельца
# 17.11): mawg и MT ставятся одной командой, снос должен быть одним ходом.
# WANT_MT: yes - удалить без вопросов, ask - спросить (неинтерактивный
# ask = нет), no - не трогать (MT могли поставить сами и отдельно)
remove_magitrickle() {
    [ "$WANT_MT" = no ] && return 0
    if [ "$WANT_MT" != yes ]; then
        ask "удалить MagiTrickle тоже (его ставил этот установщик)" || return 0
    fi
    if [ "$PLATFORM" = openwrt ]; then
        /etc/init.d/magitrickle stop >/dev/null 2>&1
    else
        /opt/etc/init.d/S99magitrickle stop >/dev/null 2>&1
    fi
    killall magitrickled 2>/dev/null
    need_pkg
    if pkg_installed magitrickle; then
        say "удаление пакета magitrickle"
        case "$PKG_MANAGER" in
            apk) apk del magitrickle >/dev/null 2>&1;;
            *) "$PKG_MANAGER" remove magitrickle >/dev/null 2>&1;;
        esac
    fi
    [ "$PLATFORM" = keenetic ] && rm -f /opt/etc/init.d/S99magitrickle
    flush_mt_chains
    # opkg/apk не сносит изменённые каталоги состояния (живой кейс 17.11
    # на 0.62: /etc/magitrickle/state остался после remove пакета)
    if [ "$PLATFORM" = openwrt ]; then rm -rf /etc/magitrickle; else rm -rf /opt/etc/magitrickle; fi
    say "MagiTrickle удалён (сервис, пакет, iptables-цепочки MT_*)"
}

# pool_cleanup_hint - интерфейсы пулов не сносим автоматически: имена
# uci-секций могут совпадать с СОБСТВЕННЫМИ wireguard-интерфейсами
# пользователя. Правильный порядок - удалить пулы в панели ДО uninstall.
pool_cleanup_hint() {
    if [ "$PLATFORM" = openwrt ]; then
        secs=$(uci show network 2>/dev/null | grep -E "\.proto='(wireguard|amneziawg)'" | sed -E "s/^network\.([^.]+)\..*/\1/" | tr '\n' ' ')
        if [ -n "$secs" ]; then
            warn "остались uci-секции туннелей: $secs - если это пулы mawg (а не ваши собственные), удалите: uci delete network.<имя> для каждого, затем uci commit network && /etc/init.d/network reload"
        fi
    else
        warn "слоты ndm (WireguardN) от пулов не удаляются автоматически - удалите пулы в панели ДО uninstall или снесите слоты в веб-интерфейсе Keenetic"
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
    # фактический порт панели: из сохранённых настроек, не из догадок
    web_port=$(grep -o '"webPort"[,: ]*[0-9]*' "$DATA/config.json" 2>/dev/null | grep -o '[0-9]*$' | head -1)
    [ -n "$web_port" ] || web_port=8090
    echo
    echo "==============================="
    echo " mawg установлен"
    echo " платформа:   $PLATFORM ($ARCH)"
    echo " бинарник:    $BIN"
    echo " данные:      $DATA"
    echo " лог:         $LOG"
    echo " веб:         http://${ip:-<ip-роутера>}:$web_port"
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
    # дети svc_stop: движковые пулы живут процессами sing-box/mihomo,
    # их mawg порождает сам - без глушения держат туннели дальше
    killall sing-box 2>/dev/null
    killall mihomo 2>/dev/null
    # dnsmasq вернуть с MT:3553 ДО сноса MT, иначе LAN без DNS
    restore_dnsmasq
    rm -f "$BIN" "$INIT" "$LOG"
    remove_engines
    remove_magitrickle
    if [ "$MODE" = purge ]; then
        rm -rf "$DATA"
        say "данные $DATA удалены"
    else
        say "данные $DATA сохранены (для полного удаления: --purge)"
    fi
    pool_cleanup_hint
    say "готово"
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

    # форк vs апстрим: у форка в версии ревизия с тире (0.8.2-22), апстрим
    # ставился без неё с bin.magitrickle.dev (кейс пользователя 15.11:
    # инсталлер ставил апстрим, форк приходилось накатывать отдельно)
    mt_installed=0
    if [ "$PKG_MANAGER" = opkg ]; then
        # форк vs апстрим: у форка ревизия с тире (0.8.2-22), апстрим - без
        mt_ver="$(opkg info magitrickle 2>/dev/null | awk '/^Version:/{print $2; exit}')"
        case "$mt_ver" in
            *-*) mt_installed=1; echo "   MagiTrickle форк ($mt_ver) уже установлен";;
            "")  mt_installed=0;;
            *)   mt_installed=0
                 echo "   найден апстрим-пакет magitrickle $mt_ver (без ревизий форка) - заменю на форк";;
        esac
    elif pkg_installed magitrickle; then
        mt_installed=1
        echo "   magitrickle уже установлен"
    fi
    if [ "$mt_installed" = 0 ]; then
        do_mt=no
        if [ "$WANT_MT" = yes ]; then
            do_mt=yes
        elif [ "$WANT_MT" = ask ]; then
            say "MagiTrickle не найден: маршрутизация по доменам работать не будет"
            if ask "установить MagiTrickle (релиз форка: зоны, лог-уровень)"; then
                do_mt=yes
            fi
        fi
        if [ "$do_mt" = yes ]; then
            say "установка MagiTrickle (форк)"
            # пакет берётся ФАЙЛОМ из релиза форка на GitHub (как mtinstall
            # в панели): репозиторий апстрима bin.magitrickle.dev остался
            # в прошлом - без rev-фич
            mt_target=""
            mt_ext=".ipk"
            if [ "$PKG_MANAGER" = apk ]; then
                mt_ext=".apk"
                mt_target="$(head -n 1 /etc/apk/arch 2>/dev/null)"
                [ -n "$mt_target" ] || mt_target="$(apk --print-arch 2>/dev/null)"
            elif command -v opkg >/dev/null 2>&1; then
                mt_target="$(opkg print-architecture 2>/dev/null | awk '$1=="arch" && $2!="all" && $2!="noarch" {print $2; exit}')"
            fi
            mt_prefix=openwrt
            [ "$PLATFORM" = keenetic ] && mt_prefix=entware
            mt_url=""
            if [ -n "$mt_target" ]; then
                mt_json="$TMP/mt-release.json"
                do_fetch "https://api.github.com/repos/MarkinAlexander/magitrickle/releases/latest" "$mt_json" || true
                if [ -s "$mt_json" ]; then
                    if [ "$PLATFORM" = keenetic ]; then
                        mt_url=$(grep -o "https://[^\"]*_${mt_prefix}_${mt_target}_kn${mt_ext}" "$mt_json" | head -n 1)
                        [ -n "$mt_url" ] || mt_url=$(grep -o "https://[^\"]*_${mt_prefix}_${mt_target}${mt_ext}" "$mt_json" | head -n 1)
                    else
                        mt_url=$(grep -o "https://[^\"]*_${mt_prefix}_${mt_target}${mt_ext}" "$mt_json" | head -n 1)
                        [ -n "$mt_url" ] || mt_url=$(grep -o "https://[^\"]*_${mt_prefix}_${mt_target}_generic${mt_ext}" "$mt_json" | head -n 1)
                    fi
                fi
            fi
            if [ -z "$mt_url" ]; then
                warn "не удалось определить пакет MagiTrickle для ${mt_prefix}/${mt_target:-?} - поставьте из панели mawg (она ставит релиз форка)"
            else
                say "релиз форка: $(basename "$mt_url")"
                mt_file="$TMP/mt-pkg"
                # валидация magic "!<arch>" (ar-архив ipk): github иногда
                # отдаёт на скачивание HTML-интерстишл с 200 - opkg на нём
                # падает непонятным "Cannot install package" (живой кейс 17.11
                # на 0.62); один ретрай, потом честная ошибка
                mt_ok=""
                for mt_try in 1 2; do
                    do_fetch "$mt_url" "$mt_file" && [ -s "$mt_file" ] || { warn "не удалось скачать пакет MagiTrickle (попытка $mt_try)"; continue; }
                    if head -c 8 "$mt_file" | grep -q "!<arch>"; then mt_ok=1; break; fi
                    warn "скачался не пакет (попытка $mt_try) - повтор"
                    sleep 2
                done
                if [ -n "$mt_ok" ]; then
                    # стоп до замены: файл живого бинаря не заменяется (ETXTBSY)
                    if [ "$PLATFORM" = keenetic ]; then
                        /opt/etc/init.d/S99magitrickle stop >/dev/null 2>&1
                        pkg_install socat >/dev/null 2>&1 || true
                    else
                        /etc/init.d/magitrickle stop >/dev/null 2>&1
                    fi
                    if [ "$PKG_MANAGER" = apk ]; then
                        apk add --allow-untrusted "$mt_file" >/dev/null 2>&1 \
                            || warn "magitrickle не установился"
                    else
                        mt_out="$(opkg install "$mt_file" 2>&1)" \
                            || warn "magitrickle не установился: $(echo "$mt_out" | tail -n 1)"
                        case "$mt_out" in
                            *"up to date"*|*"Up to date"*)
                                opkg install --force-reinstall "$mt_file" >/dev/null 2>&1 || true;;
                        esac
                    fi
                    if [ "$PLATFORM" = keenetic ]; then
                        chmod +x /opt/etc/init.d/S99magitrickle 2>/dev/null
                        /opt/etc/init.d/S99magitrickle start >/dev/null 2>&1 \
                            || warn "служба MagiTrickle не стартовала"
                    else
                        /etc/init.d/magitrickle enable >/dev/null 2>&1
                        /etc/init.d/magitrickle start >/dev/null 2>&1 \
                            || warn "служба MagiTrickle не стартовала"
                    fi
                    rm -f "$mt_file"
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
SERVICE_STOPPED=1
mark "остановка сервиса"
mv "$TMP/mawg" "$BIN" || die "не удалось записать $BIN"
chmod +x "$BIN"
mkdir -p "$DATA"

if [ "$PLATFORM" = openwrt ]; then
    write_init_openwrt
else
    write_init_keenetic
fi

# порт панели: явный --port, иначе на свежей установке заметить занятый
# 8090 и спросить (кейс пользователя: порт уже занят другой панелью)
want_port=""
if [ -n "${PORT_ARG:-}" ]; then
    want_port="$PORT_ARG"
elif [ "$MODE" = install ] && [ ! -f "$DATA/config.json" ]; then
    if netstat -tln 2>/dev/null | grep -q ':8090 '; then
        warn "порт 8090 уже занят другим процессом - панель на нём не поднимется"
        if [ -t 0 ]; then
            printf "порт панели (1-65535, Enter - всё равно 8090): "
            read -r want_port
            case "$want_port" in
                '') want_port="";;
                *[!0-9]*) die "порт должен быть числом 1-65535";;
            esac
            if [ -n "$want_port" ]; then
                [ "$want_port" -ge 1 ] && [ "$want_port" -le 65535 ] || die "порт 1-65535"
            fi
        else
            warn "терминала нет: переустановите с --port N или выполните mawg port <N> после установки"
        fi
    fi
fi
if [ -n "$want_port" ] && [ "$want_port" != "8090" ]; then
    "$BIN" port "$want_port" >/dev/null || die "не удалось задать порт $want_port"
    say "порт панели: $want_port"
fi

svc_start
SERVICE_STOPPED=0
sleep 1
mark "запуск сервиса"
if [ "$PLATFORM" = openwrt ]; then
    pgrep -f /usr/bin/mawg >/dev/null 2>&1 || warn "mawg не запустился, смотрите $LOG"
else
    pgrep -f /opt/bin/mawg >/dev/null 2>&1 || warn "mawg не запустился, смотрите $LOG"
fi

rm -rf "$TMP"
report
