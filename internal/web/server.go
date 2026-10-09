package web

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mawg/internal/auth"
	"mawg/internal/cascade"
	"mawg/internal/magitrickle"
	"mawg/internal/platform"
	"mawg/internal/platform/keenetic"
	"mawg/internal/provision"
	"mawg/internal/rotator"
	"mawg/internal/selfupdate"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

//go:embed ui
var uiFS embed.FS

type Server struct {
	store      *store.Store
	engine     *rotator.Engine
	backend    platform.Backend
	mt         *magitrickle.Client
	version    string
	auth       *auth.Auth
	tunMu      sync.Mutex
	mtCacheMu  sync.Mutex
	mtCache    *mtSnapshot
	ifaceCache respCache
	sysCache   respCache
	IPGate     *auth.IPGate
	// фактически забинденные адрес/порт демона: UI отличает
	// "сохранено, но не перезапущено" от "уже применяется"
	BindAddr string
	BindPort int
}

func New(st *store.Store, e *rotator.Engine, b platform.Backend, mt *magitrickle.Client, version string, a *auth.Auth) *Server {
	return &Server{store: st, engine: e, backend: b, mt: mt, version: version, auth: a}
}

func (s *Server) casc() *cascade.Manager {
	if s.engine == nil {
		return nil
	}
	return s.engine.Cascade()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/status", s.getStatus)
	mux.HandleFunc("GET /api/v1/slots", s.getSlots)
	mux.HandleFunc("POST /api/v1/slots/create", s.createSlot)
	mux.HandleFunc("GET /api/v1/ifaces", s.getIfaces)
	mux.HandleFunc("POST /api/v1/ifaces/{device}/mode", s.setIfaceMode)
	mux.HandleFunc("PUT /api/v1/ifaces/{device}/probe", s.setIfaceProbe)
	mux.HandleFunc("GET /api/v1/wanprobe", s.getWANProbe)
	mux.HandleFunc("PUT /api/v1/wanprobe", s.putWANProbe)
	mux.HandleFunc("GET /api/v1/rcitoken", s.getRCIToken)
	mux.HandleFunc("PUT /api/v1/rcitoken", s.putRCIToken)
	mux.HandleFunc("POST /api/v1/pools/{name}/rename", s.renamePool)
	mux.HandleFunc("GET /api/v1/events", s.getEvents)
	mux.HandleFunc("POST /api/v1/pools", s.createPool)
	mux.HandleFunc("POST /api/v1/pools/amnezia-free", s.createFreePool)
	mux.HandleFunc("POST /api/v1/pools/from-source", s.createPoolFromSource)
	mux.HandleFunc("POST /api/v1/links/inspect", s.inspectSource)
	mux.HandleFunc("POST /api/v1/singbox/mode", s.postSingboxMode)
	mux.HandleFunc("POST /api/v1/singbox/cache-file", s.postCacheFile)
	mux.HandleFunc("POST /api/v1/pools/{name}/refresh-source", s.refreshSource)
	mux.HandleFunc("PUT /api/v1/pools/{name}", s.updatePool)
	mux.HandleFunc("DELETE /api/v1/pools/{name}", s.deletePool)
	mux.HandleFunc("POST /api/v1/pools/{name}/configs", s.uploadConfigs)
	mux.HandleFunc("GET /api/v1/pools/{name}/premium", s.getPremium)
	mux.HandleFunc("POST /api/v1/pools/{name}/premium", s.importPremium)
	mux.HandleFunc("POST /api/v1/pools/{name}/premium/country", s.switchPremium)
	mux.HandleFunc("DELETE /api/v1/pools/{name}/configs/{file}", s.deleteConfig)
	mux.HandleFunc("POST /api/v1/pools/{name}/configs/{file}/enable", s.enableConfig)
	mux.HandleFunc("POST /api/v1/pools/{name}/configs/{file}/move", s.moveConfig)
	mux.HandleFunc("POST /api/v1/pools/{name}/rotate", s.postRotate)
	mux.HandleFunc("POST /api/v1/pools/{name}/check", s.postCheck)
	mux.HandleFunc("POST /api/v1/pools/{name}/enable", s.postEnable)
	mux.HandleFunc("POST /api/v1/pools/{name}/disable", s.postDisable)
	mux.HandleFunc("POST /api/v1/pools/{name}/activate", s.postActivate)
	mux.HandleFunc("GET /api/v1/magitrickle", s.getMagitrickle)
	mux.HandleFunc("GET /api/v1/system/check", s.getSystemCheck)
	mux.HandleFunc("POST /api/v1/system/install", s.postSystemInstall)

	mux.HandleFunc("GET /api/v1/bundles", s.getBundles)
	mux.HandleFunc("POST /api/v1/bundles", s.createBundle)
	mux.HandleFunc("PUT /api/v1/bundles/{name}", s.updateBundle)
	mux.HandleFunc("DELETE /api/v1/bundles/{name}", s.deleteBundle)

	mux.HandleFunc("GET /api/v1/mt/groups", s.mtGetGroups)
	mux.HandleFunc("GET /api/v1/mt/duplicates", s.mtDuplicates)
	mux.HandleFunc("GET /api/v1/mt/interfaces", s.mtGetInterfaces)
	mux.HandleFunc("GET /api/v1/mt/presets", s.mtGetPresets)
	mux.HandleFunc("POST /api/v1/mt/groups", s.mtCreateGroup)
	mux.HandleFunc("PUT /api/v1/mt/groups/{id}", s.mtUpdateGroup)
	mux.HandleFunc("POST /api/v1/mt/groups/order", s.mtGroupsOrder)
	mux.HandleFunc("POST /api/v1/mt/groups/{id}/rules/order", s.mtRulesOrder)
	mux.HandleFunc("POST /api/v1/mt/groups/{id}/enable", s.mtToggleGroup)
	mux.HandleFunc("DELETE /api/v1/mt/groups/{id}", s.mtDeleteGroup)
	mux.HandleFunc("POST /api/v1/mt/groups/{id}/rules", s.mtCreateRule)
	mux.HandleFunc("POST /api/v1/mt/groups/{id}/rules/import", s.mtImportRules)
	mux.HandleFunc("PUT /api/v1/mt/groups/{id}/rules/{rid}", s.mtUpdateRule)
	mux.HandleFunc("POST /api/v1/mt/groups/{id}/rules/{rid}/enable", s.mtToggleRule)
	mux.HandleFunc("DELETE /api/v1/mt/groups/{id}/rules/{rid}", s.mtDeleteRule)
	mux.HandleFunc("POST /api/v1/mt/presets/{id}/apply", s.mtApplyPreset)
	mux.HandleFunc("GET /api/v1/settings/server", s.getServerSettings)
	mux.HandleFunc("GET /api/v1/lan/clients", s.lanClients)
	mux.HandleFunc("PUT /api/v1/settings/server", s.putServerSettings)
	mux.HandleFunc("GET /api/v1/system/update/check", s.updateCheck)
	mux.HandleFunc("POST /api/v1/system/update/run", s.updateRun)
	mux.HandleFunc("POST /api/v1/system/restart", s.postSystemRestart)
	mux.HandleFunc("GET /api/v1/cascades", s.cascList)
	mux.HandleFunc("POST /api/v1/cascades", s.cascCreate)
	mux.HandleFunc("DELETE /api/v1/cascades/{group}", s.cascDelete)
	mux.HandleFunc("PUT /api/v1/mt/groups/{id}/policy", s.mtSetPolicy)
	mux.HandleFunc("GET /api/v1/mt/policy-cycles", s.getPolicyCycles)
	mux.HandleFunc("PUT /api/v1/mt/policy-cycles", s.putPolicyCycles)

	mux.HandleFunc("GET /api/v1/subs", s.getSubs)
	mux.HandleFunc("POST /api/v1/subs", s.postSubs)
	mux.HandleFunc("POST /api/v1/subs/{name}/refresh", s.refreshSub)
	mux.HandleFunc("DELETE /api/v1/subs/{name}", s.deleteSub)

	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		panic(err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	// новая панель (Vue, исходники в ui-src) собирается в ui/app командой
	// `npm run build`; старая панель остаётся на /. Имя ui/app, а не dist -
	// go:embed не встраивает каталоги из .gitignore (там занят dist/).
	appSub, err := fs.Sub(uiFS, "ui/app")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /app", http.RedirectHandler("/app/", http.StatusMovedPermanently))
	mux.Handle("GET /app/", http.StripPrefix("/app/", http.FileServer(http.FS(appSub))))
	if s.auth != nil {
		mux.HandleFunc("POST /api/v1/auth/login", s.auth.HandleLogin)
		mux.HandleFunc("POST /api/v1/auth/logout", s.auth.HandleLogout)
		mux.HandleFunc("POST /api/v1/auth/password", s.auth.HandlePassword)
	}
	// авторизация снаружи, лимит тела внутри (статика без лимита не нужна -
	// там нет тела); allowlist режет всё самым внешним слоем
	var h http.Handler = s.auth.Middleware(limitBody(mux))
	if s.IPGate != nil {
		h = s.IPGate.Middleware(h)
	}
	return h
}

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
	// "сохранить" станет последней, что владелец нажал
	var enabledIPs []string
	for _, e := range req.AllowedIPs {
		if e.On {
			enabledIPs = append(enabledIPs, e.Value)
		}
	}
	if gate := auth.NewIPGate(enabledIPs); !gate.Empty() {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		ip := net.ParseIP(host)
		if ip == nil || (!ip.IsLoopback() && !gate.Allowed(ip) && !gate.OwnAddress(ip)) {
			writeErr(w, fmt.Errorf("в новом списке нет вашего адреса %s - сохранение заблокировано, чтобы не потерять доступ к панели", host))
			return
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
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "needRestart": addrChanged})
}

