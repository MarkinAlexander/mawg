package web

// Хендлеры/логика настроек сервера (из server.go).

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"

	"mawg/internal/auth"
	"mawg/internal/store"
)

func (s *Server) getServerSettings(w http.ResponseWriter, r *http.Request) {
	addr, port, allowed, authOff := s.store.ServerSettings()
	if allowed == nil {
		allowed = []store.IPAllow{}
	}
	clientIp := ""
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		clientIp = host
	} else {
		clientIp = r.RemoteAddr
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"listenAddr":   addr,
		"port":         port,
		"allowedIps":   allowed,
		"authDisabled": authOff,
		"authEnabled":  s.auth == nil || s.auth.Enabled,
		"hasCreds":     s.auth != nil && s.auth.HasCreds(),
		"clientIp":     clientIp,
		"runningAddr":  s.BindAddr,
		"runningPort":  s.BindPort,
	})
}

// lanClients - известные устройства локальной сети: DHCP-аренды (с
// именами, где сервер их ведёт) плюс живые соседи из ARP. Только
// приватные диапазоны - шлюз провайдера и прочий WAN не предлагаются.
func (s *Server) lanClients(w http.ResponseWriter, r *http.Request) {
	type client struct {
		IP   string `json:"ip"`
		MAC  string `json:"mac,omitempty"`
		Name string `json:"name,omitempty"`
	}
	byIP := map[string]client{}
	if data, err := os.ReadFile("/tmp/dhcp.leases"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			f := strings.Fields(line)
			if len(f) < 3 {
				continue
			}
			ip := net.ParseIP(f[2])
			if ip == nil || !ip.IsPrivate() {
				continue
			}
			c := client{IP: f[2], MAC: strings.ToLower(f[1])}
			if len(f) >= 4 && f[3] != "*" {
				c.Name = f[3]
			}
			byIP[f[2]] = c
		}
	}
	if data, err := os.ReadFile("/proc/net/arp"); err == nil {
		for _, line := range strings.Split(string(data), "\n")[1:] {
			f := strings.Fields(line)
			if len(f) < 4 || f[2] == "0x0" || f[3] == "00:00:00:00:00:00" {
				continue
			}
			ip := net.ParseIP(f[0])
			if ip == nil || !ip.IsPrivate() {
				continue
			}
			if _, ok := byIP[f[0]]; ok {
				continue
			}
			byIP[f[0]] = client{IP: f[0], MAC: strings.ToLower(f[3])}
		}
	}
	out := make([]client, 0, len(byIP))
	for _, c := range byIP {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP < out[j].IP })
	writeJSON(w, http.StatusOK, map[string]any{"clients": out})
}

func (s *Server) putServerSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ListenAddr   string          `json:"listenAddr"`
		Port         int             `json:"port"`
		AllowedIPs   []store.IPAllow `json:"allowedIps"`
		AuthDisabled *bool           `json:"authDisabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	curAddr, curPort, curAllowed, curAuthOff := s.store.ServerSettings()
	if req.ListenAddr == "" {
		req.ListenAddr = curAddr
	}
	if req.Port == 0 {
		req.Port = curPort
	}
	if req.AllowedIPs == nil {
		req.AllowedIPs = curAllowed
	}
	authOff := curAuthOff
	if req.AuthDisabled != nil {
		authOff = *req.AuthDisabled
	}
	if !authOff && s.auth != nil && !s.auth.HasCreds() {
		writeErr(w, fmt.Errorf("нет сохранённой учётки: сначала задайте пароль"))
		return
	}
	// не даю вырезать собственный адрес из allowlist - иначе кнопка
	// "сохранить" станет последней, что владелец нажал. Если в новом
	// списке нет адреса клиента - дописываем его сами (виден чипом в
	// панели), доступ не теряется.
	var enabledIPs []string
	for _, e := range req.AllowedIPs {
		if e.On {
			enabledIPs = append(enabledIPs, e.Value)
		}
	}
	gate := auth.NewIPGate(enabledIPs)
	if !gate.Empty() {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil {
			writeErr(w, fmt.Errorf("не удалось разобрать ваш адрес %q - сохранение отклонено", host))
			return
		}
		if !ip.IsLoopback() && !gate.Allowed(ip) && !gate.OwnAddress(ip) {
			req.AllowedIPs = append(req.AllowedIPs, store.IPAllow{Value: ip.String(), On: true})
			enabledIPs = append(enabledIPs, ip.String())
			gate.Set(enabledIPs)
			s.store.LogEvent("settings", "server", "в разрешённые дописан адрес клиента "+ip.String()+" (в новом списке его не было - защита от потери доступа)")
		}
	}
	if err := s.store.SetServerSettings(req.ListenAddr, req.Port, req.AllowedIPs, authOff); err != nil {
		writeErr(w, err)
		return
	}
	if s.IPGate != nil {
		s.IPGate.Set(enabledIPs)
	}
	if s.auth != nil {
		s.auth.Enabled = !authOff
	}
	addrChanged := req.ListenAddr != s.BindAddr || req.Port != s.BindPort
	if addrChanged {
		s.store.LogEvent("settings", "server", "панель перейдёт на "+req.ListenAddr+":"+fmt.Sprint(req.Port)+" после перезапуска демона")
	}
	if authOff != curAuthOff {
		s.store.LogEvent("settings", "server", map[bool]string{true: "авторизация ВЫКЛЮЧЕНА", false: "авторизация включена"}[authOff])
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "needRestart": addrChanged, "allowedIps": req.AllowedIPs})
}

func (s *Server) getRCIToken(w http.ResponseWriter, r *http.Request) {
	// токен наружу не отдаём: это credential локального RCI Keenetic
	writeJSON(w, http.StatusOK, map[string]bool{"configured": s.store.RCIToken() != ""})
}

// getSubHwid - идентификация установки для подписок с лимитом устройств
// (Remnawave x-hwid): ручной (если задан) и текущий эффективный - его
// видно у провайдера в карточке устройств.
func (s *Server) getSubHwid(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"manual":    s.store.Settings().SubHwid,
		"effective": s.store.SubDeviceID(),
	})
}

func (s *Server) putSubHwid(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Hwid string `json:"hwid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	req.Hwid = strings.TrimSpace(req.Hwid)
	if err := s.store.SetSubHwid(req.Hwid); err != nil {
		writeErr(w, err)
		return
	}
	if req.Hwid != "" {
		s.store.LogEvent("settings", "subhwid", "ручной hwid подписок задан: mawg делит слот устройства с приложением провайдера")
	} else {
		s.store.LogEvent("settings", "subhwid", "ручной hwid снят - подписки снова идут от устройства mawg")
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "saved"})
}

func (s *Server) putRCIToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.SetRCIToken(req.Token); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("system", "rcitoken", "токен локального API обновлен")
	writeJSON(w, http.StatusOK, map[string]string{"ok": "saved"})
}
