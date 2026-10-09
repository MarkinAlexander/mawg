package singbox

import (
	"testing"
	"time"
)

func TestParseAssetName(t *testing.T) {
	cases := []struct {
		name string
		arch string
		upx  bool
		ok   bool
	}{
		{"sing-box-1.14.2-lx.12-linux-amd64.tar.gz", "amd64", false, true},
		{"sing-box-1.14.2-lx.12-linux-mipsle-softfloat.tar.gz", "mipsle-softfloat", false, true},
		{"sing-box-1.14.2-lx.12-linux-386.tar.gz", "386", false, true},
		{"sing-box-1.14.2-lx.12-linux-riscv64.tar.gz", "riscv64", false, true},
		{"sing-box-1.14.2-lx.12-linux-amd64.upx.tar.gz", "amd64", true, true},
		{"sing-box-1.14.2-lx.12-linux-mips-softfloat.upx.tar.gz", "mips-softfloat", true, true},
		{"sing-box-1.14.2-lx.12-windows-amd64.zip", "", false, false},
		{"sing-box-1.14.2-lx.12-linux-amd64.tar.gz.sha256", "", false, false},
		{"SHA256SUMS", "", false, false},
		{"random.tar.gz", "", false, false},
	}
	for _, c := range cases {
		arch, upx, ok := parseAssetName(c.name)
		if ok != c.ok || arch != c.arch || upx != c.upx {
			t.Errorf("%s: got %q %v %v, want %q %v %v", c.name, arch, upx, ok, c.arch, c.upx, c.ok)
		}
	}
}

func TestBuildReleaseMatrix(t *testing.T) {
	r := buildRelease(ghRelease{
		TagName:   "v1.14.2-lx.12",
		HTMLURL:   "https://github.com/MarkinAlexander/sing-box-lx/releases/tag/v1.14.2-lx.12",
		Published: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		Assets: []ghAsset{
			{Name: "sing-box-1.14.2-lx.12-linux-amd64.tar.gz"},
			{Name: "sing-box-1.14.2-lx.12-linux-amd64.upx.tar.gz"},
			{Name: "sing-box-1.14.2-lx.12-linux-mipsle-softfloat.tar.gz"},
			{Name: "sing-box-1.14.2-lx.12-windows-amd64.zip"},
			{Name: "SHA256SUMS"},
		},
	})
	if !r.SHA256SUM {
		t.Fatal("SHA256SUMS не замечен")
	}
	if r.Matrix["amd64"] != (Flavors{Plain: true, UPX: true}) {
		t.Fatalf("amd64: %+v", r.Matrix["amd64"])
	}
	if r.Matrix["mipsle-softfloat"] != (Flavors{Plain: true}) {
		t.Fatalf("mipsle: %+v", r.Matrix["mipsle-softfloat"])
	}
	if _, has := r.Matrix["windows-amd64"]; has {
		t.Fatal("не-linux ассет не должен попадать в матрицу")
	}
}

func TestBuildReleaseWithoutSHA(t *testing.T) {
	r := buildRelease(ghRelease{
		TagName: "v1",
		Assets:  []ghAsset{{Name: "sing-box-1-linux-arm64.tar.gz"}},
	})
	if r.SHA256SUM {
		t.Fatal("SHA256SUM не должно быть")
	}
	if r.Matrix["arm64"] != (Flavors{Plain: true}) {
		t.Fatalf("arm64: %+v", r.Matrix["arm64"])
	}
}

func TestSourceReportLatest(t *testing.T) {
	var empty SourceReport
	if empty.Latest() != nil {
		t.Fatal("у пустого отчёта нет последнего релиза")
	}
	rep := SourceReport{Releases: []Release{{Tag: "v2"}, {Tag: "v1"}}}
	if rep.Latest().Tag != "v2" {
		t.Fatal("последний релиз — первый в списке")
	}
}
