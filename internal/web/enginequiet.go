package web

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"mawg/internal/magitrickle"
	"mawg/internal/store"
)

// Движковые пулы без маршрутизации не должны тревожить общий движок:
// обновление подписки такого пула пишет узлы, не пересобирая ядро
// (SIGHUP/рестарт рвёт сессии соседних пулов - живой кейс trojan-test
// против телеграма, слово владельца 2026-11-05).

// enginePoolRouted - используется ли пул хоть какой-то маршрутизацией:
// группа MagiTrickle на его tun (включая выключенные - включение группы
// не должно застать пул со старыми узлами), умный каскад (свой или
// чужой Via по имени пула) или бандл устройств. Нет данных по МТ -
// считаем используемым (fail-safe: лучше лишняя пересборка, чем трафик
// на устаревшие узлы).
func (s *Server) enginePoolRouted(p store.Pool) bool {
	iface := p.Settings.TunName
	if iface == "" {
		return true
	}
	groups, ok := s.mtGroupsForRouting()
	if !ok {
		return true
	}
	for _, g := range groups {
		if g.Interface == iface {
			return true
		}
	}
	// умный каскад: Via - имена пулов
	for _, q := range s.store.Pools() {
		if q.Settings.SmartCascade == nil {
			continue
		}
		if q.Name == p.Name {
			return true
		}
		for _, via := range q.Settings.SmartCascade.Via {
			if via == p.Name {
				return true
			}
		}
	}
	// бандлы устройств (MT-C): members - интерфейсы пулов
	for _, b := range s.store.Bundles() {
		for _, m := range b.Members {
			if m == iface {
				return true
			}
		}
	}
	return false
}

// mtGroupsForRouting - группы МТ для предиката: сперва тень (без HTTP,
// обновляется мониторами и мутациями), при пустой тени - живой список.
// Второе значение false - данных нет (МТ недоступен).
func (s *Server) mtGroupsForRouting() ([]magitrickle.Group, bool) {
	if groups := magitrickle.ShadowGroups(); len(groups) > 0 {
		return groups, true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if groups, err := s.mtClient().Groups(ctx); err == nil {
		return groups, true
	}
	return nil, false
}

// engineCatchUpStale - досборка движка, когда «спящий» пул (узлы
// обновлены без пересборки) получил привязки: nodes.json пула новее
// применённого конфига ядра. Одна досборка за тик; перезапуск mawg
// детект не теряет - всё решают mtime файлов.
func (s *Server) engineCatchUpStale() {
	for _, p := range s.store.Pools() {
		if p.Disabled || p.Settings.EngineMode != engineMode {
			continue
		}
		mi, err := os.Stat(filepath.Join(s.store.Base(), "pools", p.Name, "nodes.json"))
		if err != nil {
			continue
		}
		mgr, err := s.engineMgr(p)
		if err != nil {
			continue
		}
		if !mgr.FragmentMTime().Before(mi.ModTime()) {
			continue // конфиг ядра не старее узлов - всё применено
		}
		if !s.enginePoolRouted(p) {
			continue
		}
		if _, aerr := s.applyEngine(); aerr != nil {
			s.store.LogEvent(p.Name, "subupdate", "досборка движка не удалась: "+aerr.Error())
			return
		}
		s.store.LogEvent(p.Name, "subupdate", "движок дособран: у пула появились привязки, свежие узлы применены")
		return
	}
}
