package tests

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// uninstallGlue - моки окружения uninstall.sh: uci с состоянием в файле,
// killall не трогает процессы хоста; apk/opkg - общий packageCommands.
const uninstallGlue = `
uci() {
    echo "uci $*" >> "$PKG_LOG"
    if [ "$1" = -q ]; then shift; fi
    case "$1" in
        show) grep -F "$2" "$UCI_STATE" 2>/dev/null;;
        delete) grep -vF "$2" "$UCI_STATE" > "$UCI_STATE.tmp" && mv "$UCI_STATE.tmp" "$UCI_STATE";;
        add_list) echo "${2%%=*}='${2#*=}'" >> "$UCI_STATE";;
    esac
}
killall() { echo "killall $*" >> "$PKG_LOG"; }
`

// runUninstall - прогон uninstall.sh целиком с временными путями вместо
// реальных (замены по литералам скрипта). pre - что создать заранее.
func runUninstall(t *testing.T, args string, mockApk int, created []string, state []string) (string, string, map[string]string) {
	t.Helper()
	source, err := os.ReadFile("../uninstall.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(source)
	script = strings.ReplaceAll(script, "\r\n", "\n")
	dir := t.TempDir()
	log := filepath.ToSlash(filepath.Join(dir, "calls.log"))

	paths := map[string]string{
		"/etc/openwrt_release":    filepath.ToSlash(filepath.Join(dir, "openwrt-release")),
		"/opt/etc":                filepath.ToSlash(filepath.Join(dir, "entware", "etc")),
		"/opt/bin/opkg":           filepath.ToSlash(filepath.Join(dir, "entware", "bin", "opkg")),
		"/usr/bin/mawg":           filepath.ToSlash(filepath.Join(dir, "bin", "mawg")),
		"/etc/init.d/mawg":        filepath.ToSlash(filepath.Join(dir, "init", "mawg")),
		"/etc/mawg":               filepath.ToSlash(filepath.Join(dir, "data")),
		"/var/log/mawg.log":       filepath.ToSlash(filepath.Join(dir, "mawg.log")),
		"/usr/sbin/iptables":      filepath.ToSlash(filepath.Join(dir, "ipt")),
		"/etc/init.d/magitrickle": filepath.ToSlash(filepath.Join(dir, "init", "magitrickle")),
		"/etc/magitrickle":        filepath.ToSlash(filepath.Join(dir, "magitrickle")),
		"/etc/sing-box-lx":        filepath.ToSlash(filepath.Join(dir, "sing-box-lx")),
		"/etc/mihomo-mawg":        filepath.ToSlash(filepath.Join(dir, "mihomo-mawg")),
		"/usr/bin/sing-box":       filepath.ToSlash(filepath.Join(dir, "bin", "sing-box")),
		"/usr/bin/mihomo":         filepath.ToSlash(filepath.Join(dir, "bin", "mihomo")),
		"/etc/init.d/dnsmasq":     filepath.ToSlash(filepath.Join(dir, "init", "dnsmasq")),
	}
	for from, to := range paths {
		script = strings.ReplaceAll(script, from, to)
	}

	// /etc/openwrt_release существует -> ветка openwrt
	if err := os.WriteFile(paths["/etc/openwrt_release"], []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// данные mawg: для keep-data должны существовать, для дефолта - удаляться
	if err := os.MkdirAll(paths["/etc/mawg"], 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths["/etc/mawg"], "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range created {
		pp := paths[p]
		if err := os.MkdirAll(filepath.Dir(pp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pp, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// каталоги-маркеры и MT-состояние
	if err := os.MkdirAll(paths["/etc/sing-box-lx"], 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(paths["/etc/magitrickle"], "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths["/etc/magitrickle"], "state", "config.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// iptables-мок: исполняемый файл, пишет вызовы в лог, -S отдаёт цепочки MT_*
	if err := os.WriteFile(paths["/usr/sbin/iptables"], []byte(
		"#!/bin/sh\n"+
			"echo \"iptables $*\" >> \"$PKG_LOG\"\n"+
			"case \"$*\" in\n"+
			"  *\"-t nat -S\") printf -- '-N MT_DNSOR\\n-A PREROUTING -j MT_DNSOR\\n-A MT_DNSOR -p udp -j DNAT --to-destination :3553\\n';;\n"+
			"  *\"-t mangle -S\") printf -- '-N MT_fd1e1ebe\\n-A PREROUTING -j MT_fd1e1ebe\\n';;\n"+
			"esac\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// uci-состояние
	statePath := filepath.ToSlash(filepath.Join(dir, "uci.state"))
	if err := os.WriteFile(statePath, []byte(strings.Join(state, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	full := "set -- " + args + "\n"
	full += "UCI_STATE=" + statePath + "\n" + uninstallGlue + packageCommands + "\n" + script
	output, _ := runShell(t, full,
		"MOCK_APK=" + strconv.Itoa(mockApk), "MOCK_OPKG=1", "MOCK_NATIVE_OPKG=0",
		"MOCK_INSTALLED=wireguard-tools amneziawg-tools magitrickle",
		"PKG_LOG="+log, "MOCK_ARCH=mips_24kc")
	calls, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return output, string(calls), paths
}

func TestUninstallOpenWrtOpkg24(t *testing.T) {
	// OpenWrt 24.10 (opkg): mawg + MT + движки + dnsmasq в режиме MT
	output, calls, paths := runUninstall(t, "", 0, []string{
		"/usr/bin/mawg", "/etc/init.d/mawg", "/var/log/mawg.log",
		"/usr/bin/sing-box", "/usr/bin/mihomo", "/etc/init.d/magitrickle",
	}, []string{
		"dhcp.@dnsmasq[0].server='127.0.0.1#3553'",
		"dhcp.@dnsmasq[0].mawg_server_bak='8.8.8.8'",
		"network.proton.proto='amneziawg'",
	})
	if runErr := recoverErr(output, calls); runErr != "" {
		t.Fatalf("uninstall упал: %s\n%s", runErr, output)
	}
	for _, p := range []string{paths["/usr/bin/mawg"], paths["/etc/init.d/mawg"], paths["/var/log/mawg.log"],
		paths["/usr/bin/sing-box"], paths["/etc/sing-box-lx"], paths["/etc/magitrickle"], paths["/etc/init.d/magitrickle"]} {
		if _, statErr := os.Stat(p); !os.IsNotExist(statErr) {
			t.Fatalf("не удалено: %s", p)
		}
	}
	// mihomo без маркера /etc/mihomo-mawg - не наша постановка, не тронут
	if _, statErr := os.Stat(paths["/usr/bin/mihomo"]); statErr != nil {
		t.Fatalf("чужой mihomo не должен удаляться: %v", statErr)
	}
	// opkg (24.x), apk не зовётся
	if !strings.Contains(calls, "opkg remove magitrickle") {
		t.Fatalf("MT не снят opkg-ом: %s", calls)
	}
	if strings.Contains(calls, "apk del") {
		t.Fatalf("apk не должен зваться на 24.x: %s", calls)
	}
	// dnsmasq: server=MT снят, bak возвращён
	if !strings.Contains(calls, "add_list dhcp.@dnsmasq[0].server=8.8.8.8") {
		t.Fatalf("bak не восстановлен: %s", calls)
	}
	// MT-цепочки промыты: jump -D, -F/-X
	for _, want := range []string{
		"iptables -t nat -D PREROUTING -j MT_DNSOR",
		"iptables -t nat -F MT_DNSOR",
		"iptables -t nat -X MT_DNSOR",
		"iptables -t mangle -X MT_fd1e1ebe",
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("нет вызова %q:\n%s", want, calls)
		}
	}
	if !strings.Contains(output, "остались uci-секции") {
		t.Fatalf("hint про пулы не показан: %s", output)
	}
	if !strings.Contains(output, "удалены") {
		t.Fatalf("данные должны удаляться по умолчанию: %s", output)
	}
}

func TestUninstallOpenWrtApk25(t *testing.T) {
	// OpenWrt 25 (apk): MT снимается apk del
	_, calls, _ := runUninstall(t, "", 1, []string{"/usr/bin/mawg", "/etc/init.d/mawg"}, []string{
		"dhcp.@dnsmasq[0].server='127.0.0.1#3553'",
	})
	if !strings.Contains(calls, "apk del magitrickle") {
		t.Fatalf("на 25.x MT должен сниматься apk del: %s", calls)
	}
	if strings.Contains(calls, "opkg remove magitrickle") {
		t.Fatalf("opkg не должен зваться на 25.x: %s", calls)
	}
}

func TestUninstallKeepData(t *testing.T) {
	// --keep-data: пулы и настройки панели сохраняются
	output, _, paths := runUninstall(t, "--keep-data", 0, []string{
		"/usr/bin/mawg", "/etc/init.d/mawg",
	}, []string{
		"dhcp.@dnsmasq[0].server='127.0.0.1#3553'",
	})
	if _, statErr := os.Stat(paths["/etc/mawg"]); statErr != nil {
		t.Fatalf("данные должны сохраняться: %v", statErr)
	}
	if !strings.Contains(output, "сохранены") {
		t.Fatalf("нет сообщения о сохранении: %s", output)
	}
}

func TestUninstallFreshRouter(t *testing.T) {
	// mawg никогда не ставился, MT есть: скрипт должен отработать
	// без падений и снять MT (гладкий путь «не понравилось»)
	output, calls, _ := runUninstall(t, "", 0, []string{}, []string{
		"dhcp.@dnsmasq[0].mawg_server_bak='8.8.8.8'",
	})
	if strings.Contains(output, "ОШИБКА") {
		t.Fatalf("свежий роутер не должен давать ошибку: %s", output)
	}
	if !strings.Contains(calls, "opkg remove magitrickle") {
		t.Fatalf("MT не снят: %s", calls)
	}
	if !strings.Contains(output, "готово") {
		t.Fatalf("нет финала: %s", output)
	}
}

// recoverErr - скрипт завершается по set -u/ошибкам неявно; ловим финал
func recoverErr(output, calls string) string {
	if strings.Contains(output, "готово") {
		return ""
	}
	return "нет финала «готово»; хвост лога: " + tail(calls)
}

func tail(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	return strings.Join(lines, "\n")
}
