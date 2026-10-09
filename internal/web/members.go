package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"mawg/internal/links"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

// Плашки протоколов для движковых пулов: что именно в пуле - AmneziaWG
// (2.0/3.x), WireGuard или прокси-узлы с транспортом. Пользователю важно
// видеть состав без лазанья по файлам.

type protoBadge struct {
	Label string `json:"label"`
	Kind  string `json:"kind"` // awg3 | awg | wg | proxy
	Count int    `json:"count"`
}

type memberView struct {
	Name     string `json:"name"`
	Proto    string `json:"proto"`
	Detail   string `json:"detail,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Active   bool   `json:"active,omitempty"`
	Enabled  bool   `json:"enabled"`
}

// wgProtoLabel - тип конфига WG по содержимому (кэш по mtime: статус
// опрашивается каждые 5с, перечитывать файлы незачем).
var (
	wgProtoMu    sync.Mutex
	wgProtoCache = map[string]wgProtoEntry{}
)

type wgProtoEntry struct {
	mtime time.Time
	proto string
}

func wgProtoLabel(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return "wg"
	}
	wgProtoMu.Lock()
	c, ok := wgProtoCache[path]
	wgProtoMu.Unlock()
	if ok && c.mtime.Equal(st.ModTime()) {
		return c.proto
	}
	data, err := os.ReadFile(path)
	proto := "wg"
	if err == nil {
		cfg, err := wgconf.Parse(data)
		if err == nil {
			switch {
			case cfg.NeedsEngine():
				proto = "awg3"
			case cfg.AWG.Present():
				proto = "awg"
			}
		}
	}
	wgProtoMu.Lock()
	wgProtoCache[path] = wgProtoEntry{mtime: st.ModTime(), proto: proto}
	wgProtoMu.Unlock()
	return proto
}

func protoLabelOf(proto string) string {
	switch proto {
	case "awg3":
		return "AmneziaWG 3.x"
	case "awg":
		return "AmneziaWG"
	default:
		return "WireGuard"
	}
}

// poolProtoBadges - сводка для карточки пула.
func (s *Server) poolProtoBadges(p store.Pool) []protoBadge {
	if p.Settings.EngineMode != engineMode {
		return nil
	}
	counts := map[string]*protoBadge{}
	add := func(kind, label string) {
		b, ok := counts[kind]
		if !ok {
			b = &protoBadge{Kind: kind, Label: label}
			counts[kind] = b
		}
		b.Count++
	}
	for _, c := range p.Configs {
		if !c.Enabled {
			continue
		}
		proto := wgProtoLabel(filepath.Join(s.store.PoolDir(p.Name), c.File))
		add(proto, protoLabelOf(proto))
	}
	if nodes, err := s.readPoolNodes(p.Name); err == nil {
		for _, n := range nodes {
			add("proxy", strings.ToUpper(n.Type)+transportBadgeOf(n))
		}
	}
	out := make([]protoBadge, 0, len(counts))
	for _, b := range counts {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

func transportBadgeOf(n links.Node) string {
	switch n.Transport {
	case "", "tcp":
		return "/tcp"
	case "ws":
		return "/ws"
	case "grpc":
		return "/grpc"
	case "xhttp":
		return "/xhttp"
	case "httpupgrade":
		return "/httpupgrade"
	default:
		return "/" + n.Transport
	}
}

// getPoolMembers - состав пула для шестерёнки: конфиги WG и прокси-узлы
// с протоколами, транспортами и адресами.
func (s *Server) getPoolMembers(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, ok := s.store.Pool(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var members []memberView
	st := s.store.State(name)
	for _, c := range p.Configs {
		proto := wgProtoLabel(filepath.Join(s.store.PoolDir(name), c.File))
		members = append(members, memberView{
			Name: c.Original, Proto: protoLabelOf(proto), Endpoint: c.Endpoint,
			Active: c.File == st.ActiveFile, Enabled: c.Enabled,
		})
	}
	if nodes, err := s.readPoolNodes(name); err == nil {
		for _, n := range nodes {
			ep := n.Host
			if n.Port > 0 {
				ep = fmt.Sprintf("%s:%d", n.Host, n.Port)
			}
			members = append(members, memberView{
				Name: n.Tag, Proto: strings.ToUpper(n.Type),
				Detail: strings.TrimPrefix(transportBadgeOf(n), "/"), Endpoint: ep,
				Active: false, Enabled: true,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// getPoolConfigRaw - выданный конфиг пула как текст: юзер может унести
// его в официальный клиент или проверить.
func (s *Server) getPoolConfigRaw(w http.ResponseWriter, r *http.Request) {
	name, file := r.PathValue("name"), r.PathValue("file")
	p, ok := s.store.Pool(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	found := false
	for _, c := range p.Configs {
		if c.File == file {
			found = true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(filepath.Join(s.store.PoolDir(name), file))
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data)
}
