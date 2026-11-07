package rotator

import (
	"context"
	"fmt"
	"time"

	"mawg/internal/magitrickle"
)

// probeMagitrickleCompatibility - проба совместимости при старте:
// идемпотентный no-op PUT того же списка групп со сравнением до/после.
// Глючная сборка MT усекает список (инцидент 2026-10-10) - флаг в панель
// со ссылкой на форк и рестарт демона, чтобы группы собрать с диска.
// Первый PUT после старта на Кинетике часто валится 500-м (грязные
// nf-объекты после прошлого сеанса - известная история) - это НЕ глючная
// сборка, а транзиент: рестарт демона + одна повторная попытка, флаг
// только если не помогло. Ждём поднятия демона до 2 минут (на буте
// иногда виснет). После первой пробы - вотчер версии: смена версии MT
// (обновили mtinstall'ом) перепроверяет флаг без рестарта панели.
func (e *Engine) probeMagitrickleCompatibility(ctx context.Context) {
	if !e.waitMagitrickleAlive(2 * time.Minute) {
		return
	}
	e.runMagitrickleProbe(ctx)
	go e.watchMagitrickleVersion(ctx)
}

// watchMagitrickleVersion - дешёвый GET /version раз в 10 минут; смена
// версии MT = повторная проба совместимости (снимает липкий флаг после
// обновления MT без рестарта панели).
func (e *Engine) watchMagitrickleVersion(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.maybeReprobeOnVersionChange(ctx)
		}
	}
}

// maybeReprobeOnVersionChange - одна проверка версии: изменилась -
// повторная проба. true = проба выполнялась.
func (e *Engine) maybeReprobeOnVersionChange(ctx context.Context) bool {
	vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	v, err := e.mt.GetVersion(vctx)
	cancel()
	if err != nil || v == "" || v == e.lastProbeVersion {
		return false
	}
	if !e.waitMagitrickleAlive(2 * time.Minute) {
		return false
	}
	e.runMagitrickleProbe(ctx)
	return true
}

// runMagitrickleProbe - тело пробы (старт + перепроверка по версии).
func (e *Engine) runMagitrickleProbe(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	if v, err := e.mt.GetVersion(pctx); err == nil && v != "" {
		e.lastProbeVersion = v
		// flavor каскаду: при бэкенде mt-c (версия -mawg-c) каскад не
		// пишет в чужие iptables-цепочки (107-док); смена версии MT
		// (обновили mtinstall'ом) перепроверяет гейт без рестарта панели
		e.casc.SetBackendFlavor(magitrickle.FlavorOf(v))
	}
	defer cancel()
	before, err := e.mt.GroupsWithRules(pctx)
	if err != nil || len(before) == 0 {
		return // честная пустая конфигурация - пробовать не на чем
	}
	// форс-PUT отремонтированного списка: changed=true - проверяем именно
	// массовую запись (путь всех наших правок), при этом repair из тени
	// живёт внутри и чинит потери, а не закрепляет их
	if err := e.mt.MutateGroups(pctx, func(groups []magitrickle.Group) bool { return true }); err != nil {
		e.store.LogEvent("magitrickle", "probe", "проба совместимости: первая запись не удалась ("+err.Error()+") - рестартую MT и повторяю")
		if e.healMagitrickle("probe", time.Minute) || e.waitMagitrickleAlive(2*time.Second) {
			if !e.waitMagitrickleAlive(90*time.Second) || e.mt.MutateGroups(pctx, func(groups []magitrickle.Group) bool { return true }) != nil {
				e.store.SetMTGlitchy(true)
				e.store.LogEvent("magitrickle", "probe", "проба совместимости: запись не удалась и после рестарта - похоже, глючная сборка MT")
				return
			}
		} else {
			e.store.SetMTGlitchy(true)
			e.store.LogEvent("magitrickle", "probe", "проба совместимости: MT не поднялся для повтора - похоже, глючная сборка MT")
			return
		}
	}
	after, err := e.mt.GroupsWithRules(pctx)
	if err != nil {
		return
	}
	// усечение: после записи групп меньше, чем было прочитано до ремонта
	if len(after) < len(before) {
		e.store.SetMTGlitchy(true)
		e.store.LogEvent("magitrickle", "probe", fmt.Sprintf("глючная сборка MT: no-op PUT усёк список групп (%d -> %d) - рестартую демона, ставьте форк mawgfix", len(before), len(after)))
		e.healMagitrickle("probe", time.Minute)
		return
	}
	e.store.SetMTGlitchy(false)
}

