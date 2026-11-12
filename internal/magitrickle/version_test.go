package magitrickle

import "testing"

func TestSourcesSupported(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{
		{"0.8.2-rev2-mawgfix2", false},
		{"0.8.2-rev3-mawgfix2", true},
		{"0.8.2-rev4-mawgfix2", true},
		{"0.8.2-rev5", true},
		{"0.8.2-rev10", true},
		{"0.8.2", false},
		{"unattached", false},
		{"", false},
		{"rev", false},
	}
	for _, c := range cases {
		if got := SourcesSupported(c.version); got != c.want {
			t.Errorf("SourcesSupported(%q) = %v, want %v", c.version, got, c.want)
		}
	}
}

func TestDnsUpstreamsSupported(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{
		{"0.8.2-rev4-mawgfix2", false}, // Кинетик
		{"0.8.2-rev7-mawgfix1", false}, // стенд до апгрейда
		{"0.8.2-rev8-mawgfix1", true},
		{"0.8.2-rev10", true},
		{"0.8.2", false},
		{"unattached", false},
		{"", false},
	}
	for _, c := range cases {
		if got := DnsUpstreamsSupported(c.version); got != c.want {
			t.Errorf("DnsUpstreamsSupported(%q) = %v, want %v", c.version, got, c.want)
		}
	}
}

func TestFlavorOf(t *testing.T) {
	cases := []struct {
		version string
		want    MtFlavor
	}{
		{"0.8.2-rev3-mawg-c-kn", FlavorMtC},   // KN-сборка mt-c (kinetic_build_kn.sh)
		{"0.8.2-rev3-mawg-c", FlavorMtC},      // mt-c без KN-суффикса
		{"0.8.2-rev16-mawg-dev", FlavorGoFork},
		{"0.8.2-rev14-mawgfix1", FlavorGoFork}, // старые суффиксы Go-форка
		{"0.8.2-rev4-mawgfix2", FlavorGoFork},
		{"0.8.2", FlavorUpstream},
		{"unattached", FlavorUpstream},
		{"", FlavorUpstream}, // /version 404 (апстрим и старые форки)
		{"rev", FlavorUpstream},
		{"mawg-c-no-dash-prefix", FlavorGoFork}, // нет "-mawg-c" - не mt-c
	}
	for _, c := range cases {
		if got := FlavorOf(c.version); got != c.want {
			t.Errorf("FlavorOf(%q) = %q, want %q", c.version, got, c.want)
		}
	}
}

func TestConfigVersion(t *testing.T) {
	yaml := []byte("# comment: configVersion: 0.0.1\n" +
		"configVersion: 0.8.2-rev4-mawgfix2\n" +
		"other: value\n")
	if got := ConfigVersion(yaml); got != "0.8.2-rev4-mawgfix2" {
		t.Fatalf("ConfigVersion = %q", got)
	}
	if got := ConfigVersion([]byte("configVersion: \"0.8.2-rev2\"\n")); got != "0.8.2-rev2" {
		t.Fatalf("ConfigVersion quoted = %q", got)
	}
	if got := ConfigVersion([]byte("no version here\n")); got != "" {
		t.Fatalf("ConfigVersion missing = %q", got)
	}
	if !SourcesSupported(ConfigVersion(yaml)) {
		t.Fatal("sources capability lost via configVersion")
	}
}

func TestMtLogLevelSupported(t *testing.T) {
	cases := []struct {
		version string
		want    bool
	}{
		{"0.8.2-rev21", false}, // hosts есть, loglevel ещё нет
		{"0.8.2-rev22", true},
		{"0.8.2-rev22-mawgfix1", true},
		{"0.8.2-rev19-mawgfix1", false},
		{"0.8.2", false},
		{"unattached", false},
		{"", false},
	}
	for _, c := range cases {
		if got := MtLogLevelSupported(c.version); got != c.want {
			t.Errorf("MtLogLevelSupported(%q) = %v, want %v", c.version, got, c.want)
		}
	}
}
