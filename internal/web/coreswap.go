package web

// Конвейер замены бинаря движка (112-док 4.2): единый для загруженного
// через панель файла и (перспективно) upgrade-in-place из релиза. Шаги:
// pre-проверки БЕЗ остановки движка (ELF+arch, размер, версия тест-запуском,
// место) -> stop -> бэкап -> rm старого (освободить место) -> копия нового
// -> chmod -> version -> start -> проба. Неудача старта/версии - откат на
// бэкап, без бэкапа - честная «движок сломан, залейте бинарь заново».
// Мотивация: на забитых роутерах установка tar.gz ломается на распаковке,
// хотя RAM хватает (слово владельца 04.11).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"mawg/internal/mihomo"
	"mawg/internal/platform"
	"mawg/internal/singbox"
)

// swapMinBinary - порог мусора: бинарь движка весит мегабайты, огрызок
// загрузки или HTML-страница ошибки - килобайты.
const swapMinBinary = 2 << 20

// подмены для тестов
var (
	swapDiskFree = platform.DiskFreeBytes
	swapCopyFile = platform.CopyFile
	swapELFArch  = singbox.ELFArch
	swapDetectArch = singbox.DetectLxArch
	swapRun      = func(argv0 string, args ...string) ([]byte, error) {
		return exec.Command(argv0, args...).Output()
	}
	swapTimeNow = func() string { return time.Now().Format(time.RFC3339) }
	// swapVersionProvider - версия-функция по ядру (подмена в тестах)
	swapVersionProvider = swapCoreVersionFn
	// целевые пути бинарей по платформе (подмена в тестах)
	swapLXTarget = singbox.LXTargetForPlatform
	swapMhTarget = mihomo.TargetForPlatform
)

// swapParams - вход конвейера: колбэки вместо прямых вызовов менеджеров,
// чтобы тестировать на ПК (менеджеры живут только на Linux).
type swapParams struct {
	Core       string // "singbox" | "mihomo"
	Bin        string // целевой путь бинаря
	MarkerDir  string // каталог маркера установки
	Staged     string // загруженный бинарь (tmpfs)
	KeepBackup bool   // сохранить копию старого (<bin>.mawg-bak) для отката
	RouterArch string // подмена арха роутера в тестах; "" = детект

	// VersionFn - версия бинаря движка (sing-box version | mihomo -v)
	VersionFn func(bin string) (string, error)
	// StopFn/StartFn - остановка/запуск движка (менеджер/applyEngine)
	StopFn  func() error
	StartFn func() error
	// Log - события в журнал панели (kind core-swap пишет обёртка)
	Log func(msg string)
}

type swapResult struct {
	Core       string `json:"core"`
	Version    string `json:"version"`
	Previous   string `json:"previous,omitempty"`
	Backup     string `json:"backup,omitempty"`
	RolledBack bool   `json:"rolledBack,omitempty"`
	Log        string `json:"log"`
}

func (r *swapResult) step(format string, a ...any) {
	r.Log += "- " + fmt.Sprintf(format, a...) + "\n"
}

