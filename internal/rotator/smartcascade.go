package rotator

// Умный каскад пулов (86-док, дизайн сверен с владельцем 2026-10-24):
// пул A работает напрямую; при блокировке трафик к его эндпоинтам
// заворачивается через первый живой пул Via штатным каскадом
// (cascade.Manager: служебная группа + OUTPUT-хук) - пользователь
// сохраняет IP A. Возврат на прямой путь - по интервалу/расписанию/
// кнопке: короткое окно без OUTPUT-хука (BeforeMutate/AfterMutate, та же
// дисциплина, что у любой записи групп), инкапсуляция A уходит напрямую,
// штатная проба пула решает. Отдельного failCycles нет: триггер -
// исчерпание конфигов в rotate() (порог FailThreshold уже отработал).
// Отличать от фоллбэка групп (Fallback): тот переводит ГРУППЫ на другое
// устройство, умный каскад сохраняет туннель A живым через Via.

import (
	"context"
	"fmt"
	"time"

	"mawg/internal/store"
)

// smartEnsureRetry - ретрай создания служебной группы, если при
// сохранении настроек MT был недоступен (EnsureErr в стейте).
const smartEnsureRetry = 5 * time.Minute

func smartConfigured(p store.Pool) bool {
	return p.Settings.SmartCascade != nil && len(p.Settings.SmartCascade.Via) > 0
}

// smartMutate - правка состояния автомата с ленивым созданием.
func (e *Engine) smartMutate(name string, fn func(*store.SmartCascadeState)) {
	e.store.MutateState(name, func(s *store.PoolState) {
		if s.Smart == nil {
			s.Smart = &store.SmartCascadeState{}
		}
		fn(s.Smart)
	})
}

// smartEntry - запись каскада, управляемая автоматом пула: сначала по
// запомненному Group (наша), иначе - чужая с тем же source+via (адопт).
func (e *Engine) smartEntry(p store.Pool, st *store.PoolState) (store.CascadeEntry, bool) {
	wantSrc := "pool:" + p.Name
	for _, c := range e.store.Cascades() {
		if st.Smart != nil && st.Smart.Group != "" && c.Group == st.Smart.Group {
			return c, true
		}
	}
	via := ""
	if st.Smart != nil && st.Smart.On {
		via = st.Smart.Via
	}
	if via == "" && p.Settings.SmartCascade != nil && len(p.Settings.SmartCascade.Via) > 0 {
		via = p.Settings.SmartCascade.Via[0]
	}
	for _, c := range e.store.Cascades() {
		if c.Source == wantSrc && c.Via == "pool:"+via {
			return c, true
		}
	}
	return store.CascadeEntry{}, false
}

// pickSmartVia - первый живой пул из списка (Mode==up, не Disabled);
// skip - неработающий Via (перебор при повторном спасении), если есть
// другие живые. Все лежат - "".
func (e *Engine) pickSmartVia(via []string, skip string) string {
	firstHealthy := ""
	for _, name := range via {
		p, ok := e.store.Pool(name)
		if !ok || p.Disabled {
			continue
		}
		if e.store.State(name).Mode != store.ModeUp {
			continue
		}
		if firstHealthy == "" {
			firstHealthy = name
		}
		if skip != "" && name == skip {
			continue
		}
		return name
	}
	if skip == "" {
		return firstHealthy
	}
	return firstHealthy // живых кроме skip нет - прежний остаётся единственным
}

