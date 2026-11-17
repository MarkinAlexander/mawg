package tests

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const installedPackages = "wireguard-tools amneziawg-tools magitrickle"

const packageCommands = `
command() {
    case "$*" in
        "-v apk") test "$MOCK_APK" = 1;;
        "-v opkg") test "$MOCK_OPKG" = 1;;
        *) builtin command "$@";;
    esac
}
test() {
    if [ "$#" = 2 ] && [ "$1" = -x ] && [ "$2" = /bin/opkg ]; then
        [ "$MOCK_NATIVE_OPKG" = 1 ]
    else
        builtin test "$@"
    fi
}
mock_package_command() {
    manager=$1
    shift
    printf '%s %s\n' "$manager" "$*" >> "$PKG_LOG"
    case "$manager:$1" in
        *:update) return "$MOCK_UPDATE_STATUS";;
        *:add|*:install) return 0;;
        apk:info)
            case " $MOCK_INSTALLED " in *" $3 "*) echo "$3"; return 0;; esac
            return 1;;
        opkg:list-installed)
            for package in $MOCK_INSTALLED; do echo "$package - 1.0-r1"; done;;
        opkg:info)
            [ "$2" = magitrickle ] && { echo "Version: $MOCK_MT_VERSION"; return 0; }
            return 1;;
        apk:--print-arch) echo "$MOCK_ARCH";;
        opkg:print-architecture) echo "arch $MOCK_ARCH 10";;
        *) return 1;;
    esac
}
apk() { mock_package_command apk "$@"; }
opkg() { mock_package_command opkg "$@"; }
`

type installerFixture struct {
	apk, opkg, nativeOpkg bool
	installed             string
	updateStatus          int
	// fetchBody - тело ответа do_fetch (релиз форка); пусто - старый мок
	// add_repo-сценария. mtVersion - что отвечает opkg info magitrickle.
	fetchBody, mtVersion string
	wantMagiTrickle      bool
}

type installerResult struct {
	output, calls string
	err           error
}

