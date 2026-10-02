package provision

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestOpenwrtNativeOpkgWithAPKUsesMatchingMagiTrickleFeed(t *testing.T) {
	f := &packageFixture{apk: true, opkg: true, nativeOpkg: true}
	res := checkOpenwrt(f.run)
	for _, item := range res.Items {
		if item.ID == "magitrickle" && !strings.Contains(item.Action, "opkg update && opkg install magitrickle") {
			t.Fatalf("native opkg repository setup must match install action: %s", item.Action)
		}
	}
	for _, command := range f.commands {
		if strings.HasPrefix(command, "apk ") {
			t.Fatalf("native opkg must match MagiTrickle repository selection: %s", command)
		}
	}
}

func TestOpenwrtAWGFallbackActions(t *testing.T) {
	for _, manager := range []string{"apk", "opkg"} {
		for _, release := range []string{"24.10.3", "25.12.4", "SNAPSHOT", "26.01.0"} {
			for _, oldTools := range []bool{false, true} {
				name := manager + "/" + release + "/missing"
				if oldTools {
					name = manager + "/" + release + "/upgrade"
				}
				t.Run(name, func(t *testing.T) {
					f := &packageFixture{apk: manager == "apk", opkg: manager == "opkg", release: release}
					if oldTools {
						f.installed = map[string]string{"amneziawg-tools": "1.0.20210914-r4"}
					}
					res := checkOpenwrt(f.run)
					want := awgDirectAction
					if manager == "apk" || strings.HasPrefix(release, "25.") {
						want = awgScriptAction
					}
					if manager == "opkg" && oldTools && !strings.HasPrefix(release, "24.10") && !strings.HasPrefix(release, "25.") {
						want = ""
					}
					item := resultItem(t, res, "awg")
					if item.Installed || item.Action != want {
						t.Errorf("AWG = %+v; want action %q", item, want)
					}
					if oldTools && item.Version != "1.0.20210914-r4" {
						t.Errorf("lost old version: %+v", item)
					}
					if manager == "apk" {
						for _, item := range res.Items {
							if strings.Contains(item.Action, "opkg") || strings.Contains(item.Action, ".ipk") {
								t.Errorf("apk action must never install ipk: %+v", item)
							}
						}
					}
				})
			}
		}
	}
}

func TestOpenwrtNoPackageManager(t *testing.T) {
	f := &packageFixture{fail: map[string]string{"command -v apk": "/sbin/apk", "command -v opkg": "/bin/opkg"}}
	res := checkOpenwrt(f.run)
	if res.Platform != "openwrt" || len(res.Items) != 3 {
		t.Fatalf("unexpected result: %+v", res)
	}
	for _, item := range res.Items {
		if item.Installed || item.Version != "" || item.Action != "" || item.Confirm != "" || item.Note == "" {
			t.Errorf("no-manager result must report unknown with no install action: %+v", item)
		}
	}
	if len(f.commands) != 3 {
		t.Errorf("no-manager check must stop after detection: %v", f.commands)
	}
}

func TestOpenwrtDetectionFailureFallsBackToOpkg(t *testing.T) {
	f := &packageFixture{apk: true, opkg: true, installed: map[string]string{"kmod-wireguard": "6.6.0-r1"}, fail: map[string]string{"command -v apk": "/sbin/apk"}}
	res := checkOpenwrt(f.run)
	if got := resultItem(t, res, "wireguard"); !got.Installed || got.Version != "6.6.0-r1" {
		t.Errorf("opkg fallback failed: %+v", got)
	}
	for _, command := range f.commands {
		if strings.HasPrefix(command, "apk ") {
			t.Errorf("failed detection must not select apk: %s", command)
		}
	}
}

func TestOpenwrtInstalledQueryFailure(t *testing.T) {
	for _, manager := range []string{"apk", "opkg"} {
		t.Run(manager, func(t *testing.T) {
			prefix, output := "apk list --installed --manifest", "kmod-wireguard 6.12.87-r1\namneziawg-tools 3.1.20260812-r1\nmagitrickle 0.8.2-r1"
			if manager == "opkg" {
				prefix, output = "opkg list-installed", "kmod-wireguard - 6.6.0-r1"
			}
			f := &packageFixture{apk: manager == "apk", opkg: manager == "opkg", fail: map[string]string{prefix: output}, release: "25.12.4"}
			res := checkOpenwrt(f.run)
			for _, item := range res.Items {
				if item.Installed || item.Version != "" {
					t.Errorf("failed query output must not count as installed: %+v", item)
				}
			}
		})
	}
}