// RetryMTWrite - самолечение записей в MT: сбой -> рестарт демона -> одна
// повторная попытка. Общий путь движка и веб-ручек панели: на Кинетике
// bulk-PUT регулярно ловит 500 на грязных nf-объектах, и веб-запись без
// ретрая теряла группы (кейс владельца: правка telega из панели).
func (e *Engine) RetryMTWrite(key string, attempt func() error) error {
	err := attempt()
	if err == nil {
		return nil
	}
	e.store.LogEvent(key, "magitrickle", "groups save failed: "+err.Error())
	restarted := e.healMagitrickle(key, 10*time.Minute)
	if !restarted && !e.waitMagitrickleAlive(2*time.Second) {
		// рестарт не состоялся (кулдаун/кап), MT мёртв - повтор бесполезен
		return err
	}
	if !e.waitMagitrickleAlive(90 * time.Second) {
		e.store.LogEvent(key, "magitrickle", "MT не поднялся за 90с после рестарта, повторная запись отложена до следующего повода")
		return err
	}
	e.store.LogEvent(key, "magitrickle", "повторная попытка записи после рестарта MT")
	if err2 := attempt(); err2 != nil {
		e.store.LogEvent(key, "magitrickle", "повторная запись не удалась: "+err2.Error()+" - проверьте группу вручную или перезапустите MT")
		return err2
	}
	e.store.LogEvent(key, "magitrickle", "повторная запись удалась после рестарта MT")
	return nil
}

// magitrickle 0.8.2: single-group PUT не пересоздаёт iptables-цепочку, если
// рантайм группы уже выключен; пересборку даёт только массовый PUT списка.
func (e *Engine) applyGroupChanges(key string, mutate func(groups []magitrickle.Group) bool) error {
	return e.RetryMTWrite(key, func() error {
		return e.tryApplyGroups(key, mutate)
	})
}

// tryApplyGroups - одна попытка массовой записи с каскадными хуками.
func (e *Engine) tryApplyGroups(key string, mutate func(groups []magitrickle.Group) bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	err := e.mt.MutateGroups(ctx, mutate)
	if err == nil {
		e.mtEverAlive = true
	}
	return err
}

// waitMagitrickleAlive опрашивает API MT до успеха или дедлайна (демон
// после рестарта поднимается не мгновенно, на буте Кинетика иногда висит
// на IPv4-лупбеке - mawg корректно ждёт).
func (e *Engine) waitMagitrickleAlive(max time.Duration) bool {
	deadline := e.now().Add(max)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := e.mt.GroupsWithRules(ctx)
		cancel()
		if err == nil {
			return true
		}
		if !e.now().Before(deadline) {
			return false
		}
		time.Sleep(3 * time.Second)
	}
}

// сбой массового PUT оставляет рантайм magitrickle усечённым, но конфиг
// на диске цел: рестарт демона собирает группы обратно. мертвому демону
// рестартим часто, живому после сбоя записи - редко. возвращает true,
// если рестарт реально состоялся.
func (e *Engine) healMagitrickle(key string, cooldown time.Duration) bool {
	if e.now().Sub(e.mtHealAt) < cooldown {
		return false
	}
	if !e.mtRestartBudget() {
		e.store.LogEvent(key, "magitrickle", "кап автоперезапусков MT исчерпан (6/час) - не молочу по мёртвому, разберитесь с демоном")
		return false
	}
	e.mtHealAt = e.now()
	if err := e.backend.RestartMagitrickle(); err != nil {
		e.store.LogEvent(key, "magitrickle", "автоперезапуск не удался: "+err.Error())
		return false
	}
	e.store.LogEvent(key, "magitrickle", "автоперезапуск после сбоя сохранения")
	return true
}

// mtHealHourlyCap - предел автоперезапусков MT в час: битый демон или
// глючная сборка не должны превращаться в рестарт-лупп.
const mtHealHourlyCap = 6

func (e *Engine) mtRestartBudget() bool {
	now := e.now()
	var kept []time.Time
	for _, t := range e.mtRestarts {
		if now.Sub(t) < time.Hour {
			kept = append(kept, t)
		}
	}
	if len(kept) >= mtHealHourlyCap {
		e.mtRestarts = kept
		return false
	}
	e.mtRestarts = append(kept, now)
	return true
}