func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, selfupdate.Check(r.Context(), s.version))
}

func (s *Server) updateRun(w http.ResponseWriter, r *http.Request) {
	info := selfupdate.Check(r.Context(), s.version)
	if info.Error != "" {
		writeErr(w, fmt.Errorf("%s", info.Error))
		return
	}
	if !info.Update {
		writeErr(w, fmt.Errorf("обновлений нет (текущая %s, последняя %s)", info.Current, info.Latest))
		return
	}
	s.store.LogEvent("system", "update", "запуск обновления до "+info.Latest)
	go func() {
		if err := selfupdate.Run(context.Background()); err != nil {
			s.store.LogEvent("system", "update", "обновление не удалось: "+err.Error())
		}
	}()
	writeJSON(w, http.StatusOK, map[string]string{"ok": "started", "to": info.Latest})
}

// postSystemRestart перезапускает сервис: ответ уходит раньше, чем
// демон снимется.
func (s *Server) postSystemRestart(w http.ResponseWriter, r *http.Request) {
	script := "/etc/init.d/mawg"
	if s.backend.Name() == store.PlatformKeenetic {
		script = "/opt/etc/init.d/S99mawg"
	}
	if _, err := os.Stat(script); err != nil {
		writeErr(w, fmt.Errorf("init-скрипт не найден: %s", script))
		return
	}
	s.store.LogEvent("settings", "server", "перезапуск панели из веб-интерфейса")
	go func() {
		time.Sleep(700 * time.Millisecond)
		cmd := exec.Command(script, "restart")
		cmd.Stdout = nil
		cmd.Stderr = nil
		cmd.Run()
	}()
	writeJSON(w, http.StatusOK, map[string]string{"ok": "restarting"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

// respCache - короткий кэш готовых JSON-ответов тяжёлых GET-эндпоинтов:
// вкладки панели опрашивают их по интервалу, строить ответ заново на
// каждый запрос на softfloat-MIPS дорого.
type respCache struct {
	mu   sync.Mutex
	at   time.Time
	body []byte
}

func (c *respCache) serve(w http.ResponseWriter, ttl time.Duration, build func() ([]byte, error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.body != nil && time.Since(c.at) < ttl {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(c.body)
		return
	}
	body, err := build()
	if err != nil {
		writeErr(w, err)
		return
	}
	c.at, c.body = time.Now(), body
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(body)
}

// reset - выкинуть кэш (после установки компонента ответ должен стать свежим).
func (c *respCache) reset() {
	c.mu.Lock()
	c.body = nil
	c.mu.Unlock()
}

func (s *Server) getStatus(w http.ResponseWriter, r *http.Request) {
	pools := s.store.Pools()
	type poolView struct {
		Name           string             `json:"name"`
		Platform       string             `json:"platform"`
		Device         string             `json:"device"`
		Slot           string             `json:"slot,omitempty"`
		Mode           string             `json:"mode"`
		ActiveFile     string             `json:"activeFile"`
		ActiveEndpoint string             `json:"activeEndpoint"`
		LastResult     string             `json:"lastResult"`
		LastError      string             `json:"lastError"`
		Rotations      int                `json:"rotations"`
		Disabled       bool               `json:"disabled"`
		ConsecFails    int                `json:"consecFails"`
		RefreshFails   int                `json:"refreshFails,omitempty"`
		LastRefreshErr string             `json:"lastRefreshErr,omitempty"`
		SubRefreshAt   time.Time          `json:"subRefreshAt,omitempty"`
		Settings       store.PoolSettings `json:"settings"`
		Configs        []configView       `json:"configs"`
		Premium        store.PremiumView  `json:"premium"`
	}
	out := struct {
		Version     string         `json:"version"`
		Platform    string         `json:"platform"`
		BackendName string         `json:"backendName"`
		AuthEnabled bool           `json:"authEnabled"`
		Pools       []poolView     `json:"pools"`
		Engine      map[string]any `json:"engine,omitempty"`
	}{Version: s.version, Platform: s.backend.Name(), BackendName: s.backend.Name(),
		AuthEnabled: s.auth == nil || s.auth.Enabled, Pools: []poolView{}}

	for _, p := range pools {
		st := s.store.State(p.Name)
		view := poolView{
			Name: p.Name, Platform: p.Settings.Platform, Device: p.DeviceName(),
			Slot: p.Settings.KeeneticSlot, Mode: st.Mode, ActiveFile: st.ActiveFile,
			LastResult: st.LastResult, LastError: st.LastError,
			Rotations: st.Rotations, Disabled: p.Disabled, ConsecFails: st.ConsecFails, Settings: p.Settings,
			RefreshFails: st.RefreshFails, LastRefreshErr: st.LastRefreshErr, SubRefreshAt: st.SubRefreshAt,
			Configs: []configView{},
			Premium: s.store.PremiumView(p.Name),
		}
		if p.Settings.EngineMode == engineMode {
			switch {
			case func() bool { _, err := s.sb(); return err != nil }():
				view.Mode, view.LastResult = "fallback", "движок недоступен"
			default:
				if mgr, err := s.sb(); err == nil {
					if ps, ok := mgr.PoolStatus(p.Name); ok {
						switch {
						case ps.Reason == "waits-lx":
							view.Mode, view.LastResult = "fallback", "узлы ждут lx-ядро: "+ps.Detail
						case ps.CheckedAt.IsZero():
							view.Mode, view.LastResult = "fallback", "проба ещё не выполнялась"
						case ps.ProbeOK:
							view.Mode = "up"
							view.LastResult = fmt.Sprintf("ok (http %dms)", ps.ProbeMs)
						default:
							view.Mode, view.LastResult = "fallback", "проба не прошла: "+ps.ProbeErr
						}
					} else {
						view.Mode, view.LastResult = "fallback", "движок не применял конфиг"
					}
				}
			}
		}
		now := time.Now().Unix()
		for _, c := range p.Configs {
			cv := configView{File: c.File, Original: c.Original, Endpoint: c.Endpoint, Enabled: c.Enabled}
			if until, ok := st.Cooldowns[c.File]; ok {
				cv.CoolFor = until - now
			}
			cv.Conflict = s.configConflict(p.Name, p.DeviceName(), s.poolConfigAddrs(p.Name, c))
			if c.File == st.ActiveFile {
				view.ActiveEndpoint = c.Endpoint
			}
			view.Configs = append(view.Configs, cv)
		}
		out.Pools = append(out.Pools, view)
	}
	if mgr, err := s.sb(); err == nil {
		st := mgr.Status()
		var tuns []map[string]any
		for _, p := range s.enginePools() {
			nodes := 0
			if ns, err := s.readPoolNodes(p.Name); err == nil {
				nodes = len(ns)
			}
			idx := 1
			if n, err := strconv.Atoi(strings.TrimPrefix(p.Settings.TunName, "tun")); err == nil && n > 0 {
				idx = n
			}
			entry := map[string]any{
				"pool": p.Name, "tun": p.Settings.TunName,
				"disabled": p.Disabled, "nodes": nodes,
				"probePort": st.Mixed + 1 + idx - 1,
			}
			if ps, ok := mgr.PoolStatus(p.Name); ok {
				entry["probeOk"] = ps.ProbeOK
				entry["probeMs"] = ps.ProbeMs
				entry["probeErr"] = ps.ProbeErr
				entry["reason"] = ps.Reason
			}
			tuns = append(tuns, entry)
		}
		out.Engine = map[string]any{
			"available": true, "running": st.Running, "version": st.Version,
			"lx": st.LX, "bin": st.Bin, "mixedPort": st.Mixed, "clashPort": st.Clash,
			"mode": st.Mode, "fragment": st.Fragment, "cacheFile": st.CacheFile, "tuns": tuns,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type configView struct {
	File     string `json:"file"`
	Original string `json:"original"`
	Endpoint string `json:"endpoint"`
	Enabled  bool   `json:"enabled"`
	CoolFor  int64  `json:"coolForSec,omitempty"`
	Conflict string `json:"conflict,omitempty"`
}

// configConflict: первый занятый адрес конфига (не считая собственного
// интерфейса пула) с описанием, пусто если конфликтов нет.
func normAddr(a string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(a), "/", 2)[0])
}

func (s *Server) configConflict(poolName, device string, addrs []string) string {
	if len(addrs) == 0 {
		return ""
	}
	for addr, dev := range s.engine.TunnelAddresses() {
		if dev == device || dev == "" {
			continue
		}
		for _, a := range addrs {
			if normAddr(a) == normAddr(addr) {
				return fmt.Sprintf("адрес %s уже занят интерфейсом %s", a, dev)
			}
		}
	}
	return ""
}

// poolConfigAddrs: адреса конфига из реестра, старые записи добираются из
// файла и запоминаются.
func (s *Server) poolConfigAddrs(poolName string, cfg store.ManagedConfig) []string {
	if len(cfg.Addresses) > 0 {
		return cfg.Addresses
	}
	parsed, err := s.store.LoadConfigFile(poolName, cfg.File)
	if err != nil {
		return nil
	}
	s.store.SetConfigAddresses(poolName, cfg.File, parsed.Addresses)
	return parsed.Addresses
}

// liveAddressConflict: конфликт по свежим адресам интерфейсов (для ручных
// действий: включение тумблером, активация).
func (s *Server) liveAddressConflict(poolName, file string) error {
	pool, ok := s.store.Pool(poolName)
	if !ok {
		return nil
	}
	cfg, err := s.store.LoadConfigFile(poolName, file)
	if err != nil {
		return nil
	}
	tunnels, err := s.backend.SysTunnels()
	if err != nil {
		return nil
	}
	own := pool.DeviceName()
	for _, a := range cfg.Addresses {
		for _, sl := range tunnels {
			if sl.Device == own || sl.Address == "" {
				continue
			}
			if normAddr(sl.Address) == normAddr(a) {
				return fmt.Errorf("адрес %s из конфига уже занят интерфейсом %s: отключите его или выберите другой конфиг", a, sl.Device)
			}
		}
	}
	return nil
}

func (s *Server) getSlots(w http.ResponseWriter, r *http.Request) {
	slots, err := s.backend.Slots()
	if err != nil {
		writeErr(w, err)
		return
	}
	managed := map[string]bool{}
	for _, p := range s.store.Pools() {
		managed[p.DeviceName()] = true
	}
	for i := range slots {
		slots[i].Managed = managed[slots[i].Device]
	}
	writeJSON(w, http.StatusOK, map[string]any{"slots": slots})
}

func (s *Server) getEvents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"events": s.store.Events()})
}

type ifaceView struct {
	Device       string             `json:"device"`
	Slot         string             `json:"slot,omitempty"`
	Description  string             `json:"description,omitempty"`
	Mode         string             `json:"mode"`
	Pool         string             `json:"pool,omitempty"`
	LinkUp       bool               `json:"linkUp"`
	Connected    bool               `json:"connected"`
	HandshakeAgo int                `json:"handshakeAgo"`
	Probe        *store.ProbeConfig `json:"probe,omitempty"`
	ProbeStatus  string             `json:"probeStatus,omitempty"`
}

func (s *Server) allIfaces(ctx context.Context) []platform.SlotInfo {
	slots, err := s.backend.Slots()
	if err != nil {
		// RCI может не ответить: панель не должна пустовать, добираем
		// интерфейсы из туннелей системы, реестра и групп magitrickle
		slots = nil
	}
	seen := map[string]bool{}
	for _, sl := range slots {
		seen[sl.Device] = true
	}
	if tunnels, err := s.backend.SysTunnels(); err == nil {
		for _, sl := range tunnels {
			if !seen[sl.Device] {
				slots = append(slots, sl)
				seen[sl.Device] = true
			}
		}
	}
	for _, p := range s.store.Pools() {
		dev := p.DeviceName()
		if dev != "" && !seen[dev] {
			slots = append(slots, platform.SlotInfo{Device: dev, Description: p.Name})
			seen[dev] = true
		}
	}
	if groups, err := s.mtClient().GroupsWithRules(ctx); err == nil {
		for _, g := range groups {
			if g.Interface == "" || seen[g.Interface] {
				continue
			}
			slots = append(slots, platform.SlotInfo{Device: g.Interface})
			seen[g.Interface] = true
		}
	}
	return slots
}

func (s *Server) getIfaces(w http.ResponseWriter, r *http.Request) {
	s.ifaceCache.serve(w, 10*time.Second, func() ([]byte, error) { return s.buildIfaces(r) })
}

func (s *Server) buildIfaces(r *http.Request) ([]byte, error) {
	slots := s.allIfaces(r.Context())
	if slots == nil {
		return nil, fmt.Errorf("не удалось получить список интерфейсов")
	}
	poolByDevice := map[string]string{}
	for _, p := range s.store.Pools() {
		poolByDevice[p.DeviceName()] = p.Name
	}
	modes := s.store.IfaceModes()
	out := make([]ifaceView, 0, len(slots))
	for _, sl := range slots {
		v := ifaceView{
			Device: sl.Device, Slot: sl.ID, Description: sl.Description,
			LinkUp: sl.LinkUp, Connected: sl.Connected, HandshakeAgo: -1,
		}
		if pool, ok := poolByDevice[sl.Device]; ok {
			v.Mode = store.IfaceManaged
			v.Pool = pool
		} else if m := modes[sl.Device]; m == store.IfaceExternal {
			v.Mode = store.IfaceExternal
		} else {
			v.Mode = store.IfaceHidden
		}
		if v.Mode != store.IfaceManaged {
			v.HandshakeAgo = s.backend.IfaceHandshake(sl.Device)
			v.Probe = s.store.IfaceProbe(sl.Device)
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })

	for i := range out {
		if out[i].Mode == store.IfaceExternal && out[i].Probe != nil {
			out[i].ProbeStatus = s.engine.DeviceProbeStatus(out[i].Device)
		}
	}
	return json.Marshal(map[string]any{"interfaces": out})
}

func (s *Server) setIfaceMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	device := r.PathValue("device")
	known := false
	for _, sl := range s.allIfaces(r.Context()) {
		if sl.Device == device {
			known = true
			break
		}
	}
	if !known {
		writeErr(w, fmt.Errorf("интерфейс %s не найден в системе", device))
		return
	}
	if err := s.store.SetIfaceMode(device, req.Mode); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("ifaces", "mode", device+" -> "+req.Mode)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "set"})
}

