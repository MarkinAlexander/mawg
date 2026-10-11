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
