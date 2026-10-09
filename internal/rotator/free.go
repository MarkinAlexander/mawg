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
		return store.Pool{}, errors.New("имя пула не подходит: строчная латиница, цифры, дефис")
	}
	// Конфиг Free у gateway - формат AWG 3.x (защита заголовка, диапазонные
	// таймеры): нативные интерфейсы его не поднимают, пул создаётся в
	// режиме движка (sing-box-lx) - web-слой задаёт EngineMode и tun.
	if settings.Source != "" || !store.ValidProbeTarget(settings.WithDefaults().ProbeHost) {
		return store.Pool{}, errors.New("для Amnezia Free нужна корректная цель пробы")
	}
	if settings.EngineMode == "" {
		return store.Pool{}, errors.New("Amnezia Free идёт через движок sing-box-lx - установите ядро в «Система -> Зависимости»")
	}
	if settings.TunName == "" {
		return store.Pool{}, errors.New("для Amnezia Free не выделен tun-интерфейс")
	}
	for _, p := range e.store.Pools() {
		if p.Free && p.Name == clean && len(p.Configs) > 0 {
			return p, nil
		}
		if p.Free {
			return store.Pool{}, errors.New("Amnezia Free уже привязан к другому пулу")
		}
		if p.Name == clean {
			return store.Pool{}, errors.New("пул с таким именем уже существует, его конфигурация сохранена")
		}
	}
	v := e.store.Free()
	if v.UUID == "" {
		v.UUID, err = premium.UUID()
		if err != nil {
			return store.Pool{}, errors.New("не удалось создать идентичность Amnezia Free")
		}
	}
	if v.Private == "" {
		v.Private, _, err = premium.WireGuardKeys()
		if err != nil {
			return store.Pool{}, errors.New("не удалось создать ключ Amnezia Free")
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
			return store.Pool{}, errors.New("gateway предлагает неподдерживаемый протокол Amnezia Free - конфиг не запрашивался")
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
		return store.Pool{}, errors.New("сохранённый конфиг Amnezia Free повреждён")
	}
	if e.addressTaken(clean, "", store.ManagedConfig{Addresses: cfg.Addresses}) {
		return store.Pool{}, errors.New("адрес туннеля Amnezia Free уже занят; конфигурация сохранена")
	}
	return e.store.CreateFreePool(clean, settings, wgconf.NamedConfig{Raw: v.Config, Config: cfg, OriginalName: "Amnezia Free"})
}