func parseProbeBody(r *http.Request) (*store.ProbeConfig, error) {
	var req store.ProbeConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, err
	}
	if req.Target == "" {
		return nil, nil
	}
	if !store.ValidProbeTarget(req.Target) {
		return nil, fmt.Errorf("цель пробы должна быть публичным IPv4 или http(s) URL")
	}
	norm := req.Normalized()
	return &norm, nil
}

func (s *Server) setIfaceProbe(w http.ResponseWriter, r *http.Request) {
	probe, err := parseProbeBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	device := r.PathValue("device")
	if probe != nil {
		for _, pool := range s.store.Pools() {
			if pool.DeviceName() == device {
				writeErr(w, fmt.Errorf("интерфейс %s управляется пулом %q, проба настраивается в настройках пула", device, pool.Name))
				return
			}
		}
	}
	if err := s.store.SetIfaceProbe(device, probe); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("ifaces", "probe", device+" probe "+map[bool]string{true: "set", false: "cleared"}[probe != nil])
	if probe != nil {
		s.engine.InvalidateProbeStatus(device)
	}
	s.engine.CheckBundlesNow()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "probe": probe})
}

func (s *Server) getRCIToken(w http.ResponseWriter, r *http.Request) {
	// токен наружу не отдаём: это credential локального RCI Keenetic
	writeJSON(w, http.StatusOK, map[string]bool{"configured": s.store.RCIToken() != ""})
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

func (s *Server) getWANProbe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": s.store.WANProbeEnabled(),
		"probe":   s.store.WANProbe(),
	})
}

