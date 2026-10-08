package web

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"mawg/internal/links"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

const (
	subCheckEvery   = time.Minute
	subFailCooldown = 30 * time.Minute
	subDefaultH     = 24
	subProbeWait    = 60 * time.Second
)

// subRefreshBusy - singleflight на пул: цикл и ручное обновление
// не должны пересобирать пул одновременно.
var subRefreshBusy sync.Map

// SubUpdateLoop - автообновление подписок: плановое (интервал - настройка
// пула или Profile-Update-Interval подписки) и при деградации пула. Тело
// подписки гейтится хешем: не изменилось - пул не трогаем.
func (s *Server) SubUpdateLoop(ctx context.Context) {
	t := time.NewTicker(subCheckEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.subUpdateTick()
		}
	}
}

func (s *Server) subUpdateTick() {
	for _, p := range s.store.Pools() {
		if p.Disabled || !autoUpdatable(p.Settings.Source) {
			continue
		}
		if p.Settings.EngineMode == engineMode {
			mgr, err := s.sb()
			if err != nil {
				continue
			}
			// waits-lx: обновление источника такие узлы не поднимет
			if ps, ok := mgr.PoolStatus(p.Name); ok && ps.Reason == "waits-lx" {
				continue
			}
		}
		st := s.store.State(p.Name)
		if s.subDegraded(p, st) {
			if !st.LastFailRefresh.IsZero() && time.Since(st.LastFailRefresh) < subFailCooldown {
				continue
			}
			s.subRefreshPool(p, "пул деградировал")
			// кулдаун считается от попытки, чем бы она ни кончилась
			s.store.MutateState(p.Name, func(x *store.PoolState) { x.LastFailRefresh = time.Now() })
			continue
		}
		interval := subIntervalH(p.Settings.UpdateIntervalH, st.SubIntervalH)
		if st.SubRefreshAt.IsZero() {
			// первый тик только ставит базовую точку: пул могли собрать руками
			s.store.MutateState(p.Name, func(x *store.PoolState) { x.SubRefreshAt = time.Now() })
			s.store.LogEvent(p.Name, "subupdate", fmt.Sprintf("автообновление источника включено, интервал %s", fmtHours(interval)))
			continue
		}
		if time.Since(st.SubRefreshAt) >= time.Duration(interval*float64(time.Hour)) {
			s.subRefreshPool(p, "по расписанию")
		}
	}
}

// subDegraded - пул деградировал N циклов подряд (проба движка или ротатора).
func (s *Server) subDegraded(p store.Pool, st *store.PoolState) bool {
	thr := p.Settings.FailThreshold
	if thr <= 0 {
		thr = 3
	}
	if thr < 2 {
		thr = 2
	}
	if p.Settings.EngineMode == engineMode {
		mgr, err := s.sb()
		if err != nil {
			return false
		}
		ps, ok := mgr.PoolStatus(p.Name)
		return ok && ps.ConsecFails >= thr
	}
	return st.ConsecFails >= thr
}

func subIntervalH(setting, fromSub float64) float64 {
	if setting > 0 {
		return setting
	}
	if fromSub > 0 {
		return fromSub
	}
	return subDefaultH
}

func fmtHours(h float64) string {
	if h == float64(int64(h)) {
		return fmt.Sprintf("%dч", int64(h))
	}
	return fmt.Sprintf("%.1fч", h)
}

// autoUpdatable - источник можно перекачивать: URL подписки или ключ
// Амнезии. Вставленный руками текст/одиночная ссылка не перекачиваются.
func autoUpdatable(src string) bool {
	return strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") ||
		strings.HasPrefix(src, "vpn://")
}

type subApplyVerdict int

const (
	subApplyOK subApplyVerdict = iota
	subApplyFail
	subApplyUnverified // проба не успела подтвердить - оставляем новое
)

