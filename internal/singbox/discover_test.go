package singbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func lxMarker() string  { return ".upx" }
func extMarker() string { return "-compressed" }

func TestParseAssetName(t *testing.T) {
	cases := []struct {
		name   string
		marker string
		arch   string
		upx    bool
		ok     bool
	}{
		// lx: сжатый профиль - суффикс .upx
		{"sing-box-1.14.2-lx.12-linux-amd64.tar.gz", lxMarker(), "amd64", false, true},
		{"sing-box-1.14.2-lx.12-linux-mipsle-softfloat.tar.gz", lxMarker(), "mipsle-softfloat", false, true},
		{"sing-box-1.14.2-lx.12-linux-386.tar.gz", lxMarker(), "386", false, true},
		{"sing-box-1.14.2-lx.12-linux-riscv64.tar.gz", lxMarker(), "riscv64", false, true},
		{"sing-box-1.14.2-lx.12-linux-amd64.upx.tar.gz", lxMarker(), "amd64", true, true},
		{"sing-box-1.14.2-lx.12-linux-mips-softfloat.upx.tar.gz", lxMarker(), "mips-softfloat", true, true},
		{"sing-box-1.14.2-lx.12-windows-amd64.zip", lxMarker(), "", false, false},
		{"sing-box-1.14.2-lx.12-linux-amd64.tar.gz.sha256", lxMarker(), "", false, false},
		{"SHA256SUMS", lxMarker(), "", false, false},
		{"random.tar.gz", lxMarker(), "", false, false},
		// extended: сжатый профиль - суффикс -compressed
		{"sing-box-1.14.3-extended-2.7.3-linux-mipsle-softfloat.tar.gz", extMarker(), "mipsle-softfloat", false, true},
		{"sing-box-1.14.3-extended-2.7.3-linux-mipsle-softfloat-compressed.tar.gz", extMarker(), "mipsle-softfloat", true, true},
		{"sing-box-1.14.3-extended-2.7.3-linux-386-compressed.tar.gz", extMarker(), "386", true, true},
		{"sing-box-1.14.3-extended-2.7.3-linux-arm64.tar.gz", extMarker(), "arm64", false, true},
		// у extended .upx-профиля нет: файл с .upx не разбирается как ассет
		{"sing-box-1.14.3-extended-2.7.3-linux-386.upx.tar.gz", extMarker(), "", false, false},
		// апстрим: сжатого профиля нет вовсе
		{"sing-box-1.14.4-linux-mipsle-softfloat.tar.gz", "", "mipsle-softfloat", false, true},
		{"sing-box-1.14.4-linux-386-softfloat.tar.gz", "", "386-softfloat", false, true},
		{"sing-box-1.14.4-linux-amd64.tar.gz", "", "amd64", false, true},
		// динамические/альтернативные сборки не наши
		{"sing-box-1.14.4-linux-386-glibc.tar.gz", "", "", false, false},
		{"sing-box-1.14.4-linux-amd64-musl.tar.gz", "", "", false, false},
		{"sing-box-1.14.3-extended-2.7.3-linux-amd64-purego.tar.gz", extMarker(), "", false, false},
	}
	for _, c := range cases {
		arch, upx, ok := parseAssetName(c.name, c.marker)
		if ok != c.ok || arch != c.arch || upx != c.upx {
			t.Errorf("%s: got %q %v %v, want %q %v %v", c.name, arch, upx, ok, c.arch, c.upx, c.ok)
		}
	}
}

func TestBuildReleaseMatrix(t *testing.T) {
	lx, _ := SourceByID(SourceLXID)
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
	}, lx)
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
	lx, _ := SourceByID(SourceLXID)
	r := buildRelease(ghRelease{
		TagName: "v1",
		Assets:  []ghAsset{{Name: "sing-box-1-linux-arm64.tar.gz"}},
	}, lx)
	if r.SHA256SUM {
		t.Fatal("SHA256SUM не должно быть")
	}
	if r.Matrix["arm64"] != (Flavors{Plain: true}) {
		t.Fatalf("arm64: %+v", r.Matrix["arm64"])
	}
}

