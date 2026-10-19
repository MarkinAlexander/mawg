package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"mawg/internal/store"
	"mawg/internal/warp"
	"mawg/internal/wgconf"
)

// Генератор WARP-конфигов (77-док). Кнопка в панели -> N регистраций
// устройств Cloudflare WARP -> пул с готовыми конфигами; каждый конфиг
// проверяется живьём сразу. Ключи генерируются локально, в Cloudflare
// уходит только pubkey. Регистрации - через internal/warp (контракт
// LxBox: api.devices.cloudflare.com первым). Подход и списки сайтов -
// warp-generator, атрибуция в UI диалога и в README.

func (s *Server) warpClient() *warp.Client {
	if s.warpReg != nil {
		return s.warpReg
	}
	return &warp.Client{}
}

// SetWarpClient - подмена регистратора для тестов.
func (s *Server) SetWarpClient(c *warp.Client) { s.warpReg = c }

const warpMaxConfigs = 16

type warpGenerateReq struct {
	Count        int      `json:"count"`
	Mode         string   `json:"mode"` // "random" | "fixed"; пусто = random
	Endpoint     string   `json:"endpoint"`
	Sites        []string `json:"sites"`
	Domains      []string `json:"domains"`
	KeeneticSlot string   `json:"keeneticSlot"`
	Fallback     string   `json:"fallback"`
	ProbeHost    string   `json:"probeHost"`
}

type warpResultOne struct {
	File     string `json:"file,omitempty"`
	Endpoint string `json:"endpoint"`
	Address  string `json:"address,omitempty"`
	OK       bool   `json:"ok"`
	Detail   string `json:"detail,omitempty"`
	Error    string `json:"error,omitempty"`
}

type warpPlan struct {
	Pool     string          `json:"pool,omitempty"`
	Added    int             `json:"added,omitempty"`
	Results  []warpResultOne `json:"results,omitempty"`
	Warnings []string        `json:"warnings,omitempty"`
}

// warpPresets - данные для диалога: диапазоны, порты, пресеты пар,
// сайты маскировки, подсказка доменов, лимиты.
func (s *Server) warpPresets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"domainEndpoint": warp.DomainEndpoint,
		"v4Blocks":       warp.V4Blocks,
		"ports":          warp.Ports,
		"presets":        warp.Presets,
		"sites":          warp.SitePresets(),
		"domainHints":    warp.DomainHints,
		"limits":         map[string]int{"count": warpMaxConfigs},
	})
}

func decodeWarpReq(w http.ResponseWriter, r *http.Request, req *warpGenerateReq) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(req); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, errors.New("не удалось разобрать запрос генерации WARP"))
		return false
	}
	if d.Decode(new(any)) != io.EOF {
		writeErr(w, errors.New("лишние данные в запросе генерации WARP"))
		return false
	}
	return true
}