// smartEnable - включить каскад A→Via с верификацией пробой (под actMu).
// reason - для события («прямой доступ потерян» / «вручную»).
func (e *Engine) smartEnable(p store.Pool, reason string) error {
	sc := p.Settings.SmartCascade
	if sc == nil || e.casc == nil {
		return fmt.Errorf("умный каскад не настроен")
	}
	st := e.store.State(p.Name)
	skip := ""
	if st.Smart != nil && st.Smart.On {
		skip = st.Smart.Via
	}
	via := e.pickSmartVia(sc.Via, skip)
	if via == "" {
		return fmt.Errorf("все запасные пулы лежат (%s)", fmtList(sc.Via))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	entry, ok := e.smartEntry(p, st)
	if ok && entry.Via != "pool:"+via {
		// наша запись со старым Via (сменили настройки/перебор): заменить;
		// чужие записи не трогаем - рядом создастся новая
		if st.Smart != nil && st.Smart.Group == entry.Group {
			if err := e.casc.Delete(ctx, entry.Group); err != nil {
				return fmt.Errorf("не заменил каскад на via %s: %v", via, err)
			}
			ok = false
		}
	}
	if !ok {
		created, err := e.casc.Create(ctx, "pool:"+p.Name, "pool:"+via, nil)
		if err != nil {
			return fmt.Errorf("каскад через %s не создался: %v", via, err)
		}
		entry = store.CascadeEntry{Group: created.ID, Source: "pool:" + p.Name, Via: "pool:" + via}
	} else if err := e.casc.SetEnabled(ctx, entry.Group, true); err != nil {
		return fmt.Errorf("каскад через %s не включился: %v", via, err)
	}
	now := e.now()
	e.smartMutate(p.Name, func(s *store.SmartCascadeState) {
		s.On = true
		s.Via = via
		s.Group = entry.Group
		s.Since = now
		s.LastDetail = reason
		s.EnsureErr = ""
	})
	e.scheduleNextCheck(p)
	// верификация: хендшейк через Via требует секунд - лучшая из двух проб
	// (прецедент политики e8a9a48); не ожил - снять и не мешать фоллбэку
	if okProbe, detail := e.smartBestProbe(p); okProbe {
		e.store.LogEvent(p.Name, "smart-cascade",
			"каскад включён: "+reason+", путь через "+via+" ("+detail+")")
		return nil
	}
	e.smartDisable(p, "туннель не ожил через "+via)
	return fmt.Errorf("туннель через %s не ожил", via)
}

// smartDisable - снять каскад (под actMu). reason - для события.
func (e *Engine) smartDisable(p store.Pool, reason string) {
	st := e.store.State(p.Name)
	if st.Smart == nil || !st.Smart.On {
		return
	}
	if entry, ok := e.smartEntry(p, st); ok {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := e.casc.SetEnabled(ctx, entry.Group, false); err != nil {
			e.store.LogEvent(p.Name, "smart-cascade", "не выключил каскад: "+err.Error())
		}
	}
	now := e.now()
	e.smartMutate(p.Name, func(s *store.SmartCascadeState) {
		s.On = false
		s.Since = now
		s.LastDetail = reason
		s.NextCheck = time.Time{}
	})
	e.store.LogEvent(p.Name, "smart-cascade", "каскад выключен: "+reason)
}

// smartRescue - спасение исчерпавшегося пула (вызывается из rotate под
// actMu): true = пул обработан (остаётся ModeUp со своим IP), false -
// обычный enterFallback пусть решает, как настроен Fallback.
func (e *Engine) smartRescue(p store.Pool) bool {
	if !smartConfigured(p) || e.casc == nil {
		return false
	}
	st := e.store.State(p.Name)
	if st.Smart != nil && st.Smart.On {
		// каскад уже работал, а пул всё равно мёртв: Via-путь не спасает -
		// снять и искать следующий живой Via (перебор списка)
		e.smartDisable(p, "туннель не ожил через "+st.Smart.Via)
	}
	if err := e.smartEnable(p, "прямой доступ потерян"); err != nil {
		e.store.LogEvent(p.Name, "smart-cascade", "каскад не включён: "+err.Error())
		return false
	}
	return true
}

// smartBestProbe - лучшая из двух проб с паузой между (хендшейк через
// Via/напрямую должен успеть случиться за неё).
func (e *Engine) smartBestProbe(p store.Pool) (bool, string) {
	if verdict, detail := e.probe(p); verdict != probeFail {
		return true, detail
	}
	pause := e.smartPause
	if pause <= 0 {
		pause = 5 * time.Second
	}
	time.Sleep(pause)
	verdict, detail := e.probe(p)
	return verdict != probeFail, detail
}

// smartReturn - проверка прямого пути при живом каскаде (под actMu).
// manual=true (кнопка) минует антифлап-гейт dwell. Проба идёт в коротком
// окне без OUTPUT-хука: BeforeMutate снимает хуки sweep'ом (все каскады
// на секунды, как у любой записи групп), инкапсуляция A уходит напрямую.
func (e *Engine) smartReturn(p store.Pool, manual bool) {
	st := e.store.State(p.Name)
	if st.Smart == nil || !st.Smart.On || p.Settings.SmartCascade == nil {
		return
	}
	sc := p.Settings.SmartCascade
	if !manual {
		if sc.MinDwellMin > 0 && e.now().Sub(st.Smart.Since) < time.Duration(sc.MinDwellMin)*time.Minute {
			return // антифлап: слишком рано после включения
		}
		if e.wanDown() {
			e.smartMutate(p.Name, func(s *store.SmartCascadeState) {
				s.LastDetail = "канал провайдера недоступен - проверка прямого перенесена"
			})
			e.scheduleNextCheck(p)
			return
		}
	}
	entry, ok := e.smartEntry(p, st)
	if !ok {
		// запись каскада удалена руками - просто синхронизировать стейт
		e.smartMutate(p.Name, func(s *store.SmartCascadeState) { s.On = false })
		return
	}
	m := e.casc
	m.BeforeMutate(nil) // окно: хуки сняты, SyncHooks молчит
	directOK, detail := e.smartBestProbe(p)
	if directOK {
		// выключаем ВНУТРИ окна: группа off - SyncHooks после окна хук
		// уже не вернёт (SetEnabled снимет отсутствующий хук без ошибки)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := e.casc.SetEnabled(ctx, entry.Group, false); err != nil {
			e.store.LogEvent(p.Name, "smart-cascade", "не выключил каскад: "+err.Error())
		}
		cancel()
		now := e.now()
		e.smartMutate(p.Name, func(s *store.SmartCascadeState) {
			s.On = false
			s.Since = now
			s.LastDetail = "прямой вернулся (" + detail + ")"
		})
		e.store.LogEvent(p.Name, "smart-cascade",
			"прямой доступ вернулся ("+detail+") - каскад снят, "+smartReturnPlan(sc))
	} else {
		e.smartMutate(p.Name, func(s *store.SmartCascadeState) {
			s.LastDetail = "прямой ещё недоступен (" + detail + ")"
		})
		e.store.LogEvent(p.Name, "smart-cascade",
			"прямой ещё недоступен ("+detail+") - каскад остаётся")
	}
	m.AfterMutate() // SyncHooks: включённым вернёт хук+исключения, выключенным снимет
	e.scheduleNextCheck(p)
}