func TestOpenwrtAvailableQueryFailure(t *testing.T) {
	for _, manager := range []string{"apk", "opkg"} {
		t.Run(manager, func(t *testing.T) {
			prefix, output := "apk search -x amneziawg-tools", "amneziawg-tools-3.1.20260812-r1"
			if manager == "opkg" {
				prefix, output = "opkg list 2>/dev/null", "amneziawg-tools - 1.0.0 - AWG"
			}
			f := &packageFixture{apk: manager == "apk", opkg: manager == "opkg", fail: map[string]string{prefix: output}, release: "25.12.4"}
			if got := resultItem(t, checkOpenwrt(f.run), "awg").Action; got != awgScriptAction {
				t.Errorf("failed available query must use external fallback, got %q", got)
			}
		})
	}
}

func TestOpenwrtAPKInstalledExactNames(t *testing.T) {
	f := &packageFixture{apk: true, installed: map[string]string{
		"kmod-wireguard-extra": "6.12.87-r1", "prefix-kmod-wireguard": "6.12.87-r1",
		"amneziawg-tools-extra": "3.1.20260812-r1", "kmod-amneziawg-extra": "6.12.87-r1",
		"magitrickle-extra": "0.8.2-r1", "prefix-magitrickle": "0.8.2-r1",
	}, release: "SNAPSHOT"}
	for _, item := range checkOpenwrt(f.run).Items {
		if item.Installed {
			t.Errorf("similar package name must not count as installed: %+v", item)
		}
	}
}

func TestOpenwrtAPKAvailableExactNames(t *testing.T) {
	for _, output := range []string{"", "amneziawg-tools-extra-3.1-r1", "prefix-amneziawg-tools-3.1-r1", "amneziawg-tools", "ERROR: amneziawg-tools-3.1-r1", "amneziawg-tools-3.1-r1 description", "amneziawg-tools-dev-3.1-r1\namneziawg-tools-extra-3.1-r1"} {
		t.Run(output, func(t *testing.T) {
			f := &packageFixture{apk: true, available: output, release: "SNAPSHOT"}
			if got := resultItem(t, checkOpenwrt(f.run), "awg").Action; got != awgScriptAction {
				t.Errorf("nonexact search output %q must use fallback; got %q", output, got)
			}
		})
	}
	f := &packageFixture{apk: true, available: "amneziawg-tools-extra-3.1-r1\namneziawg-tools-3.1.20260812-r1\n"}
	if got := resultItem(t, checkOpenwrt(f.run), "awg").Action; got != "apk update && apk add kmod-amneziawg amneziawg-tools" {
		t.Errorf("exact result not found in multiline output: %q", got)
	}
}

type packageFixture struct {
	apk, opkg  bool
	nativeOpkg bool
	installed  map[string]string
	available  string
	release    string
	fail       map[string]string
	commands   []string
}

func (f *packageFixture) run(script string, _ time.Duration) (string, error) {
	f.commands = append(f.commands, script)
	for prefix, output := range f.fail {
		if strings.HasPrefix(script, prefix) {
			return output, errors.New("command failed")
		}
	}
	switch {
	case script == "test -x /bin/opkg":
		if f.nativeOpkg {
			return "", nil
		}
	case strings.HasPrefix(script, "command -v apk"):
		if f.apk {
			return "/sbin/apk\n", nil
		}
	case strings.HasPrefix(script, "command -v opkg"):
		if f.opkg {
			return "/bin/opkg\n", nil
		}
	case script == "apk list --installed --manifest":
		if f.apk {
			var lines []string
			for pkg, version := range f.installed {
				lines = append(lines, pkg+" "+version)
			}
			return strings.Join(lines, "\n"), nil
		}
	case script == "apk search -x amneziawg-tools":
		if f.apk {
			return f.available, nil
		}
	case strings.HasPrefix(script, "opkg list-installed 2>/dev/null | grep '"):
		if f.opkg {
			pkg := strings.TrimSuffix(strings.TrimPrefix(script, "opkg list-installed 2>/dev/null | grep '^"), " '")
			if version, ok := f.installed[pkg]; ok {
				return pkg + " - " + version, nil
			}
			return "", errors.New("not installed")
		}
	case script == "opkg list 2>/dev/null | grep -m1 '^amneziawg-tools '":
		if f.opkg {
			return f.available, nil
		}
	case strings.HasPrefix(script, ". /etc/openwrt_release"):
		return f.release, nil
	default:
		return "", errors.New("unexpected command: " + script)
	}
	return "", errors.New("command not found")
}

func resultItem(t *testing.T, res Result, id string) Item {
	t.Helper()
	for _, item := range res.Items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("missing item %q in %+v", id, res)
	return Item{}
}

