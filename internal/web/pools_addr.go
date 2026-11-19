package web

// Перенумерация локальных адресов конфигов пула (120-док, слово
// владельца 15.11 «массово менять в них адрес локальный»): пулы одного
// провайдера (Proton 10.2.0.x, WARP 172.16.0.x) разводятся подсетью.
// Явный инструмент вместо тихой автоправки при импорте: конфиг не мутирует
// молча, куча однотипных правится одним ходом.

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"mawg/internal/store"
	"mawg/internal/wgconf"
)

type readdressReq struct {
	// Base - адрес первого конфига («10.2.0.3» или «10.2.0.3/32»),
	// следующим файлам последний октет +1.
	Base string `json:"base,omitempty"`
	// Addresses - явные адреса 1:1 к files (правка по одному: UI шлёт
	// только выбранные строки со своими адресами); не пусто - Base не нужен.
	Addresses []string `json:"addresses,omitempty"`
	// Files - какие конфиги перенумеровать; пусто = ВЕСЬ пул (в порядке
	// ротации): свежая куча как раз выключена конфликтом адресов ещё до
	// первой ротации - по включённым её не достать.
	Files []string `json:"files,omitempty"`
}

type readdressOne struct {
	File    string `json:"file"`
	Old     string `json:"old,omitempty"`
	Address string `json:"address"`
}

