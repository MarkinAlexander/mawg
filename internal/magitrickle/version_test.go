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
