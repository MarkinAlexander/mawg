package rotator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"mawg/internal/premium"
	"mawg/internal/store"
	"mawg/internal/wgconf"
)

// saveRejectedFree - конфиг, который mawg не принял, сохраняется рядом с
// данными: годен для импорта в официальный клиент (настоящий ключ), плюс
// событие в журнал панели с точной причиной отказа.
func (e *Engine) saveRejectedFree(err error) {
	var bad *premium.BadConfigError
	if !errors.As(err, &bad) || (bad.Raw == "" && bad.Config == "") {
		return
	}
	path := filepath.Join(e.store.Base(), "free-rejected.txt")
	content := "# mawg не смог применить конфиг Amnezia Free: " + bad.Reason + "\n" +
		"# время: " + time.Now().Format(time.RFC3339) + "\n" +
		"# ниже - выданный конфиг целиком (можно импортировать в официальный клиент Amnezia)\n"
	if bad.Config != "" {
		content += bad.Config + "\n"
	} else {
		content += bad.Raw + "\n"
	}
	if os.WriteFile(path, []byte(content), 0o600) == nil {
		e.store.LogEvent("free", "config", "выданный конфиг отклонён ("+bad.Reason+"); конфиг сохранён: "+path)
	}
}

func (e *Engine) CreateFreePool(ctx context.Context, name string, settings store.PoolSettings) (store.Pool, error) {
	return e.createFreePool(ctx, name, settings, nil)
}

// AnswerFreeCaptcha - повтор создания Free-пула с решением капчи от
// человека; идентичность (UUID/ключ) та же, сохранена с первой попытки.
func (e *Engine) AnswerFreeCaptcha(ctx context.Context, name string, settings store.PoolSettings, answer premium.CaptchaAnswer) (store.Pool, error) {
	return e.createFreePool(ctx, name, settings, &answer)
}

func (e *Engine) createFreePool(ctx context.Context, name string, settings store.PoolSettings, answer *premium.CaptchaAnswer) (store.Pool, error) {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	clean, err := wgconf.SanitizePoolName(name)
	if err != nil {
		return store.Pool{}, errors.New("invalid Amnezia Free pool name")
	}
	if settings.EngineMode != "" || settings.Source != "" || !store.ValidProbeTarget(settings.WithDefaults().ProbeHost) {
		return store.Pool{}, errors.New("Amnezia Free requires a native AWG pool with a valid probe target")
	}
	// Keenetic: конфиг Free - AWG 2.0 (I/J/H/S-параметры), прошивка 5.1+
	// умеет их нативно; применяется в слот Wireguard как любой AWG-конфиг.
	// На более старой прошивке пул поднимется, но хендшейка не будет -
	// «Система -> Зависимости» предупреждает о 5.1+ отдельно.
	if settings.Platform == store.PlatformOpenwrt {
		settings.OpenwrtProto = "amneziawg"
	}
	if settings.Platform == store.PlatformKeenetic && settings.KeeneticSlot == "" {
		return store.Pool{}, errors.New("для Amnezia Free нужен слот WireGuard - создайте его кнопкой «+ слот» в диалоге пула")
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
		v.Config, v.Auth, err = e.PremiumClient.FreeConfig(ctx, v.UUID, country, v.Private, answer)
		if err != nil {
			e.saveRejectedFree(err)
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