// readdressConfigs - POST /pools/{name}/configs/readdress. Проверки ДО
// первой записи (массовая операция - всё или ничего): файлы существуют,
// адреса свободны по ВСЕМ пулам и живым туннелям (два выключенных пула
// с одним адресом конфликт не покажет, пока оба не поднять - поэтому
// реестр, не только туннели).
func (s *Server) readdressConfigs(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req readdressReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || (req.Base == "" && len(req.Addresses) == 0) {
		writeErr(w, fmt.Errorf("нужен base (адрес первого конфига) или addresses (явные адреса к files)"))
		return
	}
	pool, ok := s.store.Pool(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	base := strings.TrimSpace(req.Base)
	ipPart := base
	basePfx := ""
	if i := strings.Index(base, "/"); i >= 0 {
		basePfx = base[i:]
		ipPart = base[:i]
	}
	if base != "" {
		if _, err := wgconf.IncV4(ipPart, 0); err != nil {
			writeErr(w, err)
			return
		}
	}
	cfgByFile := map[string]store.ManagedConfig{}
	var files []string
	if len(req.Files) == 0 {
		for _, c := range pool.Configs {
			files = append(files, c.File)
			cfgByFile[c.File] = c
		}
	} else {
		for _, f := range req.Files {
			c, ok := pool.ConfigByFile(f)
			if !ok {
				writeErr(w, fmt.Errorf("конфиг %q не найден в пуле %q", f, name))
				return
			}
			if _, dup := cfgByFile[f]; !dup {
				cfgByFile[f] = c
				files = append(files, f)
			}
		}
	}
	if len(files) == 0 {
		writeErr(w, fmt.Errorf("перенумеровывать нечего: в пуле нет конфигов"))
		return
	}
	// Адреса заранее: последний не должен вывалиться из /24 (base-режим)
	// или не разбираться (addresses-режим) после того, как первые уже записаны
	var addrs []string
	if len(req.Addresses) > 0 {
		if len(req.Addresses) != len(files) {
			writeErr(w, fmt.Errorf("addresses (%d) и files (%d) разной длины", len(req.Addresses), len(files)))
			return
		}
		for _, a := range req.Addresses {
			if _, err := wgconf.IncV4(strings.SplitN(strings.TrimSpace(a), "/", 2)[0], 0); err != nil {
				writeErr(w, err)
				return
			}
		}
		addrs = req.Addresses
	} else {
		addrs = make([]string, len(files))
		for i := range files {
			inc, err := wgconf.IncV4(ipPart, i)
			if err != nil {
				writeErr(w, err)
				return
			}
			addrs[i] = inc + basePfx
		}
	}
	target := map[string]bool{}
	for _, f := range files {
		target[f] = true
	}
	occupied := s.occupiedAddresses(name, pool.DeviceName(), target)
	var conflicts []string
	for _, a := range addrs {
		if where := occupied[normAddr(a)]; where != "" {
			conflicts = append(conflicts, a+" занят ("+where+")")
		}
	}
	if len(conflicts) > 0 {
		writeErr(w, fmt.Errorf("адреса заняты: %s", strings.Join(conflicts, "; ")))
		return
	}
	var warnings []string
	if base != "" {
		if msg := s.subnetHint(name, files, ipPart); msg != "" {
			warnings = append(warnings, msg)
		}
	}
	changed := make([]readdressOne, 0, len(files))
	for i, f := range files {
		raw, err := os.ReadFile(filepath.Join(s.store.PoolDir(name), f))
		if err != nil {
			writeErr(w, err)
			return
		}
		newRaw, err := wgconf.SetAddress(raw, addrs[i])
		if err != nil {
			writeErr(w, fmt.Errorf("%s: %w", f, err))
			return
		}
		if err := s.store.WriteConfigRaw(name, f, newRaw); err != nil {
			writeErr(w, fmt.Errorf("%s: %w", f, err))
			return
		}
		changed = append(changed, readdressOne{File: f, Old: firstV4(cfgByFile[f].Addresses), Address: addrs[i]})
	}
	var names []string
	for _, c := range changed {
		names = append(names, c.File+" -> "+c.Address)
	}
	s.store.LogEvent(name, "readdress", "адреса перенумерованы: "+strings.Join(names, ", "))
	s.reapplyAfterReaddress(name, files)
	writeJSON(w, http.StatusOK, map[string]any{"changed": changed, "warnings": warnings})
}

// firstV4 - первый IPv4 из реестра адресов конфига (для «было -> стало»).
func firstV4(addrs []string) string {
	for _, a := range addrs {
		if !strings.Contains(a, ":") {
			return normAddr(a)
		}
	}
	return ""
}

// occupiedAddresses - все занятые v4-адреса вне целевых файлов: реестр
// конфигов всех пулов (старые записи добираются из файлов) + живые
// туннели движка; собственный интерфейс пула не считается - он будет
// переприменен с новым адресом.
func (s *Server) occupiedAddresses(poolName, ownDevice string, freeFiles map[string]bool) map[string]string {
	out := map[string]string{}
	for _, p := range s.store.Pools() {
		for _, c := range p.Configs {
			if p.Name == poolName && freeFiles[c.File] {
				continue
			}
			for _, a := range s.poolConfigAddrs(p.Name, c) {
				if na := normAddr(a); na != "" {
					out[na] = "конфиг " + c.File + " пула " + p.Name
				}
			}
		}
	}
	for addr, dev := range s.engine.TunnelAddresses() {
		if dev == "" || dev == ownDevice {
			continue
		}
		if na := normAddr(addr); na != "" {
			out[na] = "интерфейс " + dev
		}
	}
	return out
}

// subnetHint - «адрес вне подсети провайдера» (не блокирует: подсеть -
// рекомендация, живьём владелец проверял только свои диапазоны).
func (s *Server) subnetHint(poolName string, files []string, ipPart string) string {
	for _, f := range files {
		cfg, err := s.store.LoadConfigFile(poolName, f)
		if err != nil {
			continue
		}
		sub := wgconf.ProviderSubnet(wgconf.DetectProvider(cfg))
		if sub == "" {
			continue
		}
		_, ipnet, err := net.ParseCIDR(sub)
		if err != nil {
			continue
		}
		if ip := net.ParseIP(ipPart); ip == nil || !ipnet.Contains(ip) {
			return "адрес " + ipPart + " вне подсети провайдера " + sub + " - проверьте"
		}
		return ""
	}
	return ""
}

// reapplyAfterReaddress - донести новые адреса до туннеля: активный
// конфиг нативного пула переприменяется, движковый - пересборкой; без
// этого интерфейс держит старый адрес до следующей ротации.
func (s *Server) reapplyAfterReaddress(name string, files []string) {
	pool, ok := s.store.Pool(name)
	if !ok || pool.Disabled {
		return
	}
	if pool.Settings.EngineMode != "" {
		if _, err := s.applyEngine(); err != nil {
			s.store.LogEvent(name, "readdress", "движок не пересобран после перенумерации: "+err.Error())
		}
		return
	}
	active := s.store.State(name).ActiveFile
	for _, f := range files {
		if f == active {
			if err := s.activatePoolConfig(name, f); err != nil {
				s.store.LogEvent(name, "readdress", "активный конфиг не переприменен: "+err.Error())
			}
			break
		}
	}
	s.engine.CheckNow(name)
}