func (s *Server) putWANProbe(w http.ResponseWriter, r *http.Request) {
	probe, err := parseProbeBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.SetWANProbe(probe); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("system", "wanprobe", "проверка канала провайдера "+map[bool]string{true: "включена", false: "выключена"}[probe != nil])
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "enabled": probe != nil})
}

func (s *Server) renamePool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeErr(w, fmt.Errorf("нужно имя"))
		return
	}
	old := r.PathValue("name")
	if s.backend.Name() != store.PlatformKeenetic {
		writeErr(w, fmt.Errorf("переименование на этой платформе не поддерживается (имя = имя системного интерфейса)"))
		return
	}
	pool, ok := s.store.Pool(old)
	if !ok {
		http.NotFound(w, r)
		return
	}
	renamed, err := s.engine.RenamePool(old, req.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	if kb, ok := s.backend.(*keenetic.Backend); ok && pool.Settings.KeeneticSlot != "" {
		kb.Ndmc("interface " + pool.Settings.KeeneticSlot + " description " + renamed.Name)
		kb.Ndmc("system configuration save")
	}
	s.store.LogEvent(renamed.Name, "rename", "пул переименован из "+old)
	s.engine.CheckNow(renamed.Name)
	writeJSON(w, http.StatusOK, renamed)
}

func (s *Server) bundleMembersResolve(members []string) (map[string]string, error) {
	out := map[string]string{}
	for _, m := range members {
		if _, ok := out[m]; ok {
			return nil, fmt.Errorf("участник %s повторяется", m)
		}
		if p, ok := s.store.Pool(m); ok {
			out[m] = p.DeviceName()
			continue
		}
		known := false
		if slots, err := s.backend.Slots(); err == nil {
			for _, sl := range slots {
				if sl.Device == m {
					known = true
					break
				}
			}
		}
		if !known {
			return nil, fmt.Errorf("участник %s не найден (нет такого пула или интерфейса)", m)
		}
		out[m] = m
	}
	return out, nil
}

type bundleView struct {
	store.Bundle
	CurrentMember string            `json:"currentMember,omitempty"`
	MemberDevices map[string]string `json:"memberDevices"`
}

func (s *Server) bundleToView(b store.Bundle) bundleView {
	v := bundleView{Bundle: b, MemberDevices: map[string]string{}, CurrentMember: s.store.BundleState(b.Name).Member}
	devices, err := s.bundleMembersResolve(b.Members)
	if err == nil {
		v.MemberDevices = devices
	}
	return v
}

func (s *Server) getBundles(w http.ResponseWriter, r *http.Request) {
	out := []bundleView{}
	for _, b := range s.store.Bundles() {
		out = append(out, s.bundleToView(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{"bundles": out})
}

func (s *Server) createBundle(w http.ResponseWriter, r *http.Request) {
	var req store.Bundle
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		writeErr(w, fmt.Errorf("нужно имя"))
		return
	}
	if _, err := s.bundleMembersResolve(req.Members); err != nil {
		writeErr(w, err)
		return
	}
	b, err := s.store.CreateBundle(req)
	if err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("bundle:"+b.Name, "create", "создан набор, участников: "+fmt.Sprint(len(b.Members)))
	s.engine.CheckBundlesNow()
	writeJSON(w, http.StatusOK, s.bundleToView(b))
}

func (s *Server) updateBundle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req store.Bundle
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	if _, err := s.bundleMembersResolve(req.Members); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.UpdateBundle(name, req); err != nil {
		writeErr(w, err)
		return
	}
	b, _ := s.store.Bundle(name)
	s.store.LogEvent("bundle:"+name, "update", "изменён набор")
	s.engine.CheckBundlesNow()
	writeJSON(w, http.StatusOK, s.bundleToView(b))
}