// smartReturnPlan - человекочитаемый план следующего возврата.
func smartReturnPlan(sc *store.SmartCascadeSettings) string {
	switch sc.Return.Mode {
	case "interval":
		h := sc.Return.IntervalH
		if h <= 0 {
			h = 6
		}
		return fmt.Sprintf("следующая проверка через %d ч", h)
	case "manual":
		return "дальше только кнопкой"
	default:
		times := sc.Return.TimeOfDay
		if len(times) == 0 {
			times = []string{"03:00"}
		}
		return "возврат по расписанию " + fmtList(times)
	}
}

// scheduleNextCheck - время следующей автоматической проверки прямого
// (interval: +IntervalH; schedule: ближайшее HH:MM из TimeOfDay; manual:
// никогда - только кнопка).
func (e *Engine) scheduleNextCheck(p store.Pool) {
	sc := p.Settings.SmartCascade
	if sc == nil {
		return
	}
	now := e.now()
	var next time.Time
	switch sc.Return.Mode {
	case "interval":
		h := sc.Return.IntervalH
		if h <= 0 {
			h = 6
		}
		next = now.Add(time.Duration(h) * time.Hour)
	case "manual":
		next = time.Time{}
	default:
		next = nextTimeOfDay(now, sc.Return.TimeOfDay)
	}
	e.smartMutate(p.Name, func(s *store.SmartCascadeState) { s.NextCheck = next })
}

// nextTimeOfDay - ближайшее наступающее время из списка "HH:MM".
func nextTimeOfDay(now time.Time, times []string) time.Time {
	best := time.Time{}
	for _, t := range times {
		h, m, ok := store.ParseTimeOfDay(t)
		if !ok {
			continue
		}
		cand := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
		if !cand.After(now) {
			cand = cand.Add(24 * time.Hour)
		}
		if best.IsZero() || cand.Before(best) {
			best = cand
		}
	}
	return best
}

// checkSmartCascades - минутная домашка умных каскадов (нечётные тики):
// долечить несозданные служебные группы (MT был недоступен при
// сохранении настроек) и прогнать наступившие проверки возврата.
func (e *Engine) checkSmartCascades() {
	if e.casc == nil {
		return
	}
	now := e.now()
	for _, p := range e.store.Pools() {
		if !smartConfigured(p) {
			continue
		}
		st := e.store.State(p.Name)
		if st.Smart == nil || !st.Smart.On {
			e.smartEnsure(p)
			continue
		}
		if st.Smart.NextCheck.IsZero() || now.Before(st.Smart.NextCheck) {
			continue
		}
		e.actMu.Lock()
		e.smartReturn(p, false)
		e.actMu.Unlock()
	}
}

