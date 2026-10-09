package store

import "testing"

func TestValidProbeHost(t *testing.T) {
	valid := []string{"1.1.1.1", "8.8.8.8", "192.0.2.10", "162.159.193.10"}
	for _, h := range valid {
		if !ValidProbeHost(h) {
			t.Fatalf("%s must be valid", h)
		}
	}
	invalid := []string{
		"", "localhost", "192.168.0.1", "10.2.0.1", "172.16.0.1", "127.0.0.1",
		"169.254.1.1", "0.0.0.0", "255.255.255.255", "224.0.0.1", "2001:db8::1",
	}
	for _, h := range invalid {
		if ValidProbeHost(h) {
			t.Fatalf("%s must be invalid", h)
		}
	}
}

func TestCreatePoolRejectsBadProbe(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreatePool("p", PoolSettings{ProbeHost: "192.168.0.5"}); err == nil {
		t.Fatal("private probe host must be rejected")
	}
	if err := st.UpdatePool("p", PoolSettings{ProbeHost: "10.0.0.1"}); err == nil {
		t.Fatal("update with private probe host must be rejected")
	}
}
