//go:build linux

package singbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type sharedFacts struct {
	ClashPort   int
	InTags      map[string]bool
	OutTags     map[string]bool
	Tuns        map[string]bool
	Addrs       map[string]bool
	DNSTags     map[string]bool
	ListenPorts map[int]bool
	CacheFile   string
}

// preflightShared разбирает чужой config.json: порт clash_api для проб и
// занятые теги/tun/адреса/dns - наш фрагмент их не трогает.
func (m *Manager) preflightShared() (*sharedFacts, error) {
	path := filepath.Join(m.SharedDir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("чужой конфиг %s не читается: %v", path, err)
	}
	var cfg struct {
		Inbounds  []map[string]any `json:"inbounds"`
		Outbounds []map[string]any `json:"outbounds"`
		DNS       struct {
			Servers []map[string]any `json:"servers"`
		} `json:"dns"`
		Experimental struct {
			CacheFile struct {
				Enabled bool   `json:"enabled"`
				Path    string `json:"path"`
			} `json:"cache_file"`
			ClashAPI struct {
				ExternalController string `json:"external_controller"`
				CacheFile          string `json:"cache_file"`
			} `json:"clash_api"`
		} `json:"experimental"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("чужой config.json не разбирается: %v", err)
	}
	f := &sharedFacts{InTags: map[string]bool{}, OutTags: map[string]bool{}, Tuns: map[string]bool{}, Addrs: map[string]bool{}, DNSTags: map[string]bool{}, ListenPorts: map[int]bool{}}
	str := func(v any) string { s, _ := v.(string); return s }
	for _, in := range cfg.Inbounds {
		if t := str(in["tag"]); t != "" {
			f.InTags[t] = true
		}
		if t := str(in["interface_name"]); t != "" {
			f.Tuns[t] = true
		}
		if p, ok := in["listen_port"].(float64); ok {
			f.ListenPorts[int(p)] = true
		}
		if a, ok := in["address"].([]any); ok {
			for _, x := range a {
				if s := str(x); s != "" {
					f.Addrs[s] = true
				}
			}
		}
	}
	for _, ob := range cfg.Outbounds {
		if t := str(ob["tag"]); t != "" {
			f.OutTags[t] = true
		}
	}
	for _, s := range cfg.DNS.Servers {
		if t := str(s["tag"]); t != "" {
			f.DNSTags[t] = true
		}
	}
	if c := cfg.Experimental.ClashAPI.ExternalController; c != "" {
		if _, port, err := net.SplitHostPort(c); err == nil {
			f.ClashPort, _ = strconv.Atoi(port)
		}
	}
	if f.ClashPort == 0 {
		f.ClashPort = m.sharedClash
	}
	if cfg.Experimental.CacheFile.Enabled {
		f.CacheFile = cfg.Experimental.CacheFile.Path
		if f.CacheFile == "" {
			f.CacheFile = "cache.db в рабочем каталоге ядра"
		}
	} else if p := cfg.Experimental.ClashAPI.CacheFile; p != "" {
		f.CacheFile = p
	}
	return f, nil
}

// resolverTag - свободный тег для нашего локального DNS-резолвера: в merged
// конфиге массивы дописываются, дубли тегов dns.servers рвут старт ядра.
func (f *sharedFacts) resolverTag() string {
	for _, tag := range []string{"local", "mawg-local", "mawg-local-2"} {
		if !f.DNSTags[tag] {
			return tag
		}
	}
	return "mawg-local"
}

func (m *Manager) writeFragment(data []byte) error {
	tmp := m.fragmentPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, m.fragmentPath()); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (m *Manager) removeFragment() error {
	err := os.Remove(m.fragmentPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (m *Manager) checkShared() error {
	out, err := exec.Command(m.eng.Bin, "check", "-C", m.SharedDir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("check -C %s не прошёл: %s", m.SharedDir, strings.TrimSpace(string(out)))
	}
	return nil
}

func (m *Manager) restartShared(timeout time.Duration) error {
	if m.SharedInit == "" {
		return fmt.Errorf("init-скрипт чужого ядра не задан")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, m.SharedInit, "restart").CombinedOutput()
	if err != nil {
		return fmt.Errorf("рестарт %s не удался: %s", m.SharedInit, strings.TrimSpace(string(out)))
	}
	return m.waitClash(timeout)
}

func (m *Manager) startShared(timeout time.Duration) error {
	if m.SharedInit == "" {
		return fmt.Errorf("init-скрипт чужого ядра не задан")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, m.SharedInit, "start").CombinedOutput()
	if err != nil {
		return fmt.Errorf("старт %s не удался: %s", m.SharedInit, strings.TrimSpace(string(out)))
	}
	return m.waitClash(timeout)
}

// WaitShared - ждёт появления процесса чужого сервиса (pidof) до дедлайна:
// после install-рестарта на mipsle сервис грузится секунды, а Apply должен
// видеть живой процесс (инцидент Кинетика 2026-10-29).
func (m *Manager) WaitShared(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if m.sharedAlive() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// startSharedIfManaged - сервис стоит, но ядро ставили из mawg (маркер):
// поднимаем его сами (слово владельца 2026-10-29: позиция «чужой сервис не
// трогаем» устарела - mawg сам ставит и рестартит это ядро). Без маркера -
// прежний отказ: чужие/подкоп-установки не трогаем.
func (m *Manager) startSharedIfManaged(timeout time.Duration) error {
	if m.MarkerDir == "" || !markerExists(m.MarkerDir) {
		return fmt.Errorf("процесс чужого ядра не запущен - не буду стартовать чужой сервис из mawg, запустите %s", m.SharedInit)
	}
	if _, err := os.Stat(m.eng.Bin); err != nil {
		return fmt.Errorf("маркер mawg есть, но %s не найден - запустите сервис вручную", m.eng.Bin)
	}
	if m.logEvent != nil {
		m.logEvent("singbox", "power", "сервис sing-box остановлен, ядро установлено mawg - запускаю "+m.SharedInit)
	}
	if err := m.startShared(timeout); err != nil {
		return fmt.Errorf("ядро установлено mawg, но сервис не стартовал: %v", err)
	}
	return nil
}

// SharedAlive - жив ли чужой процесс shared-сервиса (для web: решает,
// рестартить ли сервис после подмены бинаря ядра).
func (m *Manager) SharedAlive() bool { return m.sharedAlive() }

func (m *Manager) sharedAlive() bool {
	return len(m.sharedPids()) > 0
}

// SetCacheFile выключает кэш (off) или переносит его в /tmp (tmp): база
// пишется через mmap без остановки - на флешке это постоянный износ.
// Применение через рестарт: bbolt открывает базу только на старте,
// горячего переезда пути не умеет.
func (m *Manager) SetCacheFile(mode string) (string, error) {
	if m.Mode != "shared" {
		return "", fmt.Errorf("управление кэшем нужно только в режиме общего ядра")
	}
	path := filepath.Join(m.SharedDir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	m.backupShared()
	out, old, changed, err := editCacheFile(data, mode)
	if err != nil {
		return "", err
	}
	if !changed {
		if mode == CacheModeOff {
			return "", nil
		}
		m.sharedCacheFile = old
		return old, nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := m.checkShared(); err != nil {
		_ = os.WriteFile(path, data, 0o600)
		return "", fmt.Errorf("check не прошёл, конфиг откачен: %v", err)
	}
	if mode == CacheModeTmp {
		if err := os.MkdirAll(filepath.Dir(CacheTmpPath), 0o755); err != nil {
			return "", err
		}
	}
	if err := m.restartShared(45 * time.Second); err != nil {
		_ = os.WriteFile(path, data, 0o600)
		if rerr := m.checkShared(); rerr == nil {
			_ = m.restartShared(45 * time.Second)
		}
		return "", fmt.Errorf("рестарт не удался, конфиг откачен: %v", err)
	}
	m.sharedCacheFile = ""
	if mode == CacheModeTmp {
		m.sharedCacheFile = CacheTmpPath
	}
	return "ок", nil
}

func (m *Manager) sharedPids() []string {
	out, err := exec.Command("pidof", filepath.Base(m.eng.Bin)).Output()
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

// reloadShared применяет конфиг без рестарта: lx-ядро (1.14) умеет
// SIGHUP-перезачитывание - чужие туннели и соединения не рвутся. Upstream
// 1.13.3 от SIGHUP умирал, поэтому там и при невыжившем процессе - рестарт.
// Для extended консервативно рестарт, пока SIGHUP не проверен живьём.
func (m *Manager) reloadShared(timeout time.Duration) error {
	if !m.eng.LX {
		return m.restartShared(timeout)
	}
	for _, p := range m.sharedPids() {
		if n, conv := strconv.Atoi(p); conv == nil {
			syscall.Kill(n, syscall.SIGHUP)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(m.sharedPids()) == 0 {
			break // сигнал убил процесс - честный рестарт
		}
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/version", m.clashBase()))
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return m.restartShared(timeout)
}

// reservedPorts - порты, занятые службами роутера: чужой inbound на них
// не сможет забиндиться (а если сможет первым - ляжет сосед). Пакетный
// дефолт sing-box слушает shadowsocks на :::8080 - это API MagiTrickle.
func (m *Manager) reservedPorts() map[int]string {
	web := m.webPort
	if web <= 0 {
		web = 8090
	}
	return map[int]string{
		8080:        "API MagiTrickle",
		3553:        "DNS-прокси MagiTrickle",
		web:         "панель mawg",
		m.ClashPort: "clash_api mawg",
	}
}

// freePort подбирает свободный TCP-порт для переноса чужого inbound
// (8388+ - классика shadowsocks, ничего своего рядом нет).
func (m *Manager) freePort() int {
	for p := 8388; p <= 8399; p++ {
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err != nil {
			continue
		}
		l.Close()
		return p
	}
	return 0
}

// ensureSharedClashAPI включает clash_api в чужой config.json (и разгружает
// занятые порты его inbound'ов): без clash_api пробы и переключение узлов
// невозможны, а пакетный дефолт конфига его не ставит.
func (m *Manager) ensureSharedClashAPI() error {
	path := filepath.Join(m.SharedDir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("чужой конфиг %s не читается: %v", path, err)
	}
	out, changed, note, err := editClashAPI(data, m.ClashPort, m.reservedPorts(), m.freePort)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	m.backupShared()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := m.checkShared(); err != nil {
		_ = os.WriteFile(path, data, 0o600)
		return fmt.Errorf("check не прошёл, правка clash_api откачена: %v", err)
	}
	m.patchNote = note
	return nil
}

func (m *Manager) PatchNote() string {
	return m.patchNote
}

// backupShared сохраняет config.json владельца перед каждым apply
// (последние 5 копий в <база mawg>/singbox/shared-backups)
func (m *Manager) backupShared() {
	data, err := os.ReadFile(filepath.Join(m.SharedDir, "config.json"))
	if err != nil {
		return
	}
	bdir := filepath.Join(m.Dir, "shared-backups")
	if err := os.MkdirAll(bdir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(bdir, "config."+time.Now().Format("20060102-150405")+".json"), data, 0o600)
	entries, _ := os.ReadDir(bdir)
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "config.") && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for i := 0; i < len(names)-5; i++ {
		_ = os.Remove(filepath.Join(bdir, names[i]))
	}
}

func (m *Manager) restoreFragment(prev []byte, hadPrev bool) {
	if !hadPrev {
		_ = m.removeFragment()
		return
	}
	_ = m.writeFragment(prev)
}

func (m *Manager) applyShared(runnable []PoolSpec, skipped []string) ([]string, error) {
	facts, err := m.preflightShared()
	if err != nil {
		return skipped, err
	}
	if facts.ClashPort == 0 {
		// clash_api в чужом конфиге нет - включаем сами (с бэкапом и
		// check-ом): без него пробы невозможны, а просить владельца
		// править JSON руками - не наш путь. Сюда попадаем и когда чужой
		// сервис ещё не запущен: патч остаётся в конфиге к его старту.
		if err := m.ensureSharedClashAPI(); err != nil {
			return skipped, fmt.Errorf("не удалось включить clash_api в чужой config.json: %v (исправьте конфиг вручную или используйте свой экземпляр движка)", err)
		}
		if facts, err = m.preflightShared(); err != nil {
			return skipped, err
		}
	}
	m.resolverTag = facts.resolverTag()
	m.sharedCacheFile = facts.CacheFile
	if facts.ClashPort == 0 {
		return skipped, fmt.Errorf("в чужом config.json нет clash_api - пробы невозможны; включите clash_api или используйте свой экземпляр движка")
	}
	for i, spec := range runnable {
		inTag := fmt.Sprintf("tun-in-%d", i+1)
		if facts.InTags[inTag] || facts.InTags["mixed-"+spec.Name] {
			return skipped, fmt.Errorf("тег inbound %s уже занят чужим конфигом", inTag)
		}
		if facts.OutTags["mawg-"+spec.Name] {
			return skipped, fmt.Errorf("тег mawg-%s уже занят чужим конфигом", spec.Name)
		}
		for _, n := range spec.Nodes {
			if facts.OutTags["mawg-"+spec.Name+"|"+n.ConfName()] {
				return skipped, fmt.Errorf("тег узла mawg-%s|%s уже занят чужим конфигом", spec.Name, n.ConfName())
			}
		}
		if facts.Tuns[spec.Tun] {
			return skipped, fmt.Errorf("интерфейс %s уже занят чужим конфигом", spec.Tun)
		}
		if facts.Addrs[spec.TunIP] {
			return skipped, fmt.Errorf("адрес %s уже занят чужим конфигом", spec.TunIP)
		}
		if facts.ListenPorts[spec.MixedPort] {
			return skipped, fmt.Errorf("порт пробы %d уже слушает чужой inbound - освободите его в config.json", spec.MixedPort)
		}
	}
	if !m.sharedAlive() {
		if err := m.startSharedIfManaged(45 * time.Second); err != nil {
			return skipped, err
		}
	}
	m.backupShared()
	prev, prevErr := os.ReadFile(m.fragmentPath())
	hadPrev := prevErr == nil
	if err := m.writeConfig(runnable); err != nil {
		return skipped, err
	}
	// Конфиг байт-в-байт прежний (типовой случай - откат неудачного
	// обновления подписки): перезагрузка холостая, SIGHUP/рестарт общего
	// ядра рвёт сессии соседних пулов (живой кейс trojan-test/телеграм,
	// слово владельца 2026-11-05). Достаточно перепробовать пулы.
	if cur, rerr := os.ReadFile(m.fragmentPath()); rerr == nil && hadPrev && bytes.Equal(prev, cur) {
		m.sharedClash = facts.ClashPort
		m.lastSpecs = runnable
		for _, spec := range runnable {
			m.probeSpec(spec)
			if st, ok := m.PoolStatus(spec.Name); ok && !st.ProbeOK {
				time.Sleep(2 * time.Second)
				m.probeSpec(spec)
			}
		}
		return skipped, nil
	}
	if err := m.checkShared(); err != nil {
		m.restoreFragment(prev, hadPrev)
		return skipped, err
	}
	m.sharedClash = facts.ClashPort
	if err := m.reloadShared(45 * time.Second); err != nil {
		m.restoreFragment(prev, hadPrev)
		if rerr := m.checkShared(); rerr == nil {
			_ = m.restartShared(45 * time.Second)
		}
		return skipped, err
	}
	if cp, err := os.ReadFile(m.fragmentPath()); err == nil {
		_ = os.WriteFile(m.lastGood(), cp, 0o600)
	}
	m.lastSpecs = runnable
	for _, spec := range runnable {
		m.probeSpec(spec)
		if st, ok := m.PoolStatus(spec.Name); ok && !st.ProbeOK {
			time.Sleep(2 * time.Second)
			m.probeSpec(spec)
		}
	}
	return skipped, nil
}
