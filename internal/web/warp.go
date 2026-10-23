package web

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
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
	Subnets      []string `json:"subnets"`
	// Dest - куда девать конфиги: "download" (по умолчанию) - только
	// отдать пользователю, роутер не трогаем; "pool" - в пул с живой
	// проверкой. Слово владельца: генерация не должна сама создавать
	// подключение - ключ WARP не должен жить в двух местах сразу.
	Dest         string   `json:"dest"`
	// Pool - существующий варп-пул для dest=pool (пусто = создать новый).
	Pool         string   `json:"pool"`
	// Activate - pool: переключиться на новый конфиг после проверки.
	Activate     bool     `json:"activate"`
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
	// Removed - конфиг не прошёл живую проверку и УДАЛЁН из пула
	// (автоудаление после проверки - слово владельца).
	Removed bool `json:"removed,omitempty"`
}

// warpConfigFile - конфиг для режима "download": полный текст обоих
// вариантов; приватные ключи уезжают только авторизованному юзеру в
// браузер и на роутер НЕ попадают (ключ живёт в одном месте).
type warpConfigFile struct {
	Endpoint string `json:"endpoint"`
	Address  string `json:"address,omitempty"`
	WG       string `json:"wg"`
	AWG      string `json:"awg"`
}

// warpConfigZip - архив конфигов для скачивания одним файлом (base64;
// собирается в памяти - в режиме download на роутере ничего не хранится).
type warpPlan struct {
	Pool     string           `json:"pool,omitempty"`
	Added    int              `json:"added,omitempty"`
	Results  []warpResultOne  `json:"results,omitempty"`
	Configs  []warpConfigFile `json:"configs,omitempty"`
	ZipWG    string           `json:"zipWg,omitempty"`
	ZipAWG   string           `json:"zipAwg,omitempty"`
	Warnings []string         `json:"warnings,omitempty"`
}