// smartEnsure - служебная группа каскада существует и ВЫКЛЮЧЕНА, пока
// автомат не сработал (слово владельца: создавать при сохранении
// настроек, в момент аварии лишних записей в MT нет). Смена Via[0] при
// выключенном автомате - пересоздание. Ошибка - EnsureErr в стейте,
// ретрай раз в smartEnsureRetry.
func (e *Engine) smartEnsure(p store.Pool) {
	st := e.store.State(p.Name)
	wantVia := p.Settings.SmartCascade.Via[0]
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ours, hasOurs := e.smartEntry(p, st)
	if hasOurs {
		own := st.Smart != nil && st.Smart.Group == ours.Group
		if own && ours.Via != "pool:"+wantVia {
			if err := e.casc.Delete(ctx, ours.Group); err != nil {
				e.store.LogEvent(p.Name, "smart-cascade", "не заменил каскад на via "+wantVia+": "+err.Error())
				return
			}
			hasOurs = false
		}
	}
	if !hasOurs {
		if st.Smart != nil && st.Smart.EnsureErr != "" && !st.Smart.EnsureAt.IsZero() &&
			e.now().Sub(st.Smart.EnsureAt) < smartEnsureRetry {
			return
		}
		created, err := e.casc.Create(ctx, "pool:"+p.Name, "pool:"+wantVia, nil)
		if err != nil {
			e.smartMutate(p.Name, func(s *store.SmartCascadeState) {
				s.EnsureErr, s.EnsureAt = err.Error(), e.now()
			})
			return
		}
		// Create создаёт включённой - сразу гасим до триггера
		if err := e.casc.SetEnabled(ctx, created.ID, false); err != nil {
			e.store.LogEvent(p.Name, "smart-cascade", "группа каскада создана, но не выключилась: "+err.Error())
		}
		ours, hasOurs = store.CascadeEntry{Group: created.ID, Source: "pool:" + p.Name, Via: "pool:" + wantVia}, true
		e.store.LogEvent(p.Name, "smart-cascade",
			"служебная группа каскада готова (выключена, путь через "+wantVia+")")
	}
	e.smartMutate(p.Name, func(s *store.SmartCascadeState) {
		s.Group = ours.Group
		s.EnsureErr = ""
	})
}

// EnsureSmartCascade - ручной запуск создания группы из web (сохранение
// настроек пула); ошибки не фатальны - домашка долечит.
func (e *Engine) EnsureSmartCascade(name string) {
	if e.casc == nil {
		return
	}
	if p, ok := e.store.Pool(name); ok && smartConfigured(p) {
		e.smartEnsure(p)
	}
}

// SmartCascadeCheckNow - кнопка «проверить прямой сейчас»: гейт dwell
// и расписание минуются (слово владельца), wanDown честно переносит.
func (e *Engine) SmartCascadeCheckNow(name string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(name)
	if !ok || !smartConfigured(p) {
		return fmt.Errorf("умный каскад пула %q не настроен", name)
	}
	st := e.store.State(name)
	if st.Smart == nil || !st.Smart.On {
		return fmt.Errorf("каскад не активен")
	}
	e.smartReturn(p, true)
	return nil
}

// SmartCascadeSetManual - кнопки «завернуть в каскад» / «вернуть
// напрямую»: явное слово владельца, гейты и верификация как в автомате.
func (e *Engine) SmartCascadeSetManual(name string, on bool) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(name)
	if !ok || !smartConfigured(p) || e.casc == nil {
		return fmt.Errorf("умный каскад пула %q не настроен", name)
	}
	if on {
		return e.smartEnable(p, "вручную")
	}
	e.smartDisable(p, "возвращено вручную")
	return nil
}

// SmartCascadeSyncToggle - ручной тумблер служебной группы каскада в
// списке групп: держит зеркало-стейт автомата в согласии с MT.
func (e *Engine) SmartCascadeSyncToggle(groupID string, enable bool) {
	entry, ok := e.store.CascadeByGroup(groupID)
	if !ok {
		return
	}
	for _, p := range e.store.Pools() {
		if entry.Source != "pool:"+p.Name {
			continue
		}
		now := e.now()
		e.smartMutate(p.Name, func(s *store.SmartCascadeState) {
			s.On = enable
			s.Group = groupID
			s.Since = now
			if enable {
				s.Via = entry.Via
				if v, ok2 := store.FallbackPoolName(entry.Via); ok2 {
					s.Via = v
				}
			} else {
				s.NextCheck = time.Time{}
			}
		})
		if enable {
			e.scheduleNextCheck(p)
		}
		return
	}
}

func fmtList(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