func (s *Server) deleteBundle(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteBundle(r.PathValue("name")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}

func (s *Server) validFallback(name, fallback string) error {
	if fallback == "" || fallback == store.FallbackDirect || fallback == store.FallbackHold {
		return nil
	}
	fbName, ok := store.FallbackPoolName(fallback)
	if !ok {
		return fmt.Errorf("фоллбек должен быть direct, hold или pool:<имя пула>")
	}
	if fbName == name {
		return fmt.Errorf("фоллбек на самого себя")
	}
	seen := map[string]bool{name: true}
	cur := fbName
	for i := 0; i < 16; i++ {
		if seen[cur] {
			return fmt.Errorf("цикл фоллбеков через пул %s", cur)
		}
		seen[cur] = true
		p, ok := s.store.Pool(cur)
		if !ok {
			return fmt.Errorf("пул %q не найден", cur)
		}
		next, ok := store.FallbackPoolName(p.Settings.Fallback)
		if !ok {
			return nil
		}
		cur = next
	}
	return fmt.Errorf("слишком длинная цепочка фоллбеков")
}

func (s *Server) createSlot(w http.ResponseWriter, r *http.Request) {
	kb, ok := s.backend.(*keenetic.Backend)
	if !ok {
		writeErr(w, fmt.Errorf("создание слотов доступно только на Keenetic"))
		return
	}
	id, err := kb.CreateSlot()
	if err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("slots", "create", "создан слот "+id)
	if err := s.backend.SaveConfig(); err != nil {
		s.store.LogEvent("slots", "create", "конфиг не сохранился: "+err.Error())
	}
	writeJSON(w, http.StatusOK, map[string]string{"slot": id, "device": keenetic.DeviceName(id)})
}

func (s *Server) createPool(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name         string `json:"name"`
		KeeneticSlot string `json:"keeneticSlot"`
		OpenwrtProto string `json:"openwrtProto"`
		Fallback     string `json:"fallback"`
		ProbeHost    string `json:"probeHost"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	settings := store.PoolSettings{
		Platform:     s.backend.Name(),
		KeeneticSlot: req.KeeneticSlot,
		Fallback:     req.Fallback,
		ProbeHost:    req.ProbeHost,
	}
	if s.backend.Name() == store.PlatformOpenwrt {
		settings.OpenwrtProto = req.OpenwrtProto
		if settings.OpenwrtProto == "" {
			settings.OpenwrtProto = "wireguard"
		}
	}
	if s.backend.Name() == store.PlatformKeenetic && settings.KeeneticSlot == "" {
		writeErr(w, fmt.Errorf("не выбран слот Keenetic: не удалось получить список слотов, повторите позже"))
		return
	}
	if err := s.validFallback(req.Name, settings.Fallback); err != nil {
		writeErr(w, err)
		return
	}
	pool, err := s.store.CreatePool(req.Name, settings)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, pool)
}

func (s *Server) updatePool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pool, ok := s.store.Pool(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var patch store.PoolSettings
	var maxRtt *int
	var updateIntervalH *float64
	{
		var raw struct {
			store.PoolSettings
			MaxRTTms        *int     `json:"maxRttMs"`
			UpdateIntervalH *float64 `json:"updateIntervalH"`
		}
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			writeErr(w, err)
			return
		}
		patch = raw.PoolSettings
		maxRtt = raw.MaxRTTms
		updateIntervalH = raw.UpdateIntervalH
	}
	merged := pool.Settings
	if patch.ProbeHost != "" {
		merged.ProbeHost = patch.ProbeHost
	}
	if patch.CheckIntervalSec > 0 {
		merged.CheckIntervalSec = patch.CheckIntervalSec
	}
	if patch.FailThreshold > 0 {
		merged.FailThreshold = patch.FailThreshold
	}
	if patch.CooldownMin > 0 {
		merged.CooldownMin = patch.CooldownMin
	}
	if patch.Fallback != "" {
		merged.Fallback = patch.Fallback
	}
	if patch.Keepalive > 0 {
		merged.Keepalive = patch.Keepalive
	}
	if maxRtt != nil {
		merged.MaxRTTms = *maxRtt
	}
	if updateIntervalH != nil {
		if *updateIntervalH < 0 || *updateIntervalH > 8760 {
			writeErr(w, fmt.Errorf("интервал автообновления должен быть 0 (из подписки) или 1-8760 часов"))
			return
		}
		merged.UpdateIntervalH = *updateIntervalH
	}
	if patch.MagitrickleGroupID != "" {
		merged.MagitrickleGroupID = patch.MagitrickleGroupID
	}
	if err := s.validFallback(name, merged.Fallback); err != nil {
		writeErr(w, err)
		return
	}
	engineRebuild := pool.Settings.EngineMode == engineMode
	if err := s.store.UpdatePool(name, merged); err != nil {
		writeErr(w, err)
		return
	}
	if engineRebuild {
		if _, err := s.applyEngine(); err != nil {
			s.store.LogEvent(name, "applied", "движок не пересобран после смены настроек: "+err.Error())
		}
		writeJSON(w, http.StatusOK, map[string]string{"ok": "updated"})
		return
	}
	s.engine.CheckNow(name)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "updated"})
}

func (s *Server) deletePool(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	wasEngine := s.engineEnabled(r)
	var err error
	if wasEngine {
		err = s.store.DeletePool(name)
	} else {
		err = s.engine.DeletePool(name)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	if wasEngine {
		if _, err := s.applyEngine(); err != nil {
			s.store.LogEvent(name, "applied", "движок не пересобран после удаления: "+err.Error())
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}

func (s *Server) uploadConfigs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, ok := s.store.Pool(name); !ok {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, err)
		return
	}
	var all []wgconf.NamedConfig
	var errors []string
	for _, headers := range r.MultipartForm.File {
		for _, fh := range headers {
			f, err := fh.Open()
			if err != nil {
				errors = append(errors, fh.Filename+": "+err.Error())
				continue
			}
			data, _ := io.ReadAll(f)
			f.Close()
			parsed, err := wgconf.IngestFile(fh.Filename, data)
			if err != nil {
				errors = append(errors, err.Error())
				continue
			}
			all = append(all, parsed...)
		}
	}
	unique, dupes := wgconf.Dedupe(all)
	added, storeDupes, err := s.store.AddConfigs(name, unique)
	if err != nil {
		writeErr(w, err)
		return
	}
	dupes = append(dupes, storeDupes...)
	// помечаем конфликтные: адрес занят другим интерфейсом сейчас ->
	// в ротацию не попадает, пока конфликт не исчезнет
	var warnings []string
	if pool, ok := s.store.Pool(name); ok {
		for _, c := range pool.Configs {
			if !c.Enabled {
				continue
			}
			if msg := s.configConflict(name, pool.DeviceName(), s.poolConfigAddrs(name, c)); msg != "" {
				_ = s.store.SetConfigEnabled(name, c.File, false)
				warnings = append(warnings, c.Original+": "+msg+", исключен из ротации")
			}
		}
	}
	for _, w := range warnings {
		s.store.LogEvent(name, "conflict", w)
	}
	s.engine.CheckNow(name)
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "duplicates": dupes, "errors": errors, "warnings": warnings})
}

func (s *Server) deleteConfig(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RemoveConfig(r.PathValue("name"), r.PathValue("file")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}

func (s *Server) enableConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Enabled {
		if err := s.liveAddressConflict(r.PathValue("name"), r.PathValue("file")); err != nil {
			writeErr(w, err)
			return
		}
	}
	if err := s.store.SetConfigEnabled(r.PathValue("name"), r.PathValue("file"), req.Enabled); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "ok"})
}

func (s *Server) moveConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Delta int `json:"delta"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Delta != -1 && req.Delta != 1 {
		writeErr(w, fmt.Errorf("смещение должно быть -1 или 1"))
		return
	}
	if err := s.store.MoveConfig(r.PathValue("name"), r.PathValue("file"), req.Delta); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "moved"})
}

func (s *Server) postRotate(w http.ResponseWriter, r *http.Request) {
	if s.engineEnabled(r) {
		writeErr(w, fmt.Errorf("пул работает через движок: узлы переключаются в его группе, ротация не нужна"))
		return
	}
	if err := s.engine.RotateNow(r.PathValue("name")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "rotating"})
}

func (s *Server) postCheck(w http.ResponseWriter, r *http.Request) {
	if s.engineEnabled(r) {
		writeErr(w, fmt.Errorf("пул работает через движок: проверка выполняется автоматически"))
		return
	}
	s.engine.CheckNow(r.PathValue("name"))
	writeJSON(w, http.StatusOK, map[string]string{"ok": "checking"})
}

func (s *Server) postEnable(w http.ResponseWriter, r *http.Request) {
	if s.engineEnabled(r) {
		s.postEnableEngine(w, r)
		return
	}
	if err := s.engine.EnablePool(r.PathValue("name")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "enabled"})
}

func (s *Server) postDisable(w http.ResponseWriter, r *http.Request) {
	if s.engineEnabled(r) {
		s.postDisableEngine(w, r)
		return
	}
	if err := s.engine.DisablePool(r.PathValue("name")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "disabled"})
}

