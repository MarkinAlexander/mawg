package web

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"mawg/internal/magitrickle"
	"mawg/internal/mtinstall"
	"mawg/internal/platform/fake"
	"mawg/internal/provision"
	"mawg/internal/rotator"
	"mawg/internal/store"
)

func enrichServer(t *testing.T, mtURL string) *Server {
	t.Helper()
	magitrickle.SetShadowStorage(nil, nil)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fb := fake.New()
	engine := rotator.New(st, fb, nil)
	return New(st, engine, fb, magitrickle.New(mtURL), "test", nil)
}

// enrichMagitrickleVersion: ревизия бинаря вместо версии пакета; форк
// старше последнего релиза - «обновить до revN»; актуальный форк и
// оффлайн-GitHub - кнопки нет; апстрим без rev - «обновить до mawg-форка»
func TestEnrichMagitrickleUpdateButton(t *testing.T) {
	fm := &fakeMT{version: "0.8.2-rev10-mawg-dev"}
	mt := httptest.NewServer(fm.handler())
	t.Cleanup(mt.Close)
	s := enrichServer(t, mt.URL)

	orig := mtLatestRelease
	t.Cleanup(func() { mtLatestRelease = orig })

	enrich := func() provision.Item {
		items := []provision.Item{{ID: "magitrickle", Title: "MagiTrickle", Installed: true, Version: "0.8.2-10"}}
		s.enrichMagitrickleVersion(items)
		return items[0]
	}

	// форк rev10 при живом релизе rev11: кнопка «обновить до rev11»
	mtLatestRelease = func(context.Context) *mtinstall.Release {
		return &mtinstall.Release{TagName: "0.8.2-rev11"}
	}
	it := enrich()
	if it.ActionLabel != "обновить до rev11" || it.Action != provision.MtForkAction {
		t.Fatalf("кнопка обновления: %+v", it)
	}
	if !strings.Contains(it.Version, "0.8.2-rev10-mawg-dev") || !strings.Contains(it.Version, "пакет 0.8.2-10") {
		t.Fatalf("версия бинаря с пакетом: %q", it.Version)
	}
	if !strings.Contains(it.Confirm, "rev11") {
		t.Fatalf("confirm должен называть ревизию: %q", it.Confirm)
	}

	// та же ревизия, что и релиз: кнопки нет
	fm.mu.Lock()
	fm.version = "0.8.2-rev11-mawg-dev"
	fm.mu.Unlock()
	it = enrich()
	if it.Action != "" || it.ActionLabel != "" {
		t.Fatalf("на актуальной ревизии кнопки быть не должно: %+v", it)
	}

	// GitHub недоступен: кнопки нет
	fm.mu.Lock()
	fm.version = "0.8.2-rev10-mawg-dev"
	fm.mu.Unlock()
	mtLatestRelease = func(context.Context) *mtinstall.Release { return nil }
	it = enrich()
	if it.Action != "" || it.ActionLabel != "" {
		t.Fatalf("оффлайн-GitHub - кнопки быть не должно: %+v", it)
	}

	// апстримный бинарь без rev: прежнее «обновить до mawg-форка»
	fm.mu.Lock()
	fm.version = "0.8.2"
	fm.mu.Unlock()
	mtLatestRelease = func(context.Context) *mtinstall.Release {
		return &mtinstall.Release{TagName: "0.8.2-rev11"}
	}
	it = enrich()
	if it.ActionLabel != "обновить до mawg-форка" {
		t.Fatalf("апстрим: %+v", it)
	}
}
