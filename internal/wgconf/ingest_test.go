package wgconf

import (
	"archive/zip"
	"bytes"
	"os"
	"testing"
)

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"ProtonVPN_SE-#1.conf": "protonvpn-se-1",
		"сервер конфиг.conf":   "conf",
		"WARP..test!!.conf":    "warp-test",
		"UPPER case name.conf": "upper-case-name",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Fatalf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUniqueSlug(t *testing.T) {
	taken := map[string]bool{"proton": true, "proton-2": true}
	got := UniqueSlug("proton", func(s string) bool { return taken[s] })
	if got != "proton-3" {
		t.Fatalf("UniqueSlug = %q", got)
	}
}

func TestSanitizePoolName(t *testing.T) {
	ok := []string{"proton", "warp", "Pool-1", "a"}
	for _, s := range ok {
		if _, err := SanitizePoolName(s); err != nil {
			t.Fatalf("SanitizePoolName(%q) err = %v", s, err)
		}
	}
	bad := []string{"", "1abc", "протон", "has space", "under_score", toolongname16}
	for _, s := range bad {
		if _, err := SanitizePoolName(s); err == nil {
			t.Fatalf("SanitizePoolName(%q) must fail", s)
		}
	}
}

const toolongname16 = "aaaaaaaaaaaaaaaaaaaaaaaaa"

func TestIngestDedupe(t *testing.T) {
	plain, err := os.ReadFile("../../testdata/plain-wg.conf")
	if err != nil {
		t.Fatal(err)
	}
	got, err := IngestFile("test.conf", plain)
	if err != nil || len(got) != 1 {
		t.Fatalf("IngestFile conf: %v %d", err, len(got))
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string][]byte{
		"ProtonVPN_AT-1.conf": replaceKey(plain, "qELLdGEcuWN4Cw04dU6EcD6nSuVPWHFISHlWFPjlyl3="),
		"ProtonVPN_AT-2.conf": replaceKey(plain, "qELLdGEcuWN4Cw04dU6EcD6nSuVPWHFISHlWFPjlyl4="),
		"sub/dir/DE-1.conf":   replaceKey(plain, "qELLdGEcuWN4Cw04dU6EcD6nSuVPWHFISHlWFPjlyl5="),
		"same-as-AT-1.conf":   replaceKey(plain, "qELLdGEcuWN4Cw04dU6EcD6nSuVPWHFISHlWFPjlyl3="),
		"readme.txt":          []byte("not a config"),
	}
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(body)
	}
	zw.Close()

	parsed, err := IngestZip(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	kept, dupes := Dedupe(parsed)
	if len(kept) != 3 || len(dupes) != 1 {
		t.Fatalf("kept %d, dupes %d", len(kept), len(dupes))
	}

	if _, err := IngestFile("notes.txt", []byte("x")); err == nil {
		t.Fatal("non-conf file must be rejected")
	}
}

func replaceKey(src []byte, key string) []byte {
	out := bytes.Replace(src, []byte("qELLdGEcuWN4Cw04dU6EcD6nSuVPWHFISHlWFPjlyl2="), []byte(key), 1)
	out = bytes.Replace(out, []byte("se-01.protonvpn.net"), []byte(key[0:8]+".example.net"), 1)
	return out
}