func (s *Server) postActivate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		File string `json:"file"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.File == "" {
		writeErr(w, err)
		return
	}
	if err := s.liveAddressConflict(r.PathValue("name"), req.File); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.engine.SetActive(r.PathValue("name"), req.File); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "activated"})
}

func (s *Server) getMagitrickle(w http.ResponseWriter, r *http.Request) {
	if s.mt == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	groups, err := s.mt.Groups(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "groups": groups})
}

func (s *Server) systemCheck() provision.Result {
	if s.backend.Name() == store.PlatformKeenetic {
		if kb, ok := s.backend.(*keenetic.Backend); ok {
			return provision.CheckKeenetic(kb.Ndmc)
		}
	}
	return provision.CheckOpenwrt()
}

func (s *Server) getSystemCheck(w http.ResponseWriter, r *http.Request) {
	// панель опрашивает проверку зависимостей каждые 60с, а каждая проверка
	// гоняет opkg/apk-пайплайны и version-запуски - на слабом роутере это
	// постоянная фоновая нагрузка; кэш 3 минуты, сбрасывается после установки
	s.sysCache.serve(w, 3*time.Minute, func() ([]byte, error) {
		return json.Marshal(s.systemCheck())
	})
}

func (s *Server) postSystemInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     string `json:"id"`
		Flavor string `json:"flavor"`
		Backup *bool  `json:"backup"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeErr(w, fmt.Errorf("не указан id компонента"))
		return
	}
	if req.ID == "singbox-lx" {
		backup := true
		if req.Backup != nil {
			backup = *req.Backup
		}
		s.installLXCore(w, r, req.Flavor, backup)
		return
	}
	check := s.systemCheck()
	for _, item := range check.Items {
		if item.ID != req.ID {
			continue
		}
		if item.Installed || item.Action == "" {
			writeErr(w, fmt.Errorf("ничего не требуется для %s", item.ID))
			return
		}
		out, err := provision.Install(item.Action)
		s.store.LogEvent("system", "install", item.ID+": "+firstLine(out))
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "output": out})
			return
		}
		s.sysCache.reset()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": out})
		return
	}
	writeErr(w, fmt.Errorf("компонент %s не найден", req.ID))
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}

func (s *Server) mtClient() *magitrickle.Client {
	if s.mt != nil {
		return s.mt
	}
	return magitrickle.New("http://127.0.0.1:8080")
}

type mtSnapshot struct {
	at      time.Time
	rawHash [sha256.Size]byte
	groups  []magitrickle.Group
	titles  map[string]string
	err     error
}

// mtSnapshotCached - свежие группы + заголовки слотов. Сырой JSON групп
// получаем на каждый вызов (дёшево), декод - только если хеш изменился.
// titles отдаётся копией: обработчики мутируют карту, шарить нельзя.
func (s *Server) mtSnapshotCached() (groups []magitrickle.Group, titles map[string]string, err error) {
	s.mtCacheMu.Lock()
	defer s.mtCacheMu.Unlock()
	raw, rerr := s.mtClient().GroupsRaw(context.Background())
	if s.mtCache == nil {
		s.mtCache = &mtSnapshot{}
	}
	if rerr == nil {
		sum := sha256.Sum256(raw)
		if s.mtCache.err != nil || s.mtCache.rawHash != sum {
			if decoded, derr := magitrickle.DecodeGroups(raw); derr == nil {
				s.mtCache.rawHash, s.mtCache.groups, s.mtCache.err = sum, decoded, nil
			} else {
				s.mtCache.err = derr
			}
		}
	} else {
		s.mtCache.err = rerr
	}
	s.mtCache.at = time.Now()
	s.mtCache.titles = s.mtInterfaceTitles()
	return s.mtCache.groups, copyTitles(s.mtCache.titles), s.mtCache.err
}

func copyTitles(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (s *Server) mtInterfaceTitles() map[string]string {
	titles := map[string]string{}
	for _, p := range s.store.Pools() {
		titles[p.DeviceName()] = p.Name
	}
	if slots, err := s.backend.Slots(); err == nil {
		for _, sl := range slots {
			if sl.Description == "" || sl.Device == "" {
				continue
			}
			if _, exists := titles[sl.Device]; exists {
				continue
			}
			if s.store.IfaceModes()[sl.Device] == store.IfaceExternal {
				titles[sl.Device] = sl.Description
			}
		}
	}
	return titles
}

// mtDuplicates: дубликаты правил по ключу тип:паттерн, как их считает
// сам magitrickle (совпадение в разных группах или два включенных в одной).
func (s *Server) mtDuplicates(w http.ResponseWriter, r *http.Request) {
	groups, err := s.mtClient().GroupsWithRules(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	type ref struct {
		GroupID   string `json:"groupId"`
		GroupName string `json:"groupName"`
		RuleID    string `json:"ruleId"`
		RuleType  string `json:"ruleType"`
	}
	// ключ - только паттерн: domain и namespace на один домен перекрываются
	// полностью, для владельца это тот же дубликат.
	// Не участвуют: выключенные правила и группы целиком - в маршрутизации
	// их нет, пересечением они быть не могут.
	byKey := map[string][]ref{}
	for _, g := range groups {
		if !g.Enable {
			continue
		}
		for _, rl := range g.Rules {
			if !rl.Enable || strings.TrimSpace(rl.Rule) == "" {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(rl.Rule))
			byKey[key] = append(byKey[key], ref{GroupID: g.ID, GroupName: g.Name, RuleID: rl.ID, RuleType: rl.Type})
		}
	}
	out := map[string][]ref{}
	for key, refs := range byKey {
		if len(refs) < 2 {
			continue
		}
		groupsOf := map[string]bool{}
		for _, ref := range refs {
			groupsOf[ref.GroupID] = true
		}
		if len(groupsOf) < 2 {
			continue
		}
		out[key] = refs
	}
	writeJSON(w, http.StatusOK, map[string]any{"duplicates": out})
}

func (s *Server) mtGetGroups(w http.ResponseWriter, r *http.Request) {
	groups, titles, err := s.mtSnapshotCached()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "error": err.Error()})
		return
	}
	type groupView struct {
		magitrickle.Group
		InterfaceTitle string              `json:"interfaceTitle,omitempty"`
		Policy         *store.GroupPolicy  `json:"policy,omitempty"`
		Cascade        *store.CascadeEntry `json:"cascade,omitempty"`
		Degraded       string              `json:"degraded,omitempty"`
	}
	policies := map[string]store.GroupPolicy{}
	for id, p := range s.store.GroupPoliciesAll() {
		policies[id] = p
	}
	cascades := map[string]store.CascadeEntry{}
	for _, c := range s.store.Cascades() {
		cascades[c.Group] = c
	}
	degraded := s.store.DegradedGroups()
	out := make([]groupView, 0, len(groups))
	seen := map[string]bool{}
	for _, g := range groups {
		gv := groupView{Group: g}
		if t, ok := titles[g.Interface]; ok && t != g.Interface {
			gv.InterfaceTitle = t
		}
		if p, ok := policies[g.ID]; ok {
			gv.Policy = &p
		}
		if c, ok := cascades[g.ID]; ok {
			gv.Cascade = &c
		}
		if d, ok := degraded[g.ID]; ok {
			gv.Degraded = d.Mode
		}
		seen[g.Interface] = true
		out = append(out, gv)
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "groups": out})
}