// swapEngineBinary - сам конвейер (логика + fs через подмены).
func swapEngineBinary(p swapParams) (*swapResult, error) {
	res := &swapResult{Core: p.Core}
	if p.Bin == "" || p.Staged == "" || p.VersionFn == nil {
		return res, errors.New("не заданы пути бинаря или версия-функция")
	}
	note := func(format string, a ...any) {
		res.step(format, a...)
		if p.Log != nil {
			p.Log(fmt.Sprintf(format, a...))
		}
	}

	// --- pre-проверки ДО остановки движка: мусор и чужая архитектура
	// отвергаются без простоя
	st, err := os.Stat(p.Staged)
	if err != nil {
		return res, err
	}
	if st.Size() < swapMinBinary {
		return res, fmt.Errorf("файл подозрительно мал (%d байт): бинарь движка весит мегабайты", st.Size())
	}
	binArch, err := swapELFArch(p.Staged)
	if err != nil {
		return res, fmt.Errorf("загруженный файл не годится: %v", err)
	}
	routerArch := p.RouterArch
	if routerArch == "" {
		a, detail, derr := swapDetectArch()
		if derr != nil {
			return res, derr
		}
		routerArch = a
		note("архитектура роутера %s (%s)", a, detail)
	}
	if binArch != routerArch {
		return res, fmt.Errorf("архитектура не подходит: бинарь %s, роутер %s - движок не остановлен, ничего не изменено", binArch, routerArch)
	}
	note("ELF %s совпадает с роутером", binArch)

	// тест-запуск загруженного бинаря прямо из tmpfs: битый исполняемый
	// файл должен отвалиться до остановки движка
	if err := os.Chmod(p.Staged, 0o755); err != nil {
		return res, err
	}
	newVer, err := p.VersionFn(p.Staged)
	if err != nil {
		return res, fmt.Errorf("загруженный бинарь не запускается (%v) - движок не остановлен, ничего не изменено", err)
	}
	res.Version = newVer
	note("тест-запуск загруженного бинаря: версия %s", newVer)

	// --- место: пик = новый (+ бэкап, если сохраняем); старый снимается
	// до копии нового, его место освобождается
	var prevVer string
	var oldSize int64
	if out, verr := p.VersionFn(p.Bin); verr == nil {
		prevVer = out
		res.Previous = prevVer
	}
	if oi, oerr := os.Stat(p.Bin); oerr == nil {
		oldSize = oi.Size()
	}
	binDir := filepath.Dir(p.Bin)
	free := swapDiskFree(binDir)
	need := st.Size()
	avail := free
	if p.KeepBackup {
		need += oldSize
	} else {
		avail += oldSize // старый удаляется до записи нового
	}
	if free > 0 && avail < need {
		if p.KeepBackup {
			return res, fmt.Errorf("не хватает места: нужно %s с копией старого (свободно %s) - повторите без сохранения старого или освободите место", mbSize(need), mbSize(free))
		}
		return res, fmt.Errorf("не хватает места: нужно %s, свободно %s", mbSize(need), mbSize(free))
	}

	// --- остановка движка
	if err := p.StopFn(); err != nil {
		return res, fmt.Errorf("движок не остановился, замена отменена: %v", err)
	}
	note("движок остановлен")

	// rollback - вернуть прежний бинарь (если сохраняли) и поднять движок
	backup := p.Bin + ".mawg-bak"
	rollback := func(reason error) (*swapResult, error) {
		note("откат: %v", reason)
		if p.KeepBackup && oldSize > 0 {
			if _, err := os.Stat(backup); err == nil {
				_ = os.Remove(p.Bin)
				if rerr := os.Rename(backup, p.Bin); rerr == nil {
					note("восстановлен прежний бинарь %s", prevVer)
					if serr := p.StartFn(); serr != nil {
						note("старт после отката не удался: %v", serr)
					}
					res.RolledBack = true
					return res, fmt.Errorf("замена не удалась (%v), прежний бинарь %s восстановлен", reason, prevVer)
				} else {
					note("восстановить бэкап не удалось: %v", rerr)
				}
			}
		}
		res.RolledBack = true
		return res, fmt.Errorf("замена не удалась (%v); прежний бинарь не сохранялся - залейте рабочий бинарь заново кнопкой «Загрузить бинарь»", reason)
	}

	// --- бэкап старого -> rm старого (место) -> копия нового
	_ = os.Remove(backup)
	if p.KeepBackup && oldSize > 0 {
		if err := swapCopyFile(p.Bin, backup, 0o755); err != nil {
			return rollback(fmt.Errorf("сохранить копию старого не удалось: %v", err))
		}
		res.Backup = backup
		note("старый бинарь %s сохранён: %s", prevVer, backup)
	} else if !p.KeepBackup {
		note("копия старого бинаря не сохраняется (выбрано в диалоге)")
	}
	if oldSize > 0 {
		if err := os.Remove(p.Bin); err != nil {
			return rollback(fmt.Errorf("снять старый бинарь не удалось: %v", err))
		}
		note("старый бинарь снят (освобождено %s)", mbSize(oldSize))
	}
	if err := swapCopyFile(p.Staged, p.Bin, 0o755); err != nil {
		return rollback(fmt.Errorf("записать новый бинарь не удалось: %v", err))
	}
	_ = os.Remove(p.Staged)
	if err := os.Chmod(p.Bin, 0o755); err != nil {
		return rollback(fmt.Errorf("права на бинарь: %v", err))
	}
	note("новый бинарь записан: %s", p.Bin)

	// --- версия на месте = ожиданию
	final, err := p.VersionFn(p.Bin)
	if err != nil || final != newVer {
		return rollback(fmt.Errorf("бинарь на месте отвечает %q, ожидалось %q (%v)", final, newVer, err))
	}
	note("версия на месте: %s", final)

	// --- старт
	if err := p.StartFn(); err != nil {
		return rollback(fmt.Errorf("движок с новым бинарём не стартовал: %v", err))
	}
	note("движок стартовал с новым бинарем")

	// успех: маркер установки (source=upload) + снять бэкап
	writeUploadMarker(p.MarkerDir, p.Core, final)
	if p.KeepBackup {
		_ = os.Remove(backup)
		note("бэкап снят после успешного старта")
	}
	return res, nil
}

