package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mawg/internal/magitrickle"
)

// MT-бэкапы: дисковый конфиг и shadow копируются в base/backups перед
// каждой массовой записью (magitrickle.PreSave -> Engine.BackupMagitrickleState)
// и вручную из панели; отсюда их можно скачать и восстановить правила
// обратно в рантайм. Инцидент 2026-10-10 показал цену отсутствия верёвки.

type mtBackupInfo struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"` // shadow | config
	Size     int    `json:"size"`
	Modified string `json:"modified"`
}

// имя файла бэкапа строго нашего формата - никаких traversal и чужих
// путей; -NN - суффикс двух записей в одну секунду
var mtBackupNameRe = regexp.MustCompile(`^mt-(shadow|config)-\d{8}-\d{6}(-\d{2})?\.conf$`)

func writeErrCode(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func (s *Server) backupPath(file string) string {
	return filepath.Join(s.store.Base(), "backups", file)
}

func (s *Server) mtBackupNow(w http.ResponseWriter, r *http.Request) {
	s.engine.BackupMagitrickleState()
	s.store.LogEvent("magitrickle", "backup", "ручной бэкап конфига MT и shadow из панели")
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
		if strings.HasPrefix(name, "mt-config-") {
			kind = "config"
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
		if c := s.casc(); c != nil {
			c.BeforeReplace(dump.Groups)
			defer c.AfterMutate()
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
		// руками после инцидента усечения)
		path := s.backend.MTConfigPath()
		if path == "" {
			writeErrCode(w, http.StatusBadRequest, fmt.Errorf("платформа не знает путь конфига MT"))
			return
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
	default:
		writeErrCode(w, http.StatusBadRequest, fmt.Errorf("неизвестный вид бэкапа"))
	}
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
			if c := s.casc(); c != nil {
				// хуки снимаются по драфт-мутации: чистое добавление
				// существующие каскадные группы не трогает - снимать нечего
				c.BeforeMutate(func([]magitrickle.Group) bool { return false })
				defer c.AfterMutate()
			}
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
	if c := s.casc(); c != nil {
		c.BeforeReplace(req.Groups)
		defer c.AfterMutate()
	}
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
