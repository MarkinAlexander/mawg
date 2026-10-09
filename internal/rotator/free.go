package rotator

import (
	"context"
	"errors"

	"mawg/internal/premium"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

func (e *Engine) CreateFreePool(ctx context.Context, name string, settings store.PoolSettings) (store.Pool, error) {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	clean, err := wgconf.SanitizePoolName(name)
	if err != nil {
		return store.Pool{}, errors.New("invalid Amnezia Free pool name")
	}
	if settings.EngineMode != "" || settings.Source != "" || !store.ValidProbeTarget(settings.WithDefaults().ProbeHost) {
		return store.Pool{}, errors.New("Amnezia Free requires a native AWG pool with a valid probe target")
	}
	if settings.Platform == store.PlatformKeenetic {
		return store.Pool{}, errors.New("accountless Amnezia Free AWG requires OpenWrt or Linux")
	}
	if settings.Platform == store.PlatformOpenwrt {
		settings.OpenwrtProto = "amneziawg"
	}
	for _, p := range e.store.Pools() {
		if p.Free && p.Name == clean && len(p.Configs) > 0 {
			return p, nil
		}
		if p.Free {
			return store.Pool{}, errors.New("Amnezia Free already belongs to another pool")
		}
		if p.Name == clean {
			return store.Pool{}, errors.New("pool already exists; existing configuration retained")
		}
	}
	v := e.store.Free()
	if v.UUID == "" {
		v.UUID, err = premium.UUID()
		if err != nil {
			return store.Pool{}, errors.New("could not create Amnezia Free identity")
		}
	}
	if v.Private == "" {
		v.Private, _, err = premium.WireGuardKeys()
		if err != nil {
			return store.Pool{}, errors.New("could not create Amnezia Free key")
		}
	}
	if err := e.store.SaveFree(v); err != nil {
		return store.Pool{}, err
	}
	if len(v.Config) == 0 {
		country, protocol, err := e.PremiumClient.FreeService(ctx, v.UUID)
		if err != nil {
			return store.Pool{}, err
		}
		if protocol != "awg" {
			return store.Pool{}, errors.New("catalog offers an unsupported Amnezia Free protocol; no configuration requested")
		}
		v.UserCountry = country
		if err := e.store.SaveFree(v); err != nil {
			return store.Pool{}, err
		}
		v.Config, v.Auth, err = e.PremiumClient.FreeConfig(ctx, v.UUID, country, v.Private)
		if err != nil {
			return store.Pool{}, err
		}
		if err := e.store.SaveFree(v); err != nil {
			return store.Pool{}, err
		}
	}
	cfg, err := wgconf.Parse(v.Config)
	if err != nil || cfg.PrivateKey != v.Private || !cfg.AWG.Present() {
		return store.Pool{}, errors.New("invalid saved Amnezia Free configuration")
	}
	if e.addressTaken(clean, "", store.ManagedConfig{Addresses: cfg.Addresses}) {
		return store.Pool{}, errors.New("Amnezia Free tunnel address is occupied; previous configuration retained")
	}
	return e.store.CreateFreePool(clean, settings, wgconf.NamedConfig{Raw: v.Config, Config: cfg, OriginalName: "Amnezia Free"})
}