// extended/апстрим: digest-поля ассетов GitHub API становятся суммами
// FlavorAssets, сжатый профиль узнаётся по -compressed
func TestBuildReleaseDigests(t *testing.T) {
	ext, _ := SourceByID(SourceExtID)
	if ext.SumsFile {
		t.Fatal("у extended нет SHA256SUMS-файла")
	}
	r := buildRelease(ghRelease{
		TagName: "v1.14.3-extended-2.7.3",
		Assets: []ghAsset{
			{Name: "sing-box-1.14.3-extended-2.7.3-linux-386.tar.gz", Digest: "sha256:cafe"},
			{Name: "sing-box-1.14.3-extended-2.7.3-linux-386-compressed.tar.gz", Digest: "sha256:beef"},
			{Name: "sing-box-1.14.3-extended-2.7.3-linux-386-glibc.tar.gz", Digest: "sha256:dead"},
		},
	}, ext)
	if r.SHA256SUM {
		t.Fatal("SHA256SUMS не должно быть")
	}
	n := r.Names["386"]
	if n.PlainSHA != "cafe" || n.UPXSHA != "beef" {
		t.Fatalf("digests: %+v", n)
	}
	if _, has := r.Matrix["386-glibc"]; has {
		t.Fatal("glibc-вариант не должен попадать в матрицу")
	}
	if r.Matrix["386"] != (Flavors{Plain: true, UPX: true}) {
		t.Fatalf("386: %+v", r.Matrix["386"])
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

func TestSourceIDByRepo(t *testing.T) {
	for _, c := range []struct{ repo, id string }{
		{"Leadaxe/sing-box-lx", SourceLXID},
		{"MarkinAlexander/sing-box-lx", SourceLXOwnID},
		{"shtorm-7/sing-box-extended", SourceExtID},
		{"SagerNet/sing-box", SourceUpID},
	} {
		got, ok := SourceIDByRepo(c.repo)
		if !ok || got != c.id {
			t.Errorf("SourceIDByRepo(%q) = %q,%v want %q", c.repo, got, ok, c.id)
		}
	}
	if _, ok := SourceIDByRepo("unknown/repo"); ok {
		t.Error("неизвестный репозиторий не должен резолвиться")
	}
	// порядок Sources: дефолт (первый) - extended, все id уникальны
	seen := map[string]bool{}
	for _, s := range Sources {
		if seen[s.ID] {
			t.Fatalf("дубль id %s", s.ID)
		}
		seen[s.ID] = true
	}
	if Sources[0].ID != SourceExtID {
		t.Fatalf("первый источник должен быть дефолтом extended: %s", Sources[0].ID)
	}
}

// prereleases (альфы/рц) не попадают в дискавери: GitHub отдаёт их первыми,
// у альф нет части платформ
func TestFetchReleasesSkipsPrerelease(t *testing.T) {
	lx, _ := SourceByID(SourceLXID)
	rels := []Release{}
	for _, r := range []ghRelease{
		{TagName: "v1.14.4-lx.24-rc.3", Prerelease: true, Assets: []ghAsset{{Name: "sing-box-1.14.4-lx.24-rc.3-linux-386.tar.gz"}}},
		{TagName: "v1.14.4-lx.24", Assets: []ghAsset{{Name: "sing-box-1.14.4-lx.24-linux-386.tar.gz"}}},
	} {
		if r.Prerelease {
			continue
		}
		rels = append(rels, buildRelease(r, lx))
	}
	if len(rels) != 1 || rels[0].Tag != "v1.14.4-lx.24" {
		t.Fatalf("prerelease должен отфильтроваться: %+v", rels)
	}
}

// обрезанный ответ (капризный маршрут) ретраится и проходит вторым ударом;
// статусная ошибка (403) ретраем не размазывается
func TestFetchReleasesRetryOnTruncation(t *testing.T) {
	lx, _ := SourceByID(SourceLXID)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			// первый удар - обрыв посреди JSON
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[{"tag_name":"v1.14.4-lx.24","assets":[{"name":"a.tar.gz"} `))
			return
		}
		w.Write([]byte(`[{"tag_name":"v1.14.4-lx.24","assets":[{"name":"sing-box-1.14.4-lx.24-linux-386.tar.gz"}]}]`))
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old })
	rels, err := fetchReleases(context.Background(), lx)
	if err != nil {
		t.Fatalf("ретрай не спас от обрезки: %v", err)
	}
	if len(rels) != 1 || rels[0].Tag != "v1.14.4-lx.24" {
		t.Fatalf("rels: %+v", rels)
	}
}

func TestFetchReleasesNoRetryOnStatus(t *testing.T) {
	lx, _ := SourceByID(SourceLXID)
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old })
	if _, err := fetchReleases(context.Background(), lx); err == nil {
		t.Fatal("403 должен ошибаться")
	}
	if hits != 1 {
		t.Fatalf("403 не должен ретраиться, ударов %d", hits)
	}
}
