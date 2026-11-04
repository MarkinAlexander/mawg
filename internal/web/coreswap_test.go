package web

// Конвейер замены бинаря движка (112-док 4.2): pre-проверки без остановки
// движка, успешная замена, откат при неудачном старте/версии. Все внешние
// операции подменены; бинари - минимальные ELF-фейки с нужным e_machine.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeELF - бинарь с валидным ELF-заголовком под arch (данные - нули).
func writeFakeELF(t *testing.T, path, arch string, size int64) {
	t.Helper()
	hdr := make([]byte, 20)
	copy(hdr, "\x7fELF")
	var machine uint16
	le := true
	switch arch {
	case "amd64":
		machine = 62
	case "mipsle-softfloat":
		machine = 8
	case "arm64":
		machine = 183
	case "386":
		machine = 3
	default:
		t.Fatalf("неизвестная arch %q", arch)
	}
	if le {
		hdr[5] = 1
		hdr[18], hdr[19] = byte(machine), byte(machine>>8)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(hdr); err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

type swapFixture struct {
	staged, bin, markerDir string
	stopped, started       int
	versions               map[string]string
	verErr                 map[string]error
	startErr               error
	logs                   []string
}

func newSwapFixture(t *testing.T) *swapFixture {
	t.Helper()
	dir := t.TempDir()
	fx := &swapFixture{
		staged:     filepath.Join(dir, "staged.bin"),
		bin:        filepath.Join(dir, "target", "engine"),
		markerDir:  filepath.Join(dir, "marker"),
		versions:   map[string]string{},
		verErr:     map[string]error{},
		startErr:   nil,
	}
	if err := os.MkdirAll(filepath.Dir(fx.bin), 0o755); err != nil {
		t.Fatal(err)
	}
	old := swapDiskFree
	swapDiskFree = func(string) int64 { return 1 << 30 } // гигабайт свободно
	t.Cleanup(func() { swapDiskFree = old })
	return fx
}

func (fx *swapFixture) params(keepBackup bool) swapParams {
	return swapParams{
		Core: "singbox", Bin: fx.bin, MarkerDir: fx.markerDir, Staged: fx.staged,
		KeepBackup: keepBackup, RouterArch: "amd64",
		VersionFn: func(bin string) (string, error) {
			if err := fx.verErr[bin]; err != nil {
				return "", err
			}
			if v, ok := fx.versions[bin]; ok {
				return v, nil
			}
			return "", errors.New("не запускается")
		},
		StopFn: func() error {
			fx.stopped++
			// симуляция: на месте после остановки оказывается новый бинарь
			fx.versions[fx.bin] = fx.versions[fx.staged]
			return nil
		},
		StartFn: func() error {
			fx.started++
			return fx.startErr
		},
		Log: func(msg string) { fx.logs = append(fx.logs, msg) },
	}
}

func TestSwapRejectsWrongArchBeforeStop(t *testing.T) {
	fx := newSwapFixture(t)
	writeFakeELF(t, fx.staged, "mipsle-softfloat", 3<<20)
	_, err := swapEngineBinary(fx.params(true))
	if err == nil || !strings.Contains(err.Error(), "архитектура не подходит") {
		t.Fatalf("ожидался отказ по арх: %v", err)
	}
	if fx.stopped != 0 {
		t.Fatal("движок остановлен при отказе pre-проверки")
	}
}

func TestSwapRejectsTinyFile(t *testing.T) {
	fx := newSwapFixture(t)
	if err := os.WriteFile(fx.staged, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := swapEngineBinary(fx.params(true))
	if err == nil || !strings.Contains(err.Error(), "мал") {
		t.Fatalf("ожидался отказ мелкого файла: %v", err)
	}
	if fx.stopped != 0 {
		t.Fatal("движок остановлен при отказе pre-проверки")
	}
}

func TestSwapRejectsBrokenUploadBeforeStop(t *testing.T) {
	fx := newSwapFixture(t)
	writeFakeELF(t, fx.staged, "amd64", 3<<20)
	fx.verErr[fx.staged] = errors.New("exec format error")
	_, err := swapEngineBinary(fx.params(true))
	if err == nil || !strings.Contains(err.Error(), "не запускается") {
		t.Fatalf("ожидался отказ битого бинаря: %v", err)
	}
	if fx.stopped != 0 {
		t.Fatal("движок остановлен при отказе тест-запуска")
	}
}

func TestSwapSuccess(t *testing.T) {
	fx := newSwapFixture(t)
	writeFakeELF(t, fx.staged, "amd64", 3<<20)
	writeFakeELF(t, fx.bin, "amd64", 3<<20)
	fx.versions[fx.staged] = "1.14.4-lx.30"
	fx.versions[fx.bin] = "1.14.2-lx.12"

	res, err := swapEngineBinary(fx.params(true))
	if err != nil {
		t.Fatalf("замена не удалась: %v\nлог:\n%s", err, res.Log)
	}
	if res.Version != "1.14.4-lx.30" || res.Previous != "1.14.2-lx.12" {
		t.Fatalf("версии в результате: %+v", res)
	}
	if fx.stopped != 1 || fx.started != 1 {
		t.Fatalf("стоп/старт: %d/%d", fx.stopped, fx.started)
	}
	if _, err := os.Stat(fx.bin + ".mawg-bak"); !os.IsNotExist(err) {
		t.Fatal("бэкап не снят после успеха")
	}
	if _, err := os.Stat(fx.staged); !os.IsNotExist(err) {
		t.Fatal("staged не удалён после успеха")
	}
	marker, err := os.ReadFile(filepath.Join(fx.markerDir, ".installed-by-mawg"))
	if err != nil || !strings.Contains(string(marker), "source=upload") {
		t.Fatalf("маркер загрузки не записан: %v %q", err, marker)
	}
}

func TestSwapRollbackOnStartFail(t *testing.T) {
	fx := newSwapFixture(t)
	writeFakeELF(t, fx.staged, "amd64", 35<<20)
	writeFakeELF(t, fx.bin, "amd64", 30<<20)
	fx.versions[fx.staged] = "1.14.4-lx.30"
	fx.versions[fx.bin] = "1.14.2-lx.12"
	fx.startErr = errors.New("clash_api не отвечает")

	res, err := swapEngineBinary(fx.params(true))
	if err == nil || !strings.Contains(err.Error(), "восстановлен") {
		t.Fatalf("ожидался откат с восстановлением: %v\nлог:\n%s", err, res.Log)
	}
	if !res.RolledBack {
		t.Fatal("RolledBack не выставлен")
	}
	if fx.started < 2 {
		t.Fatalf("стартов %d: первый (новый) + повтор после отката", fx.started)
	}
	// прежний бинарь физически на месте (30 МБ, не новый 35 МБ)
	st, serr := os.Stat(fx.bin)
	if serr != nil || st.Size() != 30<<20 {
		t.Fatalf("на месте бинарь %v байт, хочу прежний %d", st.Size(), 30<<20)
	}
}

func TestSwapNoBackupLeavesBroken(t *testing.T) {
	fx := newSwapFixture(t)
	writeFakeELF(t, fx.staged, "amd64", 3<<20)
	writeFakeELF(t, fx.bin, "amd64", 3<<20)
	fx.versions[fx.staged] = "1.14.4-lx.30"
	fx.versions[fx.bin] = "1.14.2-lx.12"
	fx.startErr = errors.New("clash_api не отвечает")

	res, err := swapEngineBinary(fx.params(false))
	if err == nil || !strings.Contains(err.Error(), "залейте рабочий бинарь заново") {
		t.Fatalf("ожидалась честная поломка без бэкапа: %v\nлог:\n%s", err, res.Log)
	}
}

func TestSwapRollbackOnVersionMismatch(t *testing.T) {
	fx := newSwapFixture(t)
	writeFakeELF(t, fx.staged, "amd64", 3<<20)
	writeFakeELF(t, fx.bin, "amd64", 3<<20)
	fx.versions[fx.staged] = "1.14.4-lx.30"
	fx.versions[fx.bin] = "1.14.2-lx.12"
	// после копии на месте отвечает СТАРАЯ версия (битая запись)
	p := fx.params(true)
	p.StopFn = func() error { fx.stopped++; return nil } // без подмены версий
	res, err := swapEngineBinary(p)
	if err == nil || !strings.Contains(err.Error(), "восстановлен") {
		t.Fatalf("ожидался откат по версии: %v\nлог:\n%s", err, res.Log)
	}
}