// fileSlug - имя файла конфига из эндпоинта (зеркало fileSlug в
// WarpGenerateDialog.vue - при изменении там менять и тут).
func fileSlug(endpoint string) string {
	var b strings.Builder
	for _, r := range endpoint {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	return "warp-" + strings.ToLower(out)
}

// configsZip - zip-архив конфигов (base64). awg - имена *.awg.conf.
func configsZip(cfgs []warpConfigFile, awg bool) (string, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	used := map[string]bool{}
	for _, c := range cfgs {
		name := fileSlug(c.Endpoint) + ".conf"
		text := c.WG
		if awg {
			name = strings.TrimSuffix(name, ".conf") + ".awg.conf"
			text = c.AWG
		}
		base := strings.TrimSuffix(name, ".conf")
		for i := 2; used[name]; i++ {
			name = fmt.Sprintf("%s-%d.conf", base, i)
		}
		used[name] = true
		w, err := zw.Create(name)
		if err != nil {
			return "", err
		}
		if _, err := w.Write([]byte(text)); err != nil {
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// warpPresets - данные для диалога: диапазоны, порты, пресеты пар,
// разрешённые подсети (AllowedIPs), лимиты.
func (s *Server) warpPresets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"domainEndpoint": warp.DomainEndpoint,
		"v4Blocks":       warp.V4Blocks,
		"ports":          warp.Ports,
		"presets":        warp.Presets,
		"sites":          warp.SitePresets(),
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
	dest := req.Dest
	if dest == "" {
		dest = "download"
	}
	if dest != "download" && dest != "pool" {
		writeErr(w, errors.New("dest - download или pool"))
		return
	}
	var settings store.PoolSettings
	if dest == "pool" {
		if err := s.validFallback("warp", req.Fallback); err != nil {
			writeErr(w, err)
			return
		}
		// Слот/проба/фоллбек нужны только НОВОМУ пулу: у существующего они
		// свои, а лишний поиск свободного слота на заполненном Кинетике
		// только зря ронял бы добавление конфига. На Кинетике без свободных
		// слотов новый создаётся автоматически (85-док: мастер WARP не
		// должен отправлять в диалог пула за кнопкой «+ слот»).
		if req.Pool == "" {
			var err error
			settings, err = s.poolSettingsFromReq(poolSourceReq{
				Name: "warp", KeeneticSlot: req.KeeneticSlot, Fallback: req.Fallback, ProbeHost: req.ProbeHost,
			}, true)
			if err != nil {
				writeErr(w, err)
				return
			}
			// Конфиги генератора несут поля обфускации AWG 2.0: на OpenWrt
			// такой пул поднимается только proto amneziawg (на Кинетике поля
			// применяются нативными ASC-аргументами).
			if s.backend.Name() == store.PlatformOpenwrt {
				settings.OpenwrtProto = "amneziawg"
			}
		}
	}

	var cidrs []string
	var warnings []string
	// Слово владельца: в ПУЛ на роутере - только полный туннель. Пресеты
	// подсетей/свои подсети - для скачиваемых конфигов (внешние клиенты):
	// юзер, добавивший в пул «один ютуб», сам себе чёрная дыра.
	if dest == "pool" {
		cidrs = []string{"0.0.0.0/0"}
		warnings = append(warnings, "для пула AllowedIPs принудительно 0.0.0.0/0 - пресеты подсетей игнорируются (они для скачиваемых конфигов)")
	} else {
		var err error
		cidrs, warnings, err = warp.AllowedIPs(req.Sites, req.Subnets)
		if err != nil {
			writeErr(w, err)
			return
		}
	}

	endpoints := make([]string, 0, req.Count)
	if req.Mode == "random" {
		var err error
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

	// Режим "download": конфиги уходят пользователю текстом, роутер не
	// трогаем вообще - ни пула, ни проверок, ни подключения (слово
	// владельца: ключ WARP не должен жить в двух местах сразу).
	if dest == "download" {
		configs := make([]warpConfigFile, len(ncs))
		for i, nc := range ncs {
			configs[i] = warpConfigFile{
				Endpoint: nc.Config.Endpoint(),
				Address:  nc.Config.FirstIPv4(),
				WG:       string(wgconf.StripAWGParams(nc.Raw)),
				AWG:      string(nc.Raw),
			}
		}
		plan := warpPlan{Added: len(ncs), Results: results, Configs: configs, Warnings: warnings}
		if len(configs) > 1 {
			// кнопки «скачать zip»: владелец: скачать все сразу одним архивом
			if zg, err := configsZip(configs, false); err == nil {
				plan.ZipWG = zg
			}
			if za, err := configsZip(configs, true); err == nil {
				plan.ZipAWG = za
			}
		}
		s.store.LogEvent("warp", "generated", fmt.Sprintf("генератор WARP: %d конфигов для скачивания (режим %s), без пула", len(ncs), req.Mode))
		writeJSON(w, http.StatusOK, plan)
		return
	}

	// Режим "pool": в пул с живой проверкой каждого конфига; непрошедшие
	// удаляются сразу (автоудаление после проверки - слово владельца).
	settings.Warp = &store.WarpMeta{Mode: req.Mode, Endpoint: req.Endpoint}
	addMode := false
	name := req.Pool
	if name != "" {
		p, ok := s.store.Pool(name)
		if !ok {
			writeErr(w, fmt.Errorf("пул %q не найден", name))
			return
		}
		if !isWarpPool(p) {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "пул " + name + " не похож на WARP: ни один конфиг не указывает на сети Cloudflare",
			})
			return
		}
		addMode = true
	} else {
		name = wgconf.UniqueSlug("warp", func(cand string) bool {
			_, ok := s.store.Pool(cand)
			return ok
		})
	}
	if !addMode {
		if _, err := s.store.CreatePool(name, settings); err != nil {
			writeErr(w, err)
			return
		}
	}
	for _, dev := range devs {
		_ = s.store.AddWarpDevice(store.WarpDevice{
			ID: dev.ID, Token: dev.Token, Pool: name,
			Endpoint: dev.PeerEndpoint, AddressV4: dev.AddressV4, CreatedAt: time.Now(),
		})
	}
	added, _, err := s.store.AddConfigs(name, ncs)
	if err != nil {
		if !addMode {
			_ = s.store.DeletePool(name)
		}
		writeErr(w, err)
		return
	}
	p, ok := s.store.Pool(name)
	if !ok || len(p.Configs) < len(results) {
		writeErr(w, errors.New("конфиги добавлены, но состав пула не сверился"))
		return
	}
	for i := range results {
		results[i].File = p.Configs[len(p.Configs)-len(results)+i].File
	}

	// Живая проверка нового; непрошедшие - RemoveConfig тут же.
	prevActive := ""
	if addMode {
		prevActive = s.store.State(name).ActiveFile
	}
	firstOK := ""
	okCount := 0
	removedCount := 0
	for i := range results {
		file := results[i].File
		good, detail, checkErr := s.engine.ProbeConfigNow(name, file)
		results[i].Detail = detail
		if checkErr != nil {
			results[i].Error = checkErr.Error()
		} else {
			results[i].OK = good
		}
		if good {
			okCount++
			if firstOK == "" {
				firstOK = file
			}
			continue
		}
		if err := s.store.RemoveConfig(name, file); err == nil {
			results[i].Removed = true
			removedCount++
		}
	}
	kept := okCount
	switch {
	case !addMode && kept == 0:
		// новый пул без единого рабочего конфига не оставляем
		_ = s.store.DeletePool(name)
		warnings = append(warnings, "ни один конфиг не прошёл живую проверку - все удалены, пул не создан")
		s.store.LogEvent("warp", "applied", fmt.Sprintf("генератор WARP: %d конфигов не прошли проверку - пул не создан", added))
		writeJSON(w, http.StatusOK, warpPlan{Added: 0, Results: results, Warnings: warnings})
		return
	case addMode && req.Activate && firstOK != "":
		if err := s.engine.SetActive(name, firstOK); err != nil {
			warnings = append(warnings, "переключиться на новый конфиг не удалось: "+err.Error())
		}
	case addMode && prevActive != "":
		// ProbeConfigNow применял проверяемые конфиги: возвращаем прежний
		if _, ok := s.store.Pool(name); ok {
			if _, ok := p.ConfigByFile(prevActive); ok {
				_ = s.engine.SetActive(name, prevActive)
			}
		}
	case !addMode:
		if err := s.engine.SetActive(name, firstOK); err != nil {
			warnings = append(warnings, "активный конфиг не установлен: "+err.Error())
		}
	}
	if removedCount > 0 {
		warnings = append(warnings, fmt.Sprintf("не прошли живую проверку и удалены: %d", removedCount))
	}
	s.store.LogEvent(name, "applied", fmt.Sprintf("генератор WARP: +%d конфигов в %s (режим %s), рабочих %d, удалено %d",
		kept, name, req.Mode, okCount, removedCount))
	writeJSON(w, http.StatusOK, warpPlan{Pool: name, Added: kept, Results: results, Warnings: warnings})
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
	if req.Count != 1 && req.Count != 0 {
		writeErr(w, errors.New("в существующий пул добавляется один конфиг - используйте count=1"))
		return
	}
	settings := p.Settings
	settings.Warp = &store.WarpMeta{Mode: meta.Mode, Endpoint: meta.Endpoint}

	// Слово владельца: в пуле - только полный туннель (AllowedIPs-пресеты
	// - для скачиваемых конфигов, на роутере они ломают маршрутизацию).
	cidrs := []string{"0.0.0.0/0"}
	var warnings []string
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
	// Автоудаление после проверки (слово владельца): не отвечает -
	// не оставляем мёртвый конфиг в пуле.
	if !res.OK && res.Error == "" {
		if rmErr := s.store.RemoveConfig(name, last.File); rmErr == nil {
			res.Removed = true
		}
	}
	// ProbeConfigNow применяет проверяемый конфиг: возвращаем прежний
	// активный - или остаёмся на новом, если попросили переключиться
	// и он жив.
	if req.Activate && res.OK {
		_ = s.engine.SetActive(name, last.File)
	} else if prevActive != "" && prevActive != last.File {
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
	case res.Removed:
		return "удалён: не ответил"
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
