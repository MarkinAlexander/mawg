package web

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"mawg/internal/magitrickle"
)

// MT-бэкапы: дисковый конфиг и shadow копируются в base/backups перед
// каждой массовой записью (magitrickle.PreSave -> Engine.BackupMagitrickleState)
// и вручную из панели; отсюда их можно скачать и восстановить правила
// обратно в рантайм. Инцидент 2026-10-10 показал цену отсутствия верёвки.
// Рядом лежит mawg-full-*.tar.gz - полный бэкап панели (настройки, пулы,
// каскады, shadow, журнал, конфиг MT), собирается Go-архиватором; рестор -
// распаковка и рестарт панели; делается вручную из панели.

type mtBackupInfo struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"` // shadow | config | full
	Size     int    `json:"size"`
	Modified string `json:"modified"`
}

// имя файла бэкапа строго нашего формата - никаких traversal и чужих
// путей; -NN - суффикс двух записей в одну секунду
var mtBackupNameRe = regexp.MustCompile(`^(mt-(shadow|config)|mawg-full)-\d{8}-\d{6}(-\d{2})?\.(conf|conf\.gz|tar\.gz)$`)

// mawgServiceScript - init-скрипт панели; var ради тестов (на Windows
// его нет - рестор настроек проверяется до записи файла).
var mawgServiceScript = func(keenetic bool) string {
	if keenetic {
		return "/opt/etc/init.d/S99mawg"
	}
	return "/etc/init.d/mawg"
}

func writeErrCode(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func (s *Server) backupPath(file string) string {
	return filepath.Join(s.store.Base(), "backups", file)
}

func (s *Server) mtBackupNow(w http.ResponseWriter, r *http.Request) {
	s.engine.BackupMagitrickleState()
	if err := s.engine.BackupFull(); err != nil {
		writeErrCode(w, http.StatusInternalServerError, err)
		return
	}
	s.store.LogEvent("magitrickle", "backup", "ручной бэкап: конфиг MT, shadow и полный архив панели")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) mtBackupList(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(filepath.Join(s.store.Base(), "backups"))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"backups": []mtBackupInfo{}})
		return
	}
	var out []mtBackupInfo
	for _, e := range entries {
		name := e.Name()
		if !mtBackupNameRe.MatchString(name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		kind := "shadow"
		switch {
		case strings.HasPrefix(name, "mt-config-"):
			kind = "config"
		case strings.HasPrefix(name, "mawg-full-"):
			kind = "full"
		}
		out = append(out, mtBackupInfo{
			Name:     name,
			Kind:     kind,
			Size:     int(info.Size()),
			Modified: info.ModTime().Format("2006-01-02 15:04"),
		})
	}
	if out == nil {
		out = []mtBackupInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": out})
}

func (s *Server) mtBackupGet(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	if !mtBackupNameRe.MatchString(file) {
		writeErrCode(w, http.StatusBadRequest, fmt.Errorf("чужое имя файла бэкапа"))
		return
	}
	data, err := os.ReadFile(s.backupPath(file))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file))
	w.Write(data)
}