func (s *Server) warpGenerate(w http.ResponseWriter, r *http.Request) {
	var req warpGenerateReq
	if !decodeWarpReq(w, r, &req) {
		return
	}
	if req.Count < 1 || req.Count > warpMaxConfigs {
		writeErr(w, fmt.Errorf("количество конфигов - от 1 до %d", warpMaxConfigs))
		return
	}
	if req.Mode == "" {
		req.Mode = "random"
	}
	if req.Mode != "random" && req.Mode != "fixed" {
		writeErr(w, errors.New("режим - random или fixed"))
		return
	}
	if req.Mode == "fixed" {
		if req.Endpoint == "" {
			req.Endpoint = warp.Presets[0]
		}
		if err := warp.ValidEndpoint(req.Endpoint); err != nil {
			writeErr(w, err)
			return
		}
	}
	if err := s.validFallback("warp", req.Fallback); err != nil {
		writeErr(w, err)
		return
	}
	settings, err := s.poolSettingsFromReq(poolSourceReq{
		Name: "warp", KeeneticSlot: req.KeeneticSlot, Fallback: req.Fallback, ProbeHost: req.ProbeHost,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	cidrs, warnings := warp.AllowedIPs(r.Context(), req.Sites, req.Domains)

	endpoints := make([]string, 0, req.Count)
	if req.Mode == "random" {
		endpoints, err = warp.RandomPairs(req.Count, nil)
		if err != nil {
			writeErr(w, err)
			return
		}
	} else {
		for i := 0; i < req.Count; i++ {
			endpoints = append(endpoints, req.Endpoint)
		}
	}

	// Регистрации идут последовательно с паузой - Cloudflare
	// рейт-лимитит, спешить некуда (прецедент mtinstall: долгие
	// операции синхронно, у UI нет 90с-таймаута).
	client := s.warpClient()
	ncs := make([]wgconf.NamedConfig, 0, req.Count)
	results := make([]warpResultOne, 0, req.Count)
	devs := make([]*warp.Device, 0, req.Count)
	var lastErr error
	for i, ep := range endpoints {
		if i > 0 {
			select {
			case <-r.Context().Done():
				writeErr(w, errors.New("генерация прервана"))
				return
			case <-time.After(time.Second):
			}
		}
		nc, dev, err := warp.GenerateOne(r.Context(), client, ep, cidrs)
		if err != nil {
			lastErr = err
			results = append(results, warpResultOne{Endpoint: ep, Error: err.Error()})
			continue
		}
		devs = append(devs, dev)
		ncs = append(ncs, nc)
		results = append(results, warpResultOne{Endpoint: ep, Address: nc.Config.FirstIPv4()})
	}
	if len(ncs) == 0 {
		s.writeWarpErr(w, lastErr)
		return
	}

	settings.Warp = &store.WarpMeta{Mode: req.Mode, Endpoint: req.Endpoint, Sites: req.Sites, Domains: req.Domains}
	name := wgconf.UniqueSlug("warp", func(cand string) bool {
		_, ok := s.store.Pool(cand)
		return ok
	})
	pool, err := s.store.CreatePool(name, settings)
	if err != nil {
		writeErr(w, err)
		return
	}
	added, _, err := s.store.AddConfigs(pool.Name, ncs)
	if err != nil {
		_ = s.store.DeletePool(pool.Name)
		writeErr(w, err)
		return
	}
	for _, dev := range devs {
		_ = s.store.AddWarpDevice(store.WarpDevice{
			ID: dev.ID, Token: dev.Token, Pool: pool.Name,
			Endpoint: dev.PeerEndpoint, AddressV4: dev.AddressV4, CreatedAt: time.Now(),
		})
	}
	p, ok := s.store.Pool(pool.Name)
	if !ok || len(p.Configs) != len(results) {
		writeErr(w, errors.New("пул создан, но состав конфигов не сверился"))
		return
	}
	for i := range results {
		results[i].File = p.Configs[i].File
	}

	// Живая самопроверка каждого конфига (слово владельца: «надо
	// проверять самостоятельно все варианты»). Активным остаётся первый
	// рабочий; если рабочих нет - первый (честно видно в карточке).
	firstOK := ""
	okCount := 0
	for i := range p.Configs {
		good, detail, err := s.engine.ProbeConfigNow(p.Name, p.Configs[i].File)
		results[i].Detail = detail
		if err != nil {
			results[i].Error = err.Error()
			continue
		}
		results[i].OK = good
		if good {
			okCount++
			if firstOK == "" {
				firstOK = p.Configs[i].File
			}
		}
	}
	if firstOK == "" {
		firstOK = p.Configs[0].File
		warnings = append(warnings, "ни один конфиг не прошёл живую проверку - проверьте пул позже кнопкой «Проверить»")
	}
	if err := s.engine.SetActive(p.Name, firstOK); err != nil {
		warnings = append(warnings, "активный конфиг не установлен: "+err.Error())
	}

	s.store.LogEvent(p.Name, "applied", fmt.Sprintf("генератор WARP: %d конфигов (режим %s), рабочих %d из %d",
		added, req.Mode, okCount, added))
	writeJSON(w, http.StatusOK, warpPlan{Pool: p.Name, Added: added, Results: results, Warnings: warnings})
}

// poolWarpAdd - «Генерация нового конфига» в карточке живого варп-пула:
// +1 конфиг с теми же параметрами (PoolSettings.Warp), без мастера.
// Пустое тело = параметры пула; в теле можно переопределить sites/domains
// (режим и эндпоинт-политику менять не даём - для этого есть мастер).
func (s *Server) poolWarpAdd(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, ok := s.store.Pool(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !isWarpPool(p) {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "пул не похож на WARP: ни один конфиг не указывает на сети Cloudflare",
		})
		return
	}
	var req warpGenerateReq
	if !decodeWarpReq(w, r, &req) {
		return
	}
	meta := p.Settings.Warp
	if meta == nil {
		meta = &store.WarpMeta{Mode: "random"}
	}
	sites, domains := meta.Sites, meta.Domains
	if len(req.Sites) > 0 || len(req.Domains) > 0 {
		sites, domains = req.Sites, req.Domains
	}
	if req.Count != 1 && req.Count != 0 {
		writeErr(w, errors.New("в существующий пул добавляется один конфиг - используйте count=1"))
		return
	}
	settings := p.Settings
	settings.Warp = &store.WarpMeta{Mode: meta.Mode, Endpoint: meta.Endpoint, Sites: sites, Domains: domains}

	cidrs, warnings := warp.AllowedIPs(r.Context(), sites, domains)
	endpoint := ""
	if meta.Mode == "fixed" && meta.Endpoint != "" {
		endpoint = meta.Endpoint
	}
	avoid := map[string]bool{}
	for _, c := range p.Configs {
		avoid[c.Endpoint] = true
	}
	ep := endpoint
	if endpoint == "" {
		pairs, err := warp.RandomPairs(1, avoid)
		if err != nil {
			writeErr(w, err)
			return
		}
		ep = pairs[0]
	}
	nc, dev, err := warp.GenerateOne(r.Context(), s.warpClient(), ep, cidrs)
	if err != nil {
		s.writeWarpErr(w, err)
		return
	}
	_ = s.store.AddWarpDevice(store.WarpDevice{
		ID: dev.ID, Token: dev.Token, Pool: name,
		Endpoint: ep, AddressV4: dev.AddressV4, CreatedAt: time.Now(),
	})
	added, _, err := s.store.AddConfigs(name, []wgconf.NamedConfig{nc})
	if err != nil {
		writeErr(w, err)
		return
	}
	fresh, _ := s.store.Pool(name)
	last := fresh.Configs[len(fresh.Configs)-1]
	res := warpResultOne{File: last.File, Endpoint: last.Endpoint, Address: nc.Config.FirstIPv4()}
	prevActive := s.store.State(name).ActiveFile
	good, detail, err := s.engine.ProbeConfigNow(name, last.File)
	if err != nil {
		res.Error = err.Error()
	} else {
		res.OK = good
		res.Detail = detail
	}
	// ProbeConfigNow применяет проверяемый конфиг: возвращаем прежний
	// активный (ротация сама уйдёт на новый, если прежний ляжет).
	if prevActive != "" && prevActive != last.File {
		if _, ok := fresh.ConfigByFile(prevActive); ok {
			_ = s.engine.SetActive(name, prevActive)
		}
	}
	if err := s.store.UpdatePool(name, settings); err != nil {
		warnings = append(warnings, "параметры WARP пула не сохранились: "+err.Error())
	}
	s.store.LogEvent(name, "applied", fmt.Sprintf("генератор WARP: +1 конфиг %s (%s)", last.Endpoint, verdictRU(res)))
	writeJSON(w, http.StatusOK, warpPlan{Pool: name, Added: added, Results: []warpResultOne{res}, Warnings: warnings})
}

func verdictRU(res warpResultOne) string {
	switch {
	case res.Error != "":
		return "ошибка проверки"
	case res.OK:
		return "рабочий"
	default:
		return "не отвечает"
	}
}

// isWarpPool - пул считается варповым, если он создан генератором (мета)
// или хотя бы один конфиг указывает на датацентры Cloudflare WARP
// (владелец: узнать существующий пул по адресам/эндпоинтам).
func isWarpPool(p store.Pool) bool {
	if p.Settings.Warp != nil {
		return true
	}
	for _, c := range p.Configs {
		if warp.IsWarpEndpoint(c.Endpoint) {
			return true
		}
	}
	return false
}

// writeWarpErr - недоступность обоих хостов Cloudflare - не «ошибка
// сервера», а диагноз с советом (слово владельца: «пропиарим проект
// zator так же как с днс»): анти-DPI zator + сайт-генератор как ручной
// фолбэк (вставка как AWG-конфига - сайт генерирует с обфускацией).
func (s *Server) writeWarpErr(w http.ResponseWriter, err error) {
	var ue *warp.UnreachableError
	if !errors.As(err, &ue) {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]any{
		"error": err.Error() +
			" - для пробивки используйте анти-DPI средства, например zator для роутеров" +
			" (обвязка оригинального zapret2 от bol-van): https://github.com/AloofLibra/zator." +
			" Можно также сгенерировать конфиг на сайте https://warp-generation.github.io/" +
			" и вставить его в панель как конфиг AmneziaWG (сайт генерирует сразу с обфускацией).",
		"unreachable": true,
		"hosts":       ue.Hosts,
		"generator":   "https://warp-generation.github.io/",
	})
}
