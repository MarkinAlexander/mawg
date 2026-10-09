package rotator

import (
	"context"
	"errors"
	"fmt"

	"mawg/internal/premium"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

func (e *Engine) ImportPremium(ctx context.Context, pool, link string) error {
	key, country, err := premium.ImportKey(link)
	if err != nil {
		return err
	}
	e.actMu.Lock()
	defer e.actMu.Unlock()
	v, err := e.store.ImportPremium(pool, key, country)
	if err != nil {
		return err
	}
	v.Countries, err = e.PremiumClient.Countries(ctx, v.Key, v.UUID, v.UserCountry)
	if err != nil {
		return err
	}
	return e.store.SavePremium(v)
}

func (e *Engine) SwitchPremium(ctx context.Context, pool, country string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(pool)
	if !ok || !p.Premium {
		return errors.New("сначала импортируйте Premium в этот пул")
	}
	if p.Disabled {
		return errors.New("включите пул Premium перед сменой страны")
	}
	v := e.store.Premium()
	countries, err := e.PremiumClient.Countries(ctx, v.Key, v.UUID, v.UserCountry)
	if err != nil {
		return err
	}
	supported := false
	for _, c := range countries {
		if c.Code == country {
			supported = true
		}
	}
	if !supported {
		return errors.New("эта страна недоступна для Premium AWG")
	}
	v.Countries = countries
	if v.PendingPrivate == "" || v.PendingCountry != country {
		v.PendingPrivate, _, err = premium.WireGuardKeys()
		if err != nil {
			return err
		}
	}
	v.PendingCountry = country
	if err := e.store.SavePremium(v); err != nil {
		return err
	}
	raw, err := e.PremiumClient.Config(ctx, v.Key, v.UUID, v.UserCountry, country, v.PendingPrivate)
	if err != nil {
		return fmt.Errorf("%w; старый конфиг оставлен, регистрация у gateway могла измениться - повторите смену страны", err)
	}
	if err := e.PremiumClient.VerifyDevice(ctx, v.Key, v.UUID, v.UserCountry, country); err != nil {
		return fmt.Errorf("%w; старый конфиг оставлен, регистрация у gateway могла измениться - повторите смену страны", err)
	}
	cfg, err := wgconf.Parse(raw)
	if err != nil {
		return errors.New("gateway вернул неподдерживаемый конфиг; регистрация могла измениться - повторите смену страны")
	}
	if e.addressTaken(p.Name, p.DeviceName(), store.ManagedConfig{Addresses: cfg.Addresses}) {
		return errors.New("адрес Premium-туннеля уже занят; старый конфиг оставлен, регистрация изменилась - повторите смену страны")
	}
	// движковый пул (Keenetic: конфиг gateway - AWG 3.x, слот его не
	// поднимает): конфиг сохраняется в пул, applyEngine его подберёт;
	// нативный путь (OpenWrt с kmod AWG) - как у автора
	if p.Settings.EngineMode == "singbox" {
		if e.addressTaken(p.Name, p.DeviceName(), store.ManagedConfig{Addresses: cfg.Addresses}) {
			return errors.New("адрес Premium-туннеля уже занят; старый конфиг оставлен - повторите смену страны")
		}
		file, err := e.store.ReplacePremiumConfig(pool, raw)
		if err != nil {
			return errors.New("не удалось сохранить конфиг Premium - повторите смену страны")
		}
		if err := e.recordApplied(p, file, cfg.Endpoint()); err != nil {
			return errors.New("страна применена, но состояние пула не сохранилось - повторите смену страны")
		}
		v.Country = country
		v.PendingPrivate, v.PendingCountry = "", ""
		if err := e.store.SavePremium(v); err != nil {
			return errors.New("страна применена, но состояние подписки не сохранилось - повторите смену страны")
		}
		return nil
	}
	e.ifaceTouched()
	if err := e.backend.Apply(p, cfg); err != nil {
		return errors.New("не удалось поднять интерфейс Premium; старый конфиг оставлен - повторите смену страны")
	}
	file, err := e.store.ReplacePremiumConfig(pool, raw)
	if err != nil {
		return errors.New("интерфейс Premium сменился, но конфиг не сохранился - повторите смену страны")
	}
	if err := e.recordApplied(p, file, cfg.Endpoint()); err != nil {
		return errors.New("страна применена, но состояние пула не сохранилось - повторите смену страны")
	}
	e.saveRouterConfig(pool)
	v.Country = country
	v.PendingPrivate, v.PendingCountry = "", ""
	if err := e.store.SavePremium(v); err != nil {
		return errors.New("страна применена, но состояние подписки не сохранилось - повторите смену страны")
	}
	return nil
}

func (e *Engine) RenamePool(oldName, newName string) (store.Pool, error) {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	return e.store.RenamePool(oldName, newName)
}

func (e *Engine) DeletePool(name string) error {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	p, ok := e.store.Pool(name)
	if !ok {
		return errors.New("пул не найден")
	}
	// движковый пул: tun гасится пересборкой конфига движка на web-слое
	if !p.Disabled && p.Settings.EngineMode == "" {
		e.ifaceTouched()
		if err := e.backend.Down(p); err != nil && p.Premium {
			return errors.New("не удалось выключить интерфейс Premium; пул оставлен как есть")
		}
	}
	return e.store.DeletePool(name)
}