func (s *Server) mtGetInterfaces(w http.ResponseWriter, r *http.Request) {
	groups, titles, err := s.mtSnapshotCached()
	if err == nil {
		for _, g := range groups {
			// blackhole - служебное имя блокировки, UI добавляет его сам
			if g.Interface == "blackhole" {
				continue
			}
			if _, ok := titles[g.Interface]; !ok {
				titles[g.Interface] = ""
			}
		}
	}
	type ifaceView struct {
		Device string `json:"device"`
		Title  string `json:"title,omitempty"`
	}
	out := []ifaceView{}
	for dev, title := range titles {
		out = append(out, ifaceView{Device: dev, Title: title})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	writeJSON(w, http.StatusOK, map[string]any{"interfaces": out})
}

func (s *Server) mtGetPresets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"presets":    magitrickle.Presets,
		"sourceUrl":  magitrickle.AllowDomainsURL,
		"sourceNote": magitrickle.SourceNote(),
	})
}

func (s *Server) mtCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		Interface string `json:"interface"`
		Color     string `json:"color"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Interface == "" {
		writeErr(w, fmt.Errorf("нужны name и interface"))
		return
	}
	color := req.Color
	if color == "" {
		color = "#4a9eff"
	}
	client := s.mtClient()
	if err := client.EnsureHealthy(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	g, err := client.CreateGroup(r.Context(), magitrickle.Group{
		Name: req.Name, Interface: req.Interface, Color: color, Enable: true,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	client.RefreshShadow(r.Context())
	s.store.LogEvent("rules", "magitrickle", "создана группа "+req.Name+" на "+req.Interface)
	writeJSON(w, http.StatusOK, g)
}

// magitrickle 0.8.2 не пересоздаёт iptables-цепочку при одиночном PUT
// группы, поэтому все правки идут массовым сохранением всего списка.
func (s *Server) mtApplyGroups(ctx context.Context, mutate func(groups []magitrickle.Group) bool) error {
	if c := s.casc(); c != nil {
		c.BeforeMutate(mutate)
		defer c.AfterMutate()
	}
	return s.mtClient().MutateGroups(ctx, mutate)
}

func (s *Server) mtUpdateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		Color     string `json:"color"`
		Interface string `json:"interface"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	id := r.PathValue("id")
	if req.Interface != "" {
		if _, isCasc := s.store.CascadeByGroup(id); isCasc {
			writeErr(w, fmt.Errorf("интерфейс каскадной группы задается при создании каскада"))
			return
		}
	}
	found := false
	err := s.mtApplyGroups(r.Context(), func(groups []magitrickle.Group) bool {
		for i := range groups {
			g := &groups[i]
			if g.ID != id {
				continue
			}
			if req.Name != "" {
				g.Name = req.Name
			}
			if req.Color != "" {
				g.Color = req.Color
			}
			if req.Interface != "" {
				g.Interface = req.Interface
			}
			found = true
			return true
		}
		return false
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	s.store.LogEvent("rules", "magitrickle", "изменена группа "+req.Name)
	writeJSON(w, http.StatusOK, map[string]string{"ok": "updated"})
}

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(1 << 20)
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			limit = 32 << 20
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

func reorderIDs[T any](items []T, idOf func(T) string, ids []string) ([]T, bool) {
	if len(ids) != len(items) {
		return nil, false
	}
	byID := map[string]T{}
	for _, it := range items {
		byID[idOf(it)] = it
	}
	out := make([]T, 0, len(items))
	for _, id := range ids {
		it, ok := byID[id]
		if !ok {
			return nil, false
		}
		out = append(out, it)
		delete(byID, id)
	}
	if len(byID) > 0 {
		return nil, false
	}
	return out, true
}

func (s *Server) mtGroupsOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	client := s.mtClient()
	groups, err := client.GroupsWithRules(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	ordered, ok := reorderIDs(groups, func(g magitrickle.Group) string { return g.ID }, req.IDs)
	if !ok {
		writeErr(w, fmt.Errorf("список ids не совпадает с текущими группами"))
		return
	}
	if err := client.UpdateGroups(r.Context(), ordered, true); err != nil {
		writeErr(w, err)
		return
	}
	s.store.LogEvent("rules", "magitrickle", "изменён порядок групп")
	writeJSON(w, http.StatusOK, map[string]string{"ok": "reordered"})
}

func (s *Server) mtRulesOrder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	id := r.PathValue("id")
	found, mismatch := false, false
	err := s.mtApplyGroups(r.Context(), func(groups []magitrickle.Group) bool {
		for i := range groups {
			if groups[i].ID != id {
				continue
			}
			ordered, ok := reorderIDs(groups[i].Rules, func(rl magitrickle.Rule) string { return rl.ID }, req.IDs)
			if !ok {
				mismatch = true
				return false
			}
			groups[i].Rules = ordered
			found = true
			return true
		}
		return false
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if mismatch {
		writeErr(w, fmt.Errorf("список ids не совпадает с правилами группы"))
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	s.store.LogEvent("rules", "magitrickle", "изменён порядок правил в группе")
	writeJSON(w, http.StatusOK, map[string]string{"ok": "reordered"})
}

func (s *Server) mtToggleGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enable bool `json:"enable"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	id := r.PathValue("id")
	if c := s.casc(); c != nil {
		if _, isCasc := s.store.CascadeByGroup(id); isCasc {
			if err := c.SetEnabled(r.Context(), id, req.Enable); err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"ok": "ok"})
			return
		}
	}
	found := false
	err := s.mtApplyGroups(r.Context(), func(groups []magitrickle.Group) bool {
		for i := range groups {
			if groups[i].ID != id {
				continue
			}
			found = true
			if groups[i].Enable == req.Enable {
				return false
			}
			groups[i].Enable = req.Enable
			return true
		}
		return false
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "ok"})
}

func (s *Server) mtDeleteGroup(w http.ResponseWriter, r *http.Request) {
	if c := s.casc(); c != nil {
		if _, isCasc := s.store.CascadeByGroup(r.PathValue("id")); isCasc {
			if err := c.Delete(r.Context(), r.PathValue("id")); err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
			return
		}
	}
	client := s.mtClient()
	if err := client.DeleteGroup(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	client.RefreshShadow(r.Context())
	s.store.LogEvent("rules", "magitrickle", "удалена группа "+r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}

func (s *Server) cascList(w http.ResponseWriter, r *http.Request) {
	c := s.casc()
	if c == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "cascades": c.Infos(r.Context())})
}

func (s *Server) cascCreate(w http.ResponseWriter, r *http.Request) {
	c := s.casc()
	if c == nil {
		writeErr(w, fmt.Errorf("magitrickle недоступен"))
		return
	}
	var req struct {
		Source string   `json:"source"`
		Via    string   `json:"via"`
		Hosts  []string `json:"hosts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Source == "" || req.Via == "" {
		writeErr(w, fmt.Errorf("нужны source и via"))
		return
	}
	g, err := c.Create(r.Context(), req.Source, req.Via, req.Hosts)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) cascDelete(w http.ResponseWriter, r *http.Request) {
	c := s.casc()
	if c == nil {
		writeErr(w, fmt.Errorf("magitrickle недоступен"))
		return
	}
	if err := c.Delete(r.Context(), r.PathValue("group")); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}

// getPolicyCycles/putPolicyCycles - гистерезис политик групп: сколько
// циклов до переключения с исходного интерфейса и сколько до возврата.
func (s *Server) getPolicyCycles(w http.ResponseWriter, r *http.Request) {
	st := s.store.Settings()
	writeJSON(w, http.StatusOK, map[string]int{
		"failCycles":    st.PolicyFailCycles,
		"restoreCycles": st.PolicyRestoreCycles,
	})
}

func (s *Server) putPolicyCycles(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FailCycles    int `json:"failCycles"`
		RestoreCycles int `json:"restoreCycles"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	if req.FailCycles < 1 || req.FailCycles > 20 || req.RestoreCycles < 1 || req.RestoreCycles > 20 {
		writeErr(w, fmt.Errorf("циклы должны быть от 1 до 20"))
		return
	}
	st := s.store.Settings()
	st.PolicyFailCycles = req.FailCycles
	st.PolicyRestoreCycles = req.RestoreCycles
	if err := s.store.SetSettings(st); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{
		"failCycles":    st.PolicyFailCycles,
		"restoreCycles": st.PolicyRestoreCycles,
	})
}

func (s *Server) mtSetPolicy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OnDead string `json:"onDead"`
		Iface  string `json:"iface"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, err)
		return
	}
	var p *store.GroupPolicy
	switch req.OnDead {
	case "":
		p = nil
	case store.PolicyDirect:
		p = &store.GroupPolicy{OnDead: req.OnDead}
	case store.PolicyIface:
		if req.Iface == "" {
			writeErr(w, fmt.Errorf("укажите запасной интерфейс"))
			return
		}
		p = &store.GroupPolicy{OnDead: req.OnDead, Iface: req.Iface}
	default:
		writeErr(w, fmt.Errorf("неизвестный режим %q", req.OnDead))
		return
	}
	id := r.PathValue("id")
	if err := s.store.SetGroupPolicy(id, p); err != nil {
		writeErr(w, err)
		return
	}
	if p == nil {
		s.store.LogEvent("policy", "set", "группа "+id+": политика сброшена")
	} else {
		s.store.LogEvent("policy", "set", "группа "+id+": при отвале - "+p.OnDead)
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "ok"})
}