func (s *Server) mtRestore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		File string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, err)
		return
	}
	if !mtBackupNameRe.MatchString(req.File) {
		writeErrCode(w, http.StatusBadRequest, fmt.Errorf("чужое имя файла бэкапа"))
		return
	}
	data, err := os.ReadFile(s.backupPath(req.File))
	if err != nil {
		writeErrCode(w, http.StatusNotFound, err)
		return
	}
	// сжатые частичные бэкапы (.conf.gz) распаковываем; старые несжатые
	// .conf (в т.ч. скачанные на ПК до сжатия) работают как раньше
	if strings.HasSuffix(req.File, ".conf.gz") {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			writeErrCode(w, http.StatusBadRequest, fmt.Errorf("бэкап повреждён (не gzip): %v", err))
			return
		}
		out, err := io.ReadAll(zr)
		if err != nil {
			writeErrCode(w, http.StatusBadRequest, fmt.Errorf("бэкап повреждён: %v", err))
			return
		}
		data = out
	}
	switch {
	case strings.HasPrefix(req.File, "mt-shadow-"):
		// восстановление правил штатным путём записи: бэкап приходит в
		// наш формат shadow ({"groups":[...]}), дальше - transformGroups
		// с валидацией, дедупом и контролем усечения
		var dump struct {
			Groups []magitrickle.Group `json:"groups"`
		}
		if json.Unmarshal(data, &dump) != nil || len(dump.Groups) == 0 {
			writeErrCode(w, http.StatusBadRequest, fmt.Errorf("в бэкапе нет групп - восстанавливать нечего"))
			return
		}
		// рестор тоже через самолечение: большой bulk-PUT валится чаще всех
		if err := s.engine.RetryMTWrite("restore", func() error {
			return s.mtClient().ReplaceGroups(r.Context(), dump.Groups)
		}); err != nil {
			writeErrCode(w, http.StatusBadGateway, err)
			return
		}
		s.store.LogEvent("magitrickle", "restore", "правила восстановлены из бэкапа "+req.File)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "groups": len(dump.Groups)})
	case strings.HasPrefix(req.File, "mt-config-"):
		// сырой рестор дискового конфига MT + рестарт демона (как делали
		// руками после инцидента усечения). Рестор перезаписывает конфиг
		// ЦЕЛИКОМ (включая DNS-настройки владельца), поэтому текущее
		// состояние сначала уходит в авто-бэкап - иначе откат возможен
		// только на чужой бэкап (кейс Кинетика, 84-док).
		path := s.backend.MTConfigPath()
		if path == "" {
			writeErrCode(w, http.StatusBadRequest, fmt.Errorf("платформа не знает путь конфига MT"))
			return
		}
		if cur, err := os.ReadFile(path); err == nil {
			// через SaveBackupRotated: сжатие и ретеншен (раньше писался
			// напрямую и не ротировался до следующей авто-копии)
			if _, err := s.store.SaveBackupRotated("mt-config", "conf.gz", string(cur)); err != nil {
				s.store.LogEvent("magitrickle", "restore", "авто-бэкап текущего конфига не удался: "+err.Error())
			}
		}
		tmp := path + ".mawg-restore.tmp"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			writeErrCode(w, http.StatusInternalServerError, err)
			return
		}
		if err := os.Rename(tmp, path); err != nil {
			os.Remove(tmp)
			writeErrCode(w, http.StatusInternalServerError, err)
			return
		}
		if err := s.backend.RestartMagitrickle(); err != nil {
			s.store.LogEvent("magitrickle", "restore", "конфиг восстановлен из "+req.File+", но рестарт демона не удался: "+err.Error())
			writeErrCode(w, http.StatusBadGateway, err)
			return
		}
		s.store.LogEvent("magitrickle", "restore", "конфиг MT восстановлен из "+req.File+", демон перезапущен")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case strings.HasPrefix(req.File, "mawg-full-"):
		// полный бэкап: распаковка tar.gz в base (+ конфиг MT на его место)
		// и рестарт панели (порт/доступ применяются на старте). Скрипт
		// сервиса проверяем ДО записи - нет его, состояние не трогаем.
		if err := restartPanel(s); err != nil {
			writeErrCode(w, http.StatusBadRequest, err)
			return
		}
		files, err := unpackFullBackup(data, s)
		if err != nil {
			writeErrCode(w, http.StatusBadRequest, err)
			return
		}
		for _, f := range files {
			tmp := f.path + ".mawg-restore.tmp"
			if err := os.WriteFile(tmp, f.data, 0o600); err != nil {
				writeErrCode(w, http.StatusInternalServerError, err)
				return
			}
			if err := os.Rename(tmp, f.path); err != nil {
				os.Remove(tmp)
				writeErrCode(w, http.StatusInternalServerError, err)
				return
			}
		}
		s.store.LogEvent("settings", "restore", "полное восстановление из "+req.File+": файлов "+fmt.Sprint(len(files))+", панель перезапускается")
		writeJSON(w, http.StatusOK, map[string]string{"ok": "restarting"})
	default:
		writeErrCode(w, http.StatusBadRequest, fmt.Errorf("неизвестный вид бэкапа"))
	}
}

// unpackFullBackup разбирает tar.gz полного бэкапа: mawg/* -> base,
// mt/* -> на путь конфига MT. Валидация до записи: config.json обязан
// быть и парситься; чужие пути и backups/ внутрь не проходят.
func unpackFullBackup(data []byte, s *Server) ([]struct {
	path string
	data []byte
}, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("бэкап не tar.gz: %w", err)
	}
	tr := tar.NewReader(gz)
	var files []struct {
		path string
		data []byte
	}
	sawConfig := false
	mtPath := s.backend.MTConfigPath()
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("битый архив: %w", err)
		}
		name := path.Clean(hdr.Name)
		var dest string
		switch {
		case strings.HasPrefix(name, "mawg/"):
			rel := strings.TrimPrefix(name, "mawg/")
			if rel == "" || strings.HasPrefix(rel, "backups/") || !filepath.IsLocal(rel) {
				continue
			}
			dest = filepath.Join(s.store.Base(), rel)
		case strings.HasPrefix(name, "mt/") && mtPath != "":
			dest = mtPath
		default:
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("не дочитался %s: %w", name, err)
		}
		if name == filepath.ToSlash(filepath.Join("mawg", "config.json")) {
			var sanity map[string]any
			if json.Unmarshal(data, &sanity) != nil {
				return nil, fmt.Errorf("в архиве не настройки панели (config.json не JSON-объект)")
			}
			sawConfig = true
		}
		files = append(files, struct {
			path string
			data []byte
		}{dest, data})
	}
	if !sawConfig {
		return nil, fmt.Errorf("в архиве нет config.json - это не полный бэкап панели")
	}
	return files, nil
}

