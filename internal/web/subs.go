package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"mawg/internal/links"
	"mawg/internal/store"
)

func (s *Server) getSubs(w http.ResponseWriter, r *http.Request) {
	subs := s.store.Subs()
	if subs == nil {
		subs = []store.Sub{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"subs": subs})
}

func (s *Server) postSubs(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source string `json:"source"`
		Name   string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	req.Source = strings.TrimSpace(req.Source)
	if req.Source == "" {
		writeErr(w, fmt.Errorf("пустой источник: дайте URL подписки или ссылку"))
		return
	}
	sub, err := resolveSource(r.Context(), req.Source)
	if err != nil {
		writeErr(w, err)
		return
	}
	name := subName(req.Name, req.Source, sub)
	sub.Name = uniqSubName(s.store, name)
	sub.AddedAt = time.Now()
	if err := s.store.SaveSub(sub); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("subs", sub.Name, fmt.Sprintf("подписка добавлена: %d узлов", len(sub.Nodes)))
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) refreshSub(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	old, ok := s.store.SubByName(name)
	if !ok {
		writeErr(w, fmt.Errorf("подписка %q не найдена", name))
		return
	}
	sub, err := resolveSource(r.Context(), old.Source)
	if err != nil {
		old.Error = err.Error()
		old.RefreshedAt = time.Now()
		if saveErr := s.store.SaveSub(old); saveErr != nil {
			writeErr(w, saveErr)
			return
		}
		s.store.LogEvent("subs", name, "обновление не удалось: "+err.Error())
		writeJSON(w, http.StatusOK, old)
		return
	}
	sub.Name = old.Name
	sub.Source = old.Source
	sub.AddedAt = old.AddedAt
	if err := s.store.SaveSub(sub); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("subs", name, fmt.Sprintf("подписка обновлена: %d узлов", len(sub.Nodes)))
	writeJSON(w, http.StatusOK, sub)
}

func (s *Server) deleteSub(w http.ResponseWriter, r *http.Request) {
	ok, err := s.store.DeleteSub(r.PathValue("name"))
	if err != nil {
		writeErr(w, err)
		return
	}
	if !ok {
		writeErr(w, fmt.Errorf("подписка %q не найдена", r.PathValue("name")))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func resolveSource(ctx context.Context, source string) (store.Sub, error) {
	now := time.Now()
	sub := store.Sub{Source: source, RefreshedAt: now}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		f, err := links.Fetch(ctx, source)
		if err != nil {
			return sub, err
		}
		sub.Info = f.Sub
		sub.Nodes = f.Result.Nodes
		sub.Warnings = f.Result.Warnings
		return sub, nil
	}
	res := links.ParseSubscription("manual", source)
	sub.Nodes = res.Nodes
	sub.Warnings = res.Warnings
	if len(res.Nodes) == 0 {
		detail := "не URL подписки и не ссылка подключения"
		if len(res.Warnings) > 0 {
			detail = res.Warnings[0]
		}
		return sub, fmt.Errorf("%s", detail)
	}
	return sub, nil
}

func subName(want, source string, sub store.Sub) string {
	if want = strings.TrimSpace(want); want != "" {
		return want
	}
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		if u, err := url.Parse(source); err == nil && u.Hostname() != "" {
			return u.Hostname()
		}
		return "sub"
	}
	if len(sub.Nodes) == 1 {
		if _, name, ok := strings.Cut(sub.Nodes[0].Tag, "|"); ok && name != "" {
			return name
		}
	}
	return "paste"
}

func uniqSubName(st *store.Store, name string) string {
	if _, taken := st.SubByName(name); !taken {
		return name
	}
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s-%d", name, i)
		if _, taken := st.SubByName(cand); !taken {
			return cand
		}
	}
}
