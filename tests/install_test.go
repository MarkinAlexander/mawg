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
	repoStatus            int
	wantMagiTrickle       bool
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
	script += "do_fetch() { printf '#!/bin/sh\\nexit %s\\n' \"$MOCK_REPO_STATUS\" > \"$2\"; }\n"
	script += "PKG_MANAGER=$(detect_pkg_manager) || exit 1\n" + suffix
	output, runErr := runShell(t, script,
		fmt.Sprintf("MOCK_APK=%d", boolInt(f.apk)), fmt.Sprintf("MOCK_OPKG=%d", boolInt(f.opkg)),
		fmt.Sprintf("MOCK_NATIVE_OPKG=%d", boolInt(f.nativeOpkg)),
		"MOCK_INSTALLED="+f.installed, fmt.Sprintf("MOCK_UPDATE_STATUS=%d", f.updateStatus),
		fmt.Sprintf("MOCK_REPO_STATUS=%d", f.repoStatus), "MOCK_ARCH=mips_24kc", "PKG_LOG="+filepath.ToSlash(log))
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
			updateStatus, repoStatus     int
			wantMT, wantFailure          bool
		}{
			{name: "installed", installed: installedPackages},
			{name: "missing-wireguard", installed: "amneziawg-tools magitrickle", wantInstall: "kmod-wireguard wireguard-tools luci-proto-wireguard"},
			{name: "update-failed-installed", installed: installedPackages, updateStatus: 1},
			{name: "update-failed-missing", updateStatus: 1, wantFailure: true},
			{name: "missing-awg-and-magitrickle", installed: "wireguard-tools", wantMT: true, wantInstall: "magitrickle"},
			{name: "failed-magitrickle-feed", installed: "wireguard-tools amneziawg-tools", wantMT: true, repoStatus: 1},
			{name: "similar-package-name", installed: "wireguard-tools-extra amneziawg-tools magitrickle", wantInstall: "kmod-wireguard wireguard-tools luci-proto-wireguard"},
		} {
			t.Run(manager+"/"+scenario.name, func(t *testing.T) {
				fixture := installerFixture{apk: manager == "apk", opkg: manager == "opkg", installed: scenario.installed,
					updateStatus: scenario.updateStatus, repoStatus: scenario.repoStatus, wantMagiTrickle: scenario.wantMT}
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
				if scenario.repoStatus != 0 && !strings.Contains(result.output, "не удалось добавить") {
					t.Fatalf("missing repository failure warning: %s", result.output)
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
	result := (installerFixture{apk: true, opkg: true, nativeOpkg: true, installed: "wireguard-tools amneziawg-tools", wantMagiTrickle: true}).run(t, dependencyChecks(t))
	if result.err != nil || !strings.Contains(result.calls, "opkg install magitrickle") || strings.Contains(result.calls, "apk ") {
		t.Fatalf("native opkg feed/install mismatch: %v %s", result.err, result.calls)
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