// subRefreshPool - полный цикл обновления: скачать (гейт по хешу) ->
// бэкап каталога пула -> применить -> проба -> откат при провале.
func (s *Server) subRefreshPool(p store.Pool, reason string) {
	name := p.Name
	if _, busy := subRefreshBusy.LoadOrStore(name, struct{}{}); busy {
		return
	}
	defer subRefreshBusy.Delete(name)

	res, err := s.resolvePoolSource(context.Background(), p, p.Settings.Source, "")
	if err != nil {
		s.subRefreshFailed(name, reason, err.Error())
		return
	}
	bodyHash, intervalH := res.bodyHash, res.intervalH
	st := s.store.State(name)
	if bodyHash != "" && bodyHash == st.LastSubHash {
		s.store.MutateState(name, func(x *store.PoolState) {
			x.SubRefreshAt = time.Now()
			if intervalH > 0 {
				x.SubIntervalH = intervalH
			}
		})
		s.store.LogEvent(name, "subupdate", "обновление источника ("+reason+"): тело не изменилось, пул не тронут")
		return
	}
	snap, err := s.store.SnapshotPool(name)
	if err != nil {
		s.subRefreshFailed(name, reason, "бэкап не удался: "+err.Error())
		return
	}
	verdict, detail := s.applySourceToPool(p, res.sub)
	switch verdict {
	case subApplyOK:
		s.store.MutateState(name, func(x *store.PoolState) {
			x.SubRefreshAt = time.Now()
			if intervalH > 0 {
				x.SubIntervalH = intervalH
			}
			if bodyHash != "" {
				x.LastSubHash = bodyHash
			}
			x.RefreshFails = 0
			x.LastRefreshErr = ""
		})
		s.store.LogEvent(name, "subupdate", fmt.Sprintf("источник обновлён (%s): %s", reason, detail))
	case subApplyUnverified:
		s.store.MutateState(name, func(x *store.PoolState) {
			x.SubRefreshAt = time.Now()
			if bodyHash != "" {
				x.LastSubHash = bodyHash
			}
		})
		s.store.LogEvent(name, "subupdate", fmt.Sprintf("источник обновлён (%s), но проба не подтвердила за %s: %s", reason, subProbeWait, detail))
	default:
		if rerr := s.store.RestorePoolSnapshot(snap); rerr != nil {
			detail += "; ОТКАТ НЕ УДАЛСЯ: " + rerr.Error()
		} else if p.Settings.EngineMode != engineMode {
			s.reactivateOldNative(name, snap)
		} else if _, aerr := s.applyEngine(); aerr != nil {
			detail += "; откат применён, движок не пересобран: " + aerr.Error()
		}
		s.store.MutateState(name, func(x *store.PoolState) {
			x.RefreshFails++
			x.LastRefreshErr = detail
		})
		s.store.LogEvent(name, "subupdate", fmt.Sprintf("обновление источника (%s) откачено: %s", reason, detail))
	}
}

func (s *Server) subRefreshFailed(name, reason, detail string) {
	s.store.MutateState(name, func(x *store.PoolState) {
		x.RefreshFails++
		x.LastRefreshErr = detail
	})
	s.store.LogEvent(name, "subupdate", fmt.Sprintf("обновление источника (%s) не удалось: %s", reason, detail))
}

// reactivateOldNative - после отката нативного пула вернуть прежний активный
// конфиг в работу (снимок вернул файлы, применить должен ротатор).
func (s *Server) reactivateOldNative(name string, snap *store.PoolSnapshot) {
	if snap.ActiveFile == "" {
		return
	}
	for _, c := range snap.Configs {
		if c.File == snap.ActiveFile {
			if err := s.engine.SetActive(name, snap.ActiveFile); err != nil {
				s.store.LogEvent(name, "subupdate", "прежний конфиг не реактивирован: "+err.Error())
			}
			return
		}
	}
}

// applySourceToPool применяет разобранный источник к пулу и выносит вердикт:
// движковые пулы пробуются синхронно в applyEngine, нативные - ждём свежую
// проверку ротатора.
func (s *Server) applySourceToPool(p store.Pool, res store.Sub) (subApplyVerdict, string) {
	name := p.Name
	if len(res.Nodes) == 0 {
		return subApplyFail, "в источнике нет узлов"
	}
	if p.Settings.EngineMode == engineMode {
		if err := s.writePoolNodes(name, res.Nodes); err != nil {
			return subApplyFail, err.Error()
		}
		skipped, err := s.applyEngine()
		if err != nil {
			return subApplyFail, err.Error()
		}
		mgr, mgrErr := s.sb()
		if mgrErr != nil {
			return subApplyUnverified, mgrErr.Error()
		}
		ps, ok := mgr.PoolStatus(name)
		if !ok {
			return subApplyUnverified, "проба не выполнялась"
		}
		if len(skipped) > 0 {
			return subApplyFail, strings.Join(skipped, "; ")
		}
		if ps.ProbeOK {
			return subApplyOK, fmt.Sprintf("%d узлов, проба ok (%dms)", len(res.Nodes), ps.ProbeMs)
		}
		return subApplyFail, "проба не прошла: " + ps.ProbeErr
	}

	pool, ok := s.store.Pool(name)
	if !ok {
		return subApplyFail, "пул исчез"
	}
	for _, c := range pool.Configs {
		if err := s.store.RemoveConfig(name, c.File); err != nil {
			return subApplyFail, err.Error()
		}
	}
	ncs, engineNodes, skipped := buildNativeConfigs(res.Nodes)
	if len(ncs) == 0 {
		detail := "в источнике нет нативных конфигов"
		if len(engineNodes) > 0 {
			detail += fmt.Sprintf(" (узлов движка: %d)", len(engineNodes))
		}
		if len(skipped) > 0 {
			detail += "; " + strings.Join(skipped, "; ")
		}
		return subApplyFail, detail
	}
	if _, _, err := s.store.AddConfigs(name, ncs); err != nil {
		return subApplyFail, err.Error()
	}
	fresh, _ := s.store.Pool(name)
	if len(fresh.Configs) > 0 {
		if err := s.engine.SetActive(name, fresh.Configs[0].File); err != nil {
			return subApplyFail, "активация не удалась: " + err.Error()
		}
	}
	s.engine.CheckNow(name)
	deadline := time.Now().Add(subProbeWait)
	start := time.Now()
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		st := s.store.State(name)
		if st.LastCheck.After(start) && !strings.Contains(st.LastResult, "settling") {
			if st.ConsecFails == 0 {
				return subApplyOK, fmt.Sprintf("%d конфигов, проба ротатора ok", len(ncs))
			}
			return subApplyFail, "проба ротатора не прошла: " + st.LastResult
		}
	}
	return subApplyUnverified, "ротатор не подтвердил пробу за " + subProbeWait.String()
}