var ruleTypes = map[string]bool{
	"domain": true, "namespace": true, "wildcard": true, "regex": true, "subnet": true,
}

func (s *Server) mtImportRules(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text     string `json:"text"`
		Type     string `json:"type"`
		ToSecond bool   `json:"toSecond"`
		Enable   *bool  `json:"enable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		writeErr(w, fmt.Errorf("пустой список"))
		return
	}
	if req.Type != "auto" && !ruleTypes[req.Type] {
		writeErr(w, fmt.Errorf("неизвестный тип правила"))
		return
	}
	enabled := true
	if req.Enable != nil {
		enabled = *req.Enable
	}
	parsed, badCount, reasons := magitrickle.ParseImport(req.Text, req.Type, true, req.ToSecond)
	if len(parsed) == 0 {
		msg := "в списке не нашлось правил"
		if len(reasons) > 0 {
			msg += ": " + strings.Join(reasons, "; ")
		}
		writeErr(w, fmt.Errorf("%s", msg))
		return
	}
	for i := range parsed {
		parsed[i].Enable = enabled
	}
	id := r.PathValue("id")
	added, dup, found := 0, 0, false
	groupName := id
	err := s.mtApplyGroups(r.Context(), func(groups []magitrickle.Group) bool {
		for i := range groups {
			g := &groups[i]
			if g.ID != id {
				continue
			}
			found = true
			groupName = g.Name
			existing := map[string]bool{}
			for _, rl := range g.Rules {
				existing[rl.Type+" "+rl.Rule] = true
			}
			for _, rl := range parsed {
				if existing[rl.Type+" "+rl.Rule] {
					dup++
					continue
				}
				g.Rules = append(g.Rules, rl)
				added++
			}
			return true
		}
		return false
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	if added > 0 {
		s.store.LogEvent("rules", "magitrickle", fmt.Sprintf("в группу %s импортировано правил: %d", groupName, added))
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "skipped": dup + badCount, "bad": reasons})
}

func (s *Server) mtCreateRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type string `json:"type"`
		Rule string `json:"rule"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !ruleTypes[req.Type] || req.Rule == "" {
		writeErr(w, fmt.Errorf("нужны type (domain/namespace/wildcard/regex/subnet) и rule"))
		return
	}
	client := s.mtClient()
	if err := client.EnsureHealthy(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	rule := magitrickle.Rule{Type: req.Type, Rule: req.Rule, Name: req.Name, Enable: true}
	if req.Type != "regex" {
		norm, ok := magitrickle.NormalizeRule(req.Type, req.Rule, false)
		if !ok {
			writeErr(w, fmt.Errorf("значение не похоже на %s", req.Type))
			return
		}
		norm.Name, norm.Enable = req.Name, true
		rule = norm
	} else if strings.Contains(req.Rule, "://") {
		writeErr(w, fmt.Errorf("похоже на URL, а не на регулярку"))
		return
	}
	if _, err := client.CreateRule(r.Context(), r.PathValue("id"), rule); err != nil {
		writeErr(w, err)
		return
	}
	client.RefreshShadow(r.Context())
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) mtUpdateRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type string `json:"type"`
		Rule string `json:"rule"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !ruleTypes[req.Type] || req.Rule == "" {
		writeErr(w, fmt.Errorf("нужны type (domain/namespace/wildcard/regex/subnet) и rule"))
		return
	}
	client := s.mtClient()
	if err := client.EnsureHealthy(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	g, err := client.GroupByID(r.Context(), r.PathValue("id"), true)
	if err != nil {
		writeErr(w, err)
		return
	}
	ruleID := r.PathValue("rid")
	for _, rl := range g.Rules {
		if rl.ID != ruleID {
			continue
		}
		rl.Type = req.Type
		rl.Rule = req.Rule
		rl.Name = req.Name
		if err := client.UpdateRule(r.Context(), g.ID, rl); err != nil {
			writeErr(w, err)
			return
		}
		client.RefreshShadow(r.Context())
		writeJSON(w, http.StatusOK, map[string]string{"ok": "updated"})
		return
	}
	http.NotFound(w, r)
}

func (s *Server) mtToggleRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enable bool `json:"enable"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	client := s.mtClient()
	if err := client.EnsureHealthy(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	groups, err := client.GroupsWithRules(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	groupID, ruleID := r.PathValue("id"), r.PathValue("rid")
	for _, g := range groups {
		if g.ID != groupID {
			continue
		}
		for _, rl := range g.Rules {
			if rl.ID != ruleID {
				continue
			}
			rl.Enable = req.Enable
			if err := client.UpdateRule(r.Context(), groupID, rl); err != nil {
				writeErr(w, err)
				return
			}
			client.RefreshShadow(r.Context())
			writeJSON(w, http.StatusOK, map[string]string{"ok": "ok"})
			return
		}
	}
	http.NotFound(w, r)
}

func (s *Server) mtDeleteRule(w http.ResponseWriter, r *http.Request) {
	client := s.mtClient()
	if err := client.EnsureHealthy(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	if err := client.DeleteRule(r.Context(), r.PathValue("id"), r.PathValue("rid")); err != nil {
		writeErr(w, err)
		return
	}
	client.RefreshShadow(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"ok": "deleted"})
}

func (s *Server) mtApplyPreset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Interface string `json:"interface"`
		Name      string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Interface == "" {
		writeErr(w, fmt.Errorf("нужен interface"))
		return
	}
	preset, ok := magitrickle.PresetByID(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	name := req.Name
	if name == "" {
		name = preset.ID
	}
	client := s.mtClient()
	if err := client.EnsureHealthy(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	created := magitrickle.Group{Name: name, Interface: req.Interface, Enable: true, Rules: preset.Rules}
	if err := client.AddGroup(r.Context(), created); err != nil {
		writeErr(w, fmt.Errorf("группа не создана: %v", err))
		return
	}
	gid := ""
	if groups, err := client.GroupsWithRules(r.Context()); err == nil {
		for _, g := range groups {
			if g.Name == name && g.Interface == req.Interface {
				gid = g.ID
			}
		}
	}
	s.store.LogEvent("rules", "magitrickle", "применён шаблон "+preset.ID+" на "+req.Interface)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "groupId": gid, "rules": len(preset.Rules)})
}
