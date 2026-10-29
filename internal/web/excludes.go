package web

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Исключения из обработки (94-док, слово владельца 2026-10-29):
// глобальный список адресов, которые не маркируются ни одной группой
// (RETURN в цепочках MT_) - серверы, вторые роутеры и т.п. ходят напрямую.
// Применение - internal/cascade (SyncExclusions/SyncHooks).

const (
	maxGlobalExcludes = 128
	maxPoolExcludes   = 64
)

func (s *Server) getExcludes(w http.ResponseWriter, r *http.Request) {
	list := s.store.Excludes()
	if list == nil {
		list = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"excludes": list})
}

func (s *Server) putExcludes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Excludes []string `json:"excludes"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req); err != nil {
		writeErr(w, fmt.Errorf("не удалось разобрать список: %v", err))
		return
	}
	list, err := normalizeExcludes(req.Excludes, maxGlobalExcludes)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.SetExcludes(list); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.store.LogEvent("rules", "excludes", exclEventMsg(list))
	s.syncExclusionsNow()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "excludes": list})
}

// normalizeExcludes - канонизация и лимит: CIDR как есть (каноничная
// форма), чистый IP -> /32, IPv6 отклонён (маркировка форка - IPv4),
// дубликаты схлопываются.
func normalizeExcludes(in []string, limit int) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		e := strings.TrimSpace(raw)
		if e == "" {
			continue
		}
		var canon string
		if _, ipnet, err := net.ParseCIDR(e); err == nil {
			canon = ipnet.String()
		} else if ip := net.ParseIP(e); ip != nil {
			canon = ip.String() + "/32"
		} else {
			return nil, fmt.Errorf("не похоже на IP или подсеть: %s", e)
		}
		if strings.Contains(canon, ":") {
			return nil, fmt.Errorf("%s: IPv6 пока не поддерживается (маркировка - IPv4)", e)
		}
		if seen[canon] {
			continue
		}
		if len(out) >= limit {
			return nil, fmt.Errorf("слишком много записей (лимит %d)", limit)
		}
		seen[canon] = true
		out = append(out, canon)
	}
	return out, nil
}

func exclEventMsg(list []string) string {
	if len(list) == 0 {
		return "список исключений очищен"
	}
	return "список исключений применён (" + fmt.Sprint(len(list)) + "): " + strings.Join(list, ", ")
}

// syncExclusionsNow - немедленное применение исключений; тихо, если
// движок/MT ещё не подняты (SyncHooks доведёт при старте).
func (s *Server) syncExclusionsNow() {
	if s.engine == nil {
		return
	}
	s.engine.SyncExclusions()
}

// poolExcludesValid - нормализация исключений пула (nil = не менялось).
func poolExcludesValid(in []string) ([]string, error) {
	return normalizeExcludes(in, maxPoolExcludes)
}