func TestOpenwrtAPKInstalledVersionsAndPreference(t *testing.T) {
	f := &packageFixture{apk: true, opkg: true, installed: map[string]string{
		"kmod-wireguard":  "6.12.87-r1",
		"amneziawg-tools": "3.1.20260812-r1",
		"magitrickle":     "0.8.2-r1",
	}}
	res := checkOpenwrt(f.run)
	for id, version := range map[string]string{"wireguard": "6.12.87-r1", "awg": "3.1.20260812-r1", "magitrickle": "0.8.2-r1"} {
		item := resultItem(t, res, id)
		if !item.Installed || item.Version != version || item.Action != "" {
			t.Errorf("%s = %+v; want installed version %s without action", id, item, version)
		}
	}
	if res.Platform != "openwrt" {
		t.Errorf("platform = %q", res.Platform)
	}
	if len(f.commands) < 2 || !strings.HasPrefix(f.commands[1], "command -v apk") {
		t.Errorf("must detect apk after checking native opkg; commands = %v", f.commands)
	}
	for _, command := range f.commands {
		if strings.HasPrefix(command, "opkg ") {
			t.Errorf("apk must take precedence over nonnative opkg: %s", command)
		}
	}
}

func TestOpenwrtOpkgInstalledVersions(t *testing.T) {
	f := &packageFixture{opkg: true, installed: map[string]string{
		"kmod-wireguard":  "6.6.93-r1",
		"amneziawg-tools": "3.1.20260812-r1",
		"magitrickle":     "0.8.2-r1",
	}}
	res := checkOpenwrt(f.run)
	for id, version := range map[string]string{"wireguard": "6.6.93-r1", "awg": "3.1.20260812-r1", "magitrickle": "0.8.2-r1"} {
		item := resultItem(t, res, id)
		if !item.Installed || item.Version != version || item.Action != "" {
			t.Errorf("%s = %+v; want installed version %s without action", id, item, version)
		}
	}
}

func TestParseOpkgInstalledLegacyFormats(t *testing.T) {
	for _, test := range []struct {
		output    string
		installed bool
		version   string
	}{
		{"", false, ""},
		{"magitrickle - 0.8.2-r1\n", true, "0.8.2-r1"},
		{"magitrickle 0.8.2-r1\n", true, "0.8.2-r1"},
		{"magitrickle", true, ""},
	} {
		installed, version := parseOpkgInstalled(test.output)
		if installed != test.installed || version != test.version {
			t.Errorf("parseOpkgInstalled(%q) = %v, %q; want %v, %q", test.output, installed, version, test.installed, test.version)
		}
	}
}

func TestOpenwrtMissingPackageActions(t *testing.T) {
	for _, manager := range []string{"apk", "opkg"} {
		t.Run(manager, func(t *testing.T) {
			available := "amneziawg-tools-3.1.20260812-r1"
			install := "apk update && apk add "
			if manager == "opkg" {
				available = "amneziawg-tools - 1.0.0 - AWG tools"
				install = "opkg update && opkg install "
			}
			f := &packageFixture{apk: manager == "apk", opkg: manager == "opkg", available: available}
			res := checkOpenwrt(f.run)
			for id, packages := range map[string]string{
				"wireguard":   "kmod-wireguard wireguard-tools luci-proto-wireguard",
				"awg":         "kmod-amneziawg amneziawg-tools",
				"magitrickle": "magitrickle",
			} {
				item := resultItem(t, res, id)
				if item.Installed || !strings.Contains(item.Action, install+packages) || item.Confirm == "" {
					t.Errorf("%s = %+v; want missing with %s%s", id, item, install, packages)
				}
			}
			mt := resultItem(t, res, "magitrickle")
			if !strings.Contains(mt.Action, "add_repo.sh && sh /tmp/mt-repo.sh") || !strings.Contains(mt.Action, "/etc/init.d/magitrickle enable && /etc/init.d/magitrickle start") {
				t.Errorf("MagiTrickle setup/start lost: %s", mt.Action)
			}
		})
	}
}

func TestOpenwrtKernelOnlyToolsAction(t *testing.T) {
	for _, manager := range []string{"apk", "opkg"} {
		t.Run(manager, func(t *testing.T) {
			f := &packageFixture{apk: manager == "apk", opkg: manager == "opkg", installed: map[string]string{"kmod-amneziawg": "6.12.87-r1"}}
			res := checkOpenwrt(f.run)
			if !resultItem(t, res, "awg").Installed {
				t.Fatal("kernel-only AWG must retain installed status")
			}
			want := "apk update && apk add amneziawg-tools"
			if manager == "opkg" {
				want = "opkg update && opkg install amneziawg-tools"
			}
			if got := resultItem(t, res, "awg-tools").Action; got != want {
				t.Errorf("action = %q; want %q", got, want)
			}
		})
	}
}