// импорт групп из файла (нативный config.mtrickle, наша тень или бэкап
// mt-shadow-*.conf с ПК - формат один и тот же): клиент парсит файл,
// показывает чекбоксы и шлёт выбранные группы; здесь - применение
func (s *Server) mtImportGroups(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode   string              `json:"mode"` // add | replace
		Groups []magitrickle.Group `json:"groups"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrCode(w, http.StatusBadRequest, err)
		return
	}
	if req.Mode != "add" && req.Mode != "replace" {
		writeErrCode(w, http.StatusBadRequest, fmt.Errorf("неизвестный режим импорта %q (add|replace)", req.Mode))
		return
	}
	if len(req.Groups) == 0 {
		writeErrCode(w, http.StatusBadRequest, fmt.Errorf("в импорте нет групп"))
		return
	}
	// импорт всегда создаёт НОВЫЕ группы: ID из файла выбрасываем (MT
	// назначает сам, как у каскадов и нативного импорта), пустой цвет -
	// дефолтный
	var bad []string
	for i := range req.Groups {
		g := &req.Groups[i]
		g.ID = ""
		for j := range g.Rules {
			g.Rules[j].ID = ""
		}
		if g.Color == "" {
			g.Color = "#4a9eff"
		}
		if strings.TrimSpace(g.Name) == "" || strings.TrimSpace(g.Interface) == "" {
			bad = append(bad, fmt.Sprintf("#%d", i+1))
		}
	}
	if len(bad) > 0 {
		writeErrCode(w, http.StatusBadRequest, fmt.Errorf("в файле группы без имени или интерфейса: %s", strings.Join(bad, ", ")))
		return
	}

	if req.Mode == "add" {
		// добавление: append идёт отдельным атомарным методом (в mutate-
		// замыкании срез наружу не выходит), самолечение и хуки каскадов -
		// как у прочих правок панели
		cur, err := s.mtClient().GroupsWithRules(r.Context())
		if err != nil {
			writeErrCode(w, http.StatusBadGateway, err)
			return
		}
		existing := make(map[string]bool, len(cur)+len(s.store.Cascades()))
		for _, g := range cur {
			existing[g.Name] = true
		}
		// имя живого каскада тоже занято: второй ipset-to-link с тем же
		// именем ронял iptables-restore всего батча (корень 500-х)
		for _, c := range s.store.Cascades() {
			existing[c.Group] = true
		}
		var add []magitrickle.Group
		var skipped []string
		added, rules := 0, 0
		for _, g := range req.Groups {
			if existing[g.Name] {
				skipped = append(skipped, g.Name)
				continue
			}
			existing[g.Name] = true // и внутри партии дубли имени не плодим
			add = append(add, g)
			added++
			rules += len(g.Rules)
		}
		if len(add) > 0 {
			// тот же самолечащийся путь записи, что и у прочих правок
			if err := s.engine.RetryMTWrite("panel", func() error {
				return s.mtClient().AddGroups(r.Context(), add)
			}); err != nil {
				writeErrCode(w, http.StatusBadGateway, err)
				return
			}
		}
		note := fmt.Sprintf("импорт групп: добавлено %d (%d правил)", added, rules)
		if len(skipped) > 0 {
			note += ", пропущены дубли имени: " + strings.Join(skipped, ", ")
		}
		s.store.LogEvent("magitrickle", "import", note)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": "add", "added": added, "rules": rules, "skipped": skipped})
		return
	}

	// replace: полный список выбранными группами - тем же путём, что
	// рестор shadow-бэкапа (валидация, дедуп и контроль усечения тени
	// внутри transformGroups)
	if err := s.engine.RetryMTWrite("import", func() error {
		return s.mtClient().ReplaceGroups(r.Context(), req.Groups)
	}); err != nil {
		writeErrCode(w, http.StatusBadGateway, err)
		return
	}
	// дедуп чистит req.Groups на месте - честное число то, что записалось
	rules := countRules(req.Groups)
	s.store.LogEvent("magitrickle", "import", fmt.Sprintf("импорт групп: список заменён (%d групп, %d правил)", len(req.Groups), rules))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": "replace", "added": len(req.Groups), "rules": rules})
}

func countRules(groups []magitrickle.Group) int {
	n := 0
	for _, g := range groups {
		n += len(g.Rules)
	}
	return n
}

// сколько копий каждого вида бэкапа хранить (2..10, слово владельца);
// после смены - немедленная чистка лишних
func (s *Server) getBackupKeep(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"keep": s.store.BackupKeep()})
}

func (s *Server) putBackupKeep(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Keep int `json:"keep"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	if req.Keep < 2 || req.Keep > 10 {
		writeErr(w, fmt.Errorf("сколько копий хранить - целое от 2 до 10"))
		return
	}
	st := s.store.Settings()
	st.BackupKeep = req.Keep
	if err := s.store.SetSettings(st); err != nil {
		writeErr(w, err)
		return
	}
	s.store.EnforceBackupRetention()
	s.store.LogEvent("backup", "settings", fmt.Sprintf("хранить копий каждого вида: %d (лишние удалены)", req.Keep))
	writeJSON(w, http.StatusOK, map[string]any{"keep": req.Keep})
}