// poolSourceResult - свежие узлы источника пула + данные для гейта и плана.
type poolSourceResult struct {
	sub       store.Sub
	amnezia   *amneziaPlanMeta
	bodyHash  string
	intervalH float64
}

// resolvePoolSource получает свежие узлы из источника пула: URL подписки
// или vpn://-ключ Амнезии (тот же ключ устройства - без новой выдачи).
// Для http возвращается хеш тела и интервал из заголовка подписки.
func (s *Server) resolvePoolSource(ctx context.Context, p store.Pool, src, country string) (poolSourceResult, error) {
	out := poolSourceResult{intervalH: 0}
	if key, ok := links.IsAmneziaKey(src); ok {
		xopts := links.ExchangeOptions{ServerCountryCode: strings.TrimSpace(country)}
		if key.ServiceProtocol == "vless" {
			if ns, err := s.readPoolNodes(p.Name); err == nil && len(ns) > 0 {
				xopts.VlessUUID = ns[0].UUID
			}
		} else if len(p.Configs) > 0 {
			if data, err := os.ReadFile(filepath.Join(s.store.PoolDir(p.Name), p.Configs[0].File)); err == nil {
				if cfg, perr := wgconf.Parse(data); perr == nil {
					xopts.ClientPrivKey = cfg.PrivateKey
				}
			}
		}
		xr, err := links.ExchangeAmneziaKey(ctx, src, xopts)
		if err != nil {
			return out, err
		}
		// хеш выдачи: по содержимому узлов (тот же ключ -> тот же конфиг)
		raw, _ := json.Marshal(xr.Nodes)
		out.sub = store.Sub{Source: src, Nodes: xr.Nodes, RefreshedAt: time.Now(),
			Warnings: []string{fmt.Sprintf("ключ Amnezia %s обменян на свежий конфиг у gateway", strings.TrimPrefix(key.ServiceType, "amnezia-"))}}
		out.amnezia = amneziaFromExchange(&xr)
		out.bodyHash = fmt.Sprintf("%x", sha256.Sum256(raw))
		return out, nil
	}
	if !strings.HasPrefix(src, "http://") && !strings.HasPrefix(src, "https://") {
		res, err := resolveSource(ctx, src)
		out.sub = res
		return out, err
	}
	f, err := links.Fetch(ctx, src)
	if err != nil {
		out.sub = store.Sub{Source: src, RefreshedAt: time.Now()}
		return out, err
	}
	out.sub = store.Sub{Source: src, RefreshedAt: time.Now(), Info: f.Sub, Nodes: f.Result.Nodes, Warnings: f.Result.Warnings}
	out.bodyHash = f.BodySHA
	out.intervalH = f.Sub.UpdateIntervalHours
	return out, nil
}

// recordSourceRefresh - после ручного «обновить из источника» фиксируем хеш
// и время, чтобы цикл не считал следующий плановый тик просроченным.
func (s *Server) recordSourceRefresh(name, bodyHash string, intervalH float64) {
	s.store.MutateState(name, func(x *store.PoolState) {
		x.SubRefreshAt = time.Now()
		if intervalH > 0 {
			x.SubIntervalH = intervalH
		}
		if bodyHash != "" {
			x.LastSubHash = bodyHash
		}
		x.RefreshFails = 0
		x.LastRefreshErr = ""
	})
}