func installerSource(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func before(t *testing.T, source, marker string) string {
	t.Helper()
	prefix, _, ok := strings.Cut(source, marker)
	if !ok {
		t.Fatalf("installer marker missing: %q", marker)
	}
	return prefix
}

func after(t *testing.T, source, marker string) string {
	t.Helper()
	_, suffix, ok := strings.Cut(source, marker)
	if !ok {
		t.Fatalf("installer marker missing: %q", marker)
	}
	return suffix
}

func shellQuote(path string) string {
	return strconv.Quote(filepath.ToSlash(path))
}

func runShell(t *testing.T, script string, env ...string) (string, error) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("installer integration tests require bash")
	}
	cmd := exec.Command(bash)
	cmd.Stdin = strings.NewReader(script)
	cmd.Env = append(os.Environ(), env...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func (f installerFixture) run(t *testing.T, suffix string) installerResult {
	t.Helper()
	source := installerSource(t)
	definitions := before(t, source, "\nPLATFORM=$(detect_platform)")
	definitions = strings.ReplaceAll(definitions, "echo /bin/opkg", "echo opkg")
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	init := filepath.Join(dir, "mt-init")
	if err := os.WriteFile(init, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	suffix = strings.ReplaceAll(suffix, "/etc/init.d/magitrickle", shellQuote(init))
	wantMT := "no"
	if f.wantMagiTrickle {
		wantMT = "yes"
	}
	script := definitions + packageCommands + "\nPLATFORM=openwrt\n"
	script += "TMP=" + shellQuote(filepath.Join(dir, "tmp")) + "\nmkdir -p \"$TMP\"\n"
	script += "WANT_MT=" + wantMT + "\n"
	script += "do_fetch() { case \"$2\" in *mt-pkg*) printf '!<arch>\\nfake-ipk' > \"$2\";; *) if [ -n \"$MOCK_FETCH_BODY\" ]; then printf '%s' \"$MOCK_FETCH_BODY\" > \"$2\"; else printf '#!/bin/sh\\nexit %s\\n' \"$MOCK_REPO_STATUS\" > \"$2\"; fi;; esac; }\n"
	script += "PKG_MANAGER=$(detect_pkg_manager) || exit 1\n" + suffix
	output, runErr := runShell(t, script,
		fmt.Sprintf("MOCK_APK=%d", boolInt(f.apk)), fmt.Sprintf("MOCK_OPKG=%d", boolInt(f.opkg)),
		fmt.Sprintf("MOCK_NATIVE_OPKG=%d", boolInt(f.nativeOpkg)),
		"MOCK_INSTALLED="+f.installed, fmt.Sprintf("MOCK_UPDATE_STATUS=%d", f.updateStatus),
		"MOCK_FETCH_BODY="+f.fetchBody, "MOCK_MT_VERSION="+f.mtVersion, "MOCK_REPO_STATUS=0", "MOCK_ARCH=mips_24kc", "PKG_LOG="+filepath.ToSlash(log))
	calls, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return installerResult{output: output, calls: string(calls), err: runErr}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func dependencyChecks(t *testing.T) string {
	t.Helper()
	source := installerSource(t)
	marker := "if [ \"$MODE\" = install ]; then\n    pkg_update_ok=0"
	return marker + before(t,
		after(t, source, marker), "\nsay \"загрузка mawg-linux-$ARCH\"")
}

func TestInstallerPackageManagers(t *testing.T) {
	for _, manager := range []string{"apk", "opkg"} {
		for _, scenario := range []struct {
			name, installed, wantInstall string
			updateStatus                 int
			wantMT, wantFailure          bool
		}{
			{name: "installed", installed: installedPackages},
			{name: "missing-wireguard", installed: "amneziawg-tools magitrickle", wantInstall: "kmod-wireguard wireguard-tools luci-proto-wireguard"},
			{name: "update-failed-installed", installed: installedPackages, updateStatus: 1},
			{name: "update-failed-missing", updateStatus: 1, wantFailure: true},
			{name: "missing-awg-and-magitrickle", installed: "wireguard-tools", wantMT: true, wantInstall: "kmod-amneziawg amneziawg-tools"},
			{name: "mt-release-unmatched", installed: "wireguard-tools amneziawg-tools", wantMT: true},
			{name: "similar-package-name", installed: "wireguard-tools-extra amneziawg-tools magitrickle", wantInstall: "kmod-wireguard wireguard-tools luci-proto-wireguard"},
		} {
			t.Run(manager+"/"+scenario.name, func(t *testing.T) {
				fixture := installerFixture{apk: manager == "apk", opkg: manager == "opkg", installed: scenario.installed,
					updateStatus: scenario.updateStatus, wantMagiTrickle: scenario.wantMT}
				result := fixture.run(t, dependencyChecks(t))
				if (result.err != nil) != scenario.wantFailure {
					t.Fatalf("unexpected exit: %v\n%s", result.err, result.output)
				}
				verb := "install"
				other := "apk"
				if manager == "apk" {
					verb, other = "add", "opkg"
				}
				if strings.Contains(result.calls, other+" ") {
					t.Fatalf("wrong manager: %s", result.calls)
				}
				if scenario.wantInstall != "" && !strings.Contains(result.calls, manager+" "+verb+" "+scenario.wantInstall) {
					t.Fatalf("missing installation command: %s", result.calls)
				}
				if scenario.wantInstall == "" && strings.Contains(result.calls, manager+" "+verb+" ") {
					t.Fatalf("unexpected installation: %s", result.calls)
				}
				if scenario.wantMT && !strings.Contains(result.output, "не удалось определить пакет") {
					// тело do_fetch в моке - не JSON: релиз не матчится, честный warn
					t.Fatalf("missing mt-release warning: %s", result.output)
				}
				if scenario.name == "missing-awg-and-magitrickle" && !strings.Contains(result.calls, manager+" "+verb+" kmod-amneziawg amneziawg-tools") {
					t.Fatalf("missing AWG installation: %s", result.calls)
				}
			})
		}
	}
}

func TestInstallerManagerSelection(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		fixture installerFixture
		want    string
	}{
		{"native-opkg-with-apk", installerFixture{apk: true, opkg: true, nativeOpkg: true}, "opkg"},
		{"apk-with-nonnative-opkg", installerFixture{apk: true, opkg: true}, "apk"},
		{"only-apk", installerFixture{apk: true}, "apk"},
		{"only-opkg", installerFixture{opkg: true}, "opkg"},
		{"no-manager", installerFixture{}, ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			result := scenario.fixture.run(t, "printf '%s' \"$PKG_MANAGER\"")
			if scenario.want == "" {
				if result.err == nil || !strings.Contains(result.output, "не найден пакетный менеджер apk или opkg") {
					t.Fatalf("missing explicit failure: %v %s", result.err, result.output)
				}
			} else if result.err != nil || result.output != scenario.want {
				t.Fatalf("manager = %q, error = %v; want %q", result.output, result.err, scenario.want)
			}
		})
	}
	// MT теперь ставится ФАЙЛОМ из релиза форка: JSON отдаёт ассет под
	// таргет, opkg install файла (кейс пользователя 15.11: инсталлер
	// ставил апстрим с bin.magitrickle.dev)
	body := `{"assets":[{"name":"magitrickle_0.8.2-22_openwrt_mips_24kc.ipk","browser_download_url":"https://github.com/MarkinAlexander/magitrickle/releases/download/0.8.2-rev22/magitrickle_0.8.2-22_openwrt_mips_24kc.ipk"}]}`
	result := (installerFixture{apk: true, opkg: true, nativeOpkg: true, installed: "wireguard-tools amneziawg-tools", wantMagiTrickle: true, fetchBody: body}).run(t, dependencyChecks(t))
	if result.err != nil || strings.Contains(result.calls, "apk ") {
		t.Fatalf("native opkg fork-install mismatch: %v %s", result.err, result.calls)
	}
	if !strings.Contains(result.calls, "opkg install") || strings.Contains(result.calls, "opkg install magitrickle") {
		t.Fatalf("fork package must install from FILE, not by name: %s", result.calls)
	}
	if !strings.Contains(result.output, "релиз форка: magitrickle_0.8.2-22_openwrt_mips_24kc.ipk") {
		t.Fatalf("fork asset not selected: %s", result.output)
	}
	// апстрим-версия (без тире в Version) - заменяется на форк
	up := (installerFixture{apk: true, opkg: true, nativeOpkg: true, installed: "wireguard-tools amneziawg-tools", wantMagiTrickle: true, fetchBody: body, mtVersion: "0.8.2"}).run(t, dependencyChecks(t))
	if !strings.Contains(up.calls, "opkg install") || !strings.Contains(up.output, "заменю на форк") {
		t.Fatalf("upstream package must be replaced: %s %s", up.output, up.calls)
	}
	// форк уже стоит (ревизия с тире) - пропускается
	fork := (installerFixture{apk: true, opkg: true, nativeOpkg: true, installed: "wireguard-tools amneziawg-tools", wantMagiTrickle: true, fetchBody: body, mtVersion: "0.8.2-22"}).run(t, dependencyChecks(t))
	if strings.Contains(fork.calls, "opkg install") || !strings.Contains(fork.output, "форк (0.8.2-22) уже установлен") {
		t.Fatalf("fork must be skipped: %s %s", fork.output, fork.calls)
	}
}

func TestInstallerKeeneticKeepsEntware(t *testing.T) {
	script := before(t, installerSource(t), "\nPLATFORM=$(detect_platform)") + "\nPLATFORM=keenetic\ndetect_pkg_manager"
	output, err := runShell(t, script)
	if err != nil || strings.TrimSpace(output) != "/opt/bin/opkg" {
		t.Fatalf("manager = %q, error = %v", output, err)
	}
}

func TestInstallerFetchBootstrap(t *testing.T) {
	for _, manager := range []string{"apk", "opkg"} {
		t.Run(manager, func(t *testing.T) {
			verb := "install"
			if manager == "apk" {
				verb = "add"
			}
			script := fmt.Sprintf(`
command() {
    case "$*" in
        "-v curl") test "${MOCK_CURL:-0}" = 1;;
        "-v wget") return 1;;
        "-v apk") test "$MOCK_APK" = 1;;
        "-v opkg") test "$MOCK_OPKG" = 1;;
        *) builtin command "$@";;
    esac
}
%[1]s() {
    printf '%%s %%s\n' %[1]s "$*" >> "$PKG_LOG"
    case "$*" in *curl*) export MOCK_CURL=1;; esac
    return 0
}
ensure_fetcher
`, manager)
			result := (installerFixture{apk: manager == "apk", opkg: manager == "opkg"}).run(t, script)
			if result.err != nil || !strings.Contains(result.calls, manager+" "+verb+" curl") {
				t.Fatalf("bootstrap failure: %v\n%s\n%s", result.err, result.output, result.calls)
			}
		})
	}
}

func TestInstallerAPKMIPSEndianness(t *testing.T) {
	for arch, want := range map[string]string{"mips": "mips", "mips_24kc": "mips", "mipsel": "mipsle", "mipsel_24kc": "mipsle"} {
		t.Run(arch, func(t *testing.T) {
			result := (installerFixture{apk: true}).run(t, "MOCK_ARCH="+arch+"\nuname() { echo mips; }\ndetect_arch")
			if result.err != nil || strings.TrimSpace(result.output) != want {
				t.Fatalf("architecture = %q, error = %v; want %q", result.output, result.err, want)
			}
		})
	}
}

func TestInstallerUpdateModeSkipsPackageManager(t *testing.T) {
	source := installerSource(t)
	definitions := before(t, source, "\nPLATFORM=$(detect_platform)")
	download := before(t, after(t, source, "\nsay \"загрузка mawg-linux-$ARCH\""), "\nsize=$(wc -c")
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := definitions + packageCommands + "\nPLATFORM=openwrt\nMODE=update\nARCH=arm64\n"
	script += "TMP=" + shellQuote(filepath.Join(dir, "tmp")) + "\nmkdir -p \"$TMP\"\n"
	script += `
command() {
    case "$*" in
        "-v curl") return 1;;
        "-v wget") return 0;;
        *) builtin command "$@";;
    esac
}
wget() {
    printf 'wget %s\n' "$*" >> "$PKG_LOG"
    printf 'fake-mawg-binary' > "$2"
}
ensure_fetcher
` + download
	output, err := runShell(t, script, "PKG_LOG="+filepath.ToSlash(log), "MAWG_DEBUG=1")
	if err != nil {
		t.Fatalf("update через wget не прошёл: %v\n%s", err, output)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "apk") || strings.Contains(string(calls), "opkg") {
		t.Fatalf("update-режим не должен вызывать пакетный менеджер: %s", calls)
	}
	if !strings.Contains(string(calls), "wget") {
		t.Fatalf("скачивание должно идти wget-ом: %s", calls)
	}
	if !strings.Contains(output, "[debug]") {
		t.Fatalf("MAWG_DEBUG=1 должен печатать тайминги: %s", output)
	}
}

func TestInstallerUpdateModeUclientFetch(t *testing.T) {
	// OpenWrt с apk и без curl/wget: uclient-fetch качает сам,
	// пакетный менеджер в update-режиме не зовётся
	source := installerSource(t)
	definitions := before(t, source, "\nPLATFORM=$(detect_platform)")
	download := before(t, after(t, source, "\nsay \"загрузка mawg-linux-$ARCH\""), "\nsize=$(wc -c")
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := definitions + packageCommands + "\nPLATFORM=openwrt\nMODE=update\nARCH=arm64\n"
	script += "TMP=" + shellQuote(filepath.Join(dir, "tmp")) + "\nmkdir -p \"$TMP\"\n"
	script += `
command() {
    case "$*" in
        "-v curl") return 1;;
        "-v wget") return 1;;
        "-v uclient-fetch") return 0;;
        "-v opkg") return 1;;
        *) builtin command "$@";;
    esac
}
uclient-fetch() {
    printf 'uclient %s\n' "$*" >> "$PKG_LOG"
    printf 'fake-mawg-binary' > "$2"
}
ensure_fetcher
` + download
	output, err := runShell(t, script, "PKG_LOG="+filepath.ToSlash(log))
	if err != nil {
		t.Fatalf("update через uclient-fetch не прошёл: %v\n%s", err, output)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(calls), "apk") {
		t.Fatalf("update-режим не должен вызывать apk: %s", calls)
	}
	if !strings.Contains(string(calls), "uclient") {
		t.Fatalf("скачивание должно идти uclient-fetch-ом: %s", calls)
	}
}

func TestInstallerLocalBinary(t *testing.T) {
	source := installerSource(t)
	definitions := before(t, source, "\nPLATFORM=$(detect_platform)")
	download := before(t, after(t, source, "\nsay \"загрузка mawg-linux-$ARCH\""), "\nsay \"установка $BIN\"")
	dir := t.TempDir()
	binary := filepath.Join(dir, "local-mawg")
	data := bytes.Repeat([]byte("x"), 500001)
	if err := os.WriteFile(binary, data, 0o644); err != nil {
		t.Fatal(err)
	}
	script := definitions + "\nARCH=arm64\nTMP=" + shellQuote(filepath.Join(dir, "tmp")) + "\nmkdir -p \"$TMP\"\n"
	script += "do_fetch() { return 1; }\n" + download
	output, err := runShell(t, script, "MAWG_BINARY="+filepath.ToSlash(binary))
	if err != nil {
		t.Fatalf("local binary installation failed: %v\n%s", err, output)
	}
	copied, err := os.ReadFile(filepath.Join(dir, "tmp", "mawg"))
	if err != nil || !bytes.Equal(copied, data) {
		t.Fatalf("local binary not preserved: %v", err)
	}
}

// ---- remove/purge: «поставил и не понравилось ни то ни другое»
// (вопрос владельца 17.11) ----

// removeGlue - моки окружения remove-режима: killall не должен трогать
// процессы хоста, uci - с состоянием в файле, iptables - фиксированный
// набор MT-цепочек как на живом роутере
const removeGlue = `
uci() {
    echo "uci $*" >> "$PKG_LOG"
    if [ "$1" = -q ]; then shift; fi
    case "$1" in
        show) grep -F "$2" "$UCI_STATE" 2>/dev/null;;
        delete) grep -vF "$2" "$UCI_STATE" > "$UCI_STATE.tmp" && mv "$UCI_STATE.tmp" "$UCI_STATE";;
        add_list) echo "${2%%=*}='${2#*=}'" >> "$UCI_STATE";;
    esac
}
iptables() {
    echo "iptables $*" >> "$PKG_LOG"
    case "$*" in
        *"-t nat -S")
            printf -- '-N MT_DNSOR\n-N MT_a2d34556\n-A PREROUTING -j MT_DNSOR\n-A PREROUTING -j MT_a2d34556\n-A MT_DNSOR -p udp -j DNAT --to-destination :3553\n';;
        *"-t mangle -S")
            printf -- '-N MT_fd1e1ebe\n-A PREROUTING -j MT_fd1e1ebe\n';;
    esac
}
killall() { echo "killall $*" >> "$PKG_LOG"; }
`

// runRemove - suffix remove/purge-блока с временными путями вместо реальных
// (замены по литералам install.sh: BIN/INIT/DATA/LOG, движки, MT-init)
func runRemove(t *testing.T, mode, wantMT string, state []string) (installerResult, map[string]string) {
	t.Helper()
	source := installerSource(t)
	definitions := before(t, source, "\nPLATFORM=$(detect_platform)")
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")

	paths := map[string]string{
		"/usr/bin/mawg":     filepath.ToSlash(filepath.Join(dir, "bin", "mawg")),
		"/etc/init.d/mawg":  filepath.ToSlash(filepath.Join(dir, "init", "mawg")),
		"/etc/mawg":         filepath.ToSlash(filepath.Join(dir, "data")),
		"/var/log/mawg.log": filepath.ToSlash(filepath.Join(dir, "mawg.log")),
		"/usr/bin/sing-box": filepath.ToSlash(filepath.Join(dir, "bin", "sing-box")),
		"/usr/bin/mihomo":   filepath.ToSlash(filepath.Join(dir, "bin", "mihomo")),
		"/etc/sing-box-lx":  filepath.ToSlash(filepath.Join(dir, "sing-box-lx")),
		"/etc/mihomo-mawg":  filepath.ToSlash(filepath.Join(dir, "mihomo-mawg")),
		"/etc/magitrickle":  filepath.ToSlash(filepath.Join(dir, "magitrickle")),
	}
	suffix := after(t, source, "\nPLATFORM=$(detect_platform)")
	for from, to := range paths {
		suffix = strings.ReplaceAll(suffix, from, to)
	}
	suffix = strings.ReplaceAll(suffix, "/etc/init.d/magitrickle", shellQuote(filepath.ToSlash(filepath.Join(dir, "init", "magitrickle"))))

	for _, p := range []string{
		paths["/usr/bin/mawg"], paths["/etc/init.d/mawg"], paths["/var/log/mawg.log"],
		paths["/usr/bin/sing-box"], paths["/usr/bin/mihomo"],
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(paths["/etc/mawg"], 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths["/etc/mawg"], "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths["/etc/sing-box-lx"], 0o755); err != nil {
		t.Fatal(err)
	}
	// MT-состояние: opkg не сносит изменённые каталоги - зачистка на инсталлере
	if err := os.MkdirAll(filepath.Join(paths["/etc/magitrickle"], "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths["/etc/magitrickle"], "state", "config.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.ToSlash(filepath.Join(dir, "uci.state"))
	if err := os.WriteFile(statePath, []byte(strings.Join(state, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	script := definitions + packageCommands + removeGlue + "\nPLATFORM=openwrt\nMODE=" + mode + "\nWANT_MT=" + wantMT + "\n"
	script += "UCI_STATE=" + shellQuote(statePath) + "\n"
	script += "PKG_MANAGER=opkg\n" + suffix
	output, runErr := runShell(t, script,
		"MOCK_APK=0", "MOCK_OPKG=1", "MOCK_NATIVE_OPKG=0",
		"MOCK_INSTALLED=wireguard-tools magitrickle", "PKG_LOG="+filepath.ToSlash(log))
	calls, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return installerResult{output: output, calls: string(calls), err: runErr}, paths
}

func TestInstallerRemoveMode(t *testing.T) {
	res, paths := runRemove(t, "remove", "yes", []string{
		"dhcp.@dnsmasq[0].server='127.0.0.1#3553'",
		"dhcp.@dnsmasq[0].mawg_server_bak='8.8.8.8'",
		"network.proton.proto='amneziawg'",
	})
	if res.err != nil {
		t.Fatalf("remove не прошёл: %v\n%s", res.err, res.output)
	}
	for _, p := range []string{paths["/usr/bin/mawg"], paths["/etc/init.d/mawg"], paths["/var/log/mawg.log"],
		paths["/usr/bin/sing-box"], paths["/etc/sing-box-lx"], paths["/etc/magitrickle"]} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("не удалено: %s", p)
		}
	}
	if _, err := os.Stat(paths["/etc/mawg"]); err != nil {
		t.Fatalf("данные при remove должны сохраняться: %v", err)
	}
	// mihomo без маркера /etc/mihomo-mawg - не наша постановка, не тронут
	if _, err := os.Stat(paths["/usr/bin/mihomo"]); err != nil {
		t.Fatalf("чужой mihomo не должен удаляться: %v", err)
	}
	// dnsmasq: server=MT заменён на bak, bak удалён
	if strings.Contains(string(res.calls), "add_list dhcp.@dnsmasq[0].server=8.8.8.8") {
		// порядок вызовов не важен - проверяем наличие
	} else {
		t.Fatalf("bak не восстановлен в dnsmasq: %s", res.calls)
	}
	if strings.Count(string(res.calls), "delete dhcp.@dnsmasq[0].server") < 1 {
		t.Fatalf("server с MT:3553 не снят: %s", res.calls)
	}
	if strings.Count(string(res.calls), "delete dhcp.@dnsmasq[0].mawg_server_bak") < 1 {
		t.Fatalf("bak-ключ не удалён: %s", res.calls)
	}
	// MT: пакет снят, цепочки промыты (jump -D, потом -F/-X)
	for _, want := range []string{
		"opkg remove magitrickle",
		"iptables -t nat -D PREROUTING -j MT_DNSOR",
		"iptables -t nat -F MT_DNSOR",
		"iptables -t nat -X MT_DNSOR",
		"iptables -t mangle -X MT_fd1e1ebe",
	} {
		if !strings.Contains(string(res.calls), want) {
			t.Fatalf("нет вызова %q:\n%s", want, res.calls)
		}
	}
	if !strings.Contains(res.output, "данные") || !strings.Contains(res.output, "сохранены") {
		t.Fatalf("remove должен сохранять данные: %s", res.output)
	}
	if !strings.Contains(res.output, "остались uci-секции") {
		t.Fatalf("hint про пулы не показан: %s", res.output)
	}
}

func TestInstallerPurgeMode(t *testing.T) {
	res, paths := runRemove(t, "purge", "yes", []string{
		"dhcp.@dnsmasq[0].server='127.0.0.1#3553'",
	})
	if res.err != nil {
		t.Fatalf("purge не прошёл: %v\n%s", res.err, res.output)
	}
	if _, err := os.Stat(paths["/etc/mawg"]); !os.IsNotExist(err) {
		t.Fatalf("purge должен удалять данные: %v", err)
	}
	if !strings.Contains(res.output, "удалены") {
		t.Fatalf("нет сообщения о purged-данных: %s", res.output)
	}
}

func TestInstallerRemoveKeepsMT(t *testing.T) {
	// --without-magitrickle: MT могли поставить сами и отдельно - не трогаем
	res, _ := runRemove(t, "remove", "no", []string{
		"dhcp.@dnsmasq[0].server='127.0.0.1#3553'",
	})
	if res.err != nil {
		t.Fatalf("remove без MT не прошёл: %v\n%s", res.err, res.output)
	}
	if strings.Contains(string(res.calls), "remove magitrickle") {
		t.Fatalf("WANT_MT=no должен сохранять MagiTrickle: %s", res.calls)
	}
	if !strings.Contains(res.output, "удаление mawg") {
		t.Fatalf("mawg должен удаляться: %s", res.output)
	}
}
