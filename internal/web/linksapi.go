package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"mawg/internal/links"
)

func (s *Server) inspectSource(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	req.Source = strings.TrimSpace(req.Source)
	if req.Source == "" {
		writeErr(w, fmt.Errorf("пустой источник"))
		return
	}
	out := struct {
		Error     string   `json:"error,omitempty"`
		Native    int      `json:"native"`
		Engine    int      `json:"engine"`
		Types     []string `json:"types,omitempty"`
		Tags      []string `json:"tags,omitempty"`
		Userinfo  string   `json:"userinfo,omitempty"`
		IntervalH float64  `json:"intervalHours,omitempty"`
		Warnings  []string `json:"warnings,omitempty"`
		Amnezia   *struct {
			ServiceType     string `json:"serviceType"`
			ServiceProtocol string `json:"serviceProtocol"`
		} `json:"amnezia,omitempty"`
	}{}
	if key, ok := links.IsAmneziaKey(req.Source); ok {
		out.Amnezia = &struct {
			ServiceType     string `json:"serviceType"`
			ServiceProtocol string `json:"serviceProtocol"`
		}{strings.TrimPrefix(key.ServiceType, "amnezia-"), key.ServiceProtocol}
		writeJSON(w, http.StatusOK, out)
		return
	}
	res, err := resolveSource(r.Context(), req.Source)
	out.Warnings = res.Warnings
	if err != nil {
		out.Error = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	seen := map[string]bool{}
	for _, n := range res.Nodes {
		if n.Type == "wireguard" || n.Type == "amneziawg" {
			out.Native++
		} else {
			out.Engine++
		}
		if !seen[n.Type] {
			seen[n.Type] = true
			out.Types = append(out.Types, n.Type)
		}
		if len(out.Tags) < 8 {
			out.Tags = append(out.Tags, n.Tag)
		}
	}
	out.Userinfo = res.Info.Userinfo
	out.IntervalH = res.Info.UpdateIntervalHours
	writeJSON(w, http.StatusOK, out)
}
