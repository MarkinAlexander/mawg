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
		return errors.New("import Premium into this pool first")
	}
	if p.Disabled {
		return errors.New("enable the Premium pool before switching countries")
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
		return errors.New("country is unavailable for Premium AWG")
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
		return fmt.Errorf("%w; previous local config retained, remote registration may have changed; retry the country", err)
	}
	if err := e.PremiumClient.VerifyDevice(ctx, v.Key, v.UUID, v.UserCountry, country); err != nil {
		return fmt.Errorf("%w; previous local config retained, remote registration may have changed; retry the country", err)
	}
	cfg, err := wgconf.Parse(raw)
	if err != nil {
		return errors.New("unsupported Premium configuration; remote registration may have changed")
	}
	if e.addressTaken(p.Name, p.DeviceName(), store.ManagedConfig{Addresses: cfg.Addresses}) {
		return errors.New("Premium tunnel address is occupied; previous local config retained, remote registration changed; retry the country")
	}
	e.ifaceTouched()
	if err := e.backend.Apply(p, cfg); err != nil {
		return errors.New("Premium interface apply failed; previous local config retained, remote registration changed; retry the country")
	}
	file, err := e.store.ReplacePremiumConfig(pool, raw)
	if err != nil {
		return errors.New("Premium interface changed but local config could not be saved; retry the country")
	}
	if err := e.recordApplied(p, file, cfg.Endpoint()); err != nil {
		return errors.New("Premium country applied but active state could not be saved; retry the country")
	}
	e.saveRouterConfig(pool)
	v.Country = country
	v.PendingPrivate, v.PendingCountry = "", ""
	if err := e.store.SavePremium(v); err != nil {
		return errors.New("Premium country applied but subscription state could not be saved; retry the country")
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
		return errors.New("pool not found")
	}
	if !p.Disabled {
		e.ifaceTouched()
		if err := e.backend.Down(p); err != nil && p.Premium {
			return errors.New("Premium interface could not be brought down; pool retained")
		}
	}
	return e.store.DeletePool(name)
}