// writeUploadMarker - маркер .installed-by-mawg для ручной заливки: ядро
// остаётся «нашим» (uninstall/смена источника работают как обычно).
func writeUploadMarker(markerDir, core, version string) {
	if markerDir == "" {
		return
	}
	source := "upload"
	if core == "mihomo" {
		source = "upload-mihomo"
	}
	_ = os.MkdirAll(markerDir, 0o755)
	body := fmt.Sprintf("tag=manual\nversion=%s\nsource=%s\ninstalled=%s\n", version, source, swapTimeNow())
	_ = os.WriteFile(filepath.Join(markerDir, singbox.LXMarkerName), []byte(body), 0o644)
}

func mbSize(n int64) string {
	return fmt.Sprintf("%.0f МБ", float64(n)/float64(1<<20))
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// swapCoreVersionFn - version-функция конвейера для ядра.
func swapCoreVersionFn(core string) func(string) (string, error) {
	return func(bin string) (string, error) {
		if core == "mihomo" {
			return mihomo.MihomoVersion(bin, swapRun)
		}
		return singbox.SingBoxVersion(bin, swapRun)
	}
}

// runCoreSwap - собрать параметры конвейера из живых менеджеров и платформы.
func (s *Server) runCoreSwap(core, staged string, keepBackup bool) (*swapResult, error) {
	var bin, markerDir string
	// killBinProcs - страховка после сервисного стопа: живые процессы с
	// ЗАМЕНЯЕМОГО бинаря (ручные запуски, тестовые из /tmp на живом
	// заменяемом пути) остались бы сиротами с удалённым образом (112-док 4.2)
	killBinProcs := func() int {
		return singbox.KillProcs(singbox.BinProcs(bin))
	}
	stop := func() error { return nil }
	switch core {
	case "mihomo":
		t := swapMhTarget(s.backend.Name())
		bin, markerDir = t.Bin, t.MarkerDir
		stop = func() error {
			// менеджер может быть не создан (ядро не установлено) - тогда
			// останавливать нечего, это не ошибка
			if mgr, err := s.mh(); err == nil {
				mgr.StopOwn()
			}
			killBinProcs()
			return nil
		}
	default:
		core = "singbox"
		t := swapLXTarget(s.backend.Name())
		bin, markerDir = t.Bin, t.MarkerDir
		stop = func() error {
			if s.store.SingboxMode() == "shared" {
				_, init := s.sharedPaths()
				if init == "" || !fileExists(init) {
					// init-скрипта нет (ручная/тестовая установка сервиса):
					// сервисом управлять нечем - гасим процессы с заменяемого
					// бинаря напрямую, чужие с другими бинарями не трогаем
					killBinProcs()
					return nil
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := singbox.ServiceCmd(ctx, init, "stop", nil); err != nil {
					return err
				}
				killBinProcs()
				return nil
			}
			if mgr, err := s.sb(); err == nil {
				mgr.StopOwn()
			}
			killBinProcs()
			return nil
		}
	}
	return swapEngineBinary(swapParams{
		Core: core, Bin: bin, MarkerDir: markerDir, Staged: staged,
		KeepBackup: keepBackup,
		VersionFn: swapVersionProvider(core),
		StopFn:    stop,
		StartFn: func() error {
			// менеджеры перечитывают версию нового бинаря, кэш детекта ядер -
			// тоже; applyEngine поднимает движок и пробует пулы
			s.resetSB()
			s.resetMH()
			s.coreDetectReset()
			_, err := s.applyEngine()
			return err
		},
		Log: func(msg string) { s.store.LogEvent(core, "core-swap", msg) },
	})
}
