package rotator

import (
	"context"
	"errors"
	"fmt"
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

// ensureFreeConfig гарантирует валидную идентичность и кэш конфига Free.
// Идентичность (UUID + ключ) создаётся один раз и живёт в free.json:
// gateway привязывает выдачу к устройству и даёт один конфиг; при пустом
// кэше конфига идём в gateway (капча оттуда уходит вызывающему как есть).
func (e *Engine) ensureFreeConfig(ctx context.Context, answer *premium.CaptchaAnswer) error {
	v := e.store.Free()
	if v.UUID == "" {
		u, err := premium.UUID()
		if err != nil {
			return errors.New("не удалось создать идентичность Amnezia Free")
		}
		v.UUID = u
	}
	if v.Private == "" {
		k, _, err := premium.WireGuardKeys()
		if err != nil {
			return errors.New("не удалось создать ключ Amnezia Free")
		}
		v.Private = k
	}
	if err := e.store.SaveFree(v); err != nil {
		return err
	}
	if len(v.Config) == 0 {
		country, protocol, err := e.PremiumClient.FreeService(ctx, v.UUID)
		if err != nil {
			return err
		}
		if protocol != "awg" {
			return errors.New("gateway предлагает неподдерживаемый протокол Amnezia Free - конфиг не запрашивался")
		}
		v.UserCountry = country
		if err := e.store.SaveFree(v); err != nil {
			return err
		}
		v.Config, v.Auth, err = e.PremiumClient.FreeConfig(ctx, v.UUID, country, v.Private, answer)
		if err != nil {
			e.saveRejectedFree(err)
			return err
		}
		if err := e.store.SaveFree(v); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) createFreePool(ctx context.Context, name string, settings store.PoolSettings, answer *premium.CaptchaAnswer) (store.Pool, error) {
	e.actMu.Lock()
	defer e.actMu.Unlock()
	clean, err := wgconf.SanitizePoolName(name)
	if err != nil {
		return store.Pool{}, errors.New("имя пула не подходит: строчная латиница, цифры, дефис")
	}
	// Конфиг Free у gateway - формат AWG 3.x. Поднимают его:
	//  - kmod amneziawg 3.x (OpenWrt) - нативно, без движка sing-box
	//    (полезно, когда ядра нет или под него нет места);
	//  - движок sing-box-lx (Keenetic: слот AWG 2.0 HP-ключ не применяет).
	// Путь выбирает web-слой: nativeAWG3=true -> обычный пул (EngineMode
	// пуст), иначе EngineMode + tun.
	if settings.Source != "" || !store.ValidProbeTarget(settings.WithDefaults().ProbeHost) {
		return store.Pool{}, errors.New("для Amnezia Free нужна корректная цель пробы")
	}
	if settings.Platform == store.PlatformOpenwrt {
		settings.OpenwrtProto = "amneziawg"
	}
	if settings.EngineMode == "" && !settings.NativeAWG3 {
		return store.Pool{}, errors.New("Amnezia Free идёт через движок sing-box-lx - установите ядро в «Система -> Зависимости»")
	}
	if settings.EngineMode != "" && settings.TunName == "" {
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
	if err := e.ensureFreeConfig(ctx, answer); err != nil {
		return store.Pool{}, err
	}
	v := e.store.Free()
	cfg, err := wgconf.Parse(v.Config)
	if err != nil || cfg.PrivateKey != v.Private || !cfg.AWG.Present() {
		return store.Pool{}, errors.New("сохранённый конфиг Amnezia Free повреждён")
	}
	if e.addressTaken(clean, "", store.ManagedConfig{Addresses: cfg.Addresses}) {
		return store.Pool{}, errors.New("адрес туннеля Amnezia Free уже занят; конфигурация сохранена")
	}
	return e.store.CreateFreePool(clean, settings, wgconf.NamedConfig{Raw: v.Config, Config: cfg, OriginalName: "Amnezia Free"})
}

// ReissueFreePool - перевыпуск Amnezia Free. Идентичность привязана к
// устройству и даёт ОДИН конфиг, так что новый появляется только с чистой
// идентичностью - сервер у свежевыданного конфига может быть другим.
// Свежий конфиг запрашивается ДО удаления старого пула: капча или сбой
// gateway оставляют пул как был; после успешной выдачи пул пересоздаётся
// с теми же настройками и остаётся выключенным до ручной активации.
func (e *Engine) ReissueFreePool(ctx context.Context, name string, answer *premium.CaptchaAnswer) (store.Pool, error) {
	p, ok := e.store.Pool(name)
	if !ok {
		return store.Pool{}, fmt.Errorf("пул %s не найден", name)
	}
	if !p.Free {
		return store.Pool{}, errors.New("перевыпуск - только для пулов Amnezia Free")
	}
	settings := p.Settings
	if err := e.store.ClearFree(); err != nil {
		return store.Pool{}, err
	}
	if err := e.ensureFreeConfig(ctx, answer); err != nil {
		return store.Pool{}, err
	}
	if err := e.DeletePool(name); err != nil {
		return store.Pool{}, fmt.Errorf("не удалил старый пул: %w", err)
	}
	// кэш уже со свежим конфигом - CreateFreePool в gateway не пойдёт
	created, err := e.CreateFreePool(ctx, name, settings)
	if err == nil && len(created.Configs) > 0 {
		e.store.LogEvent("free", "config", "Amnezia Free перевыпущен: новая идентичность, endpoint "+created.Configs[0].Endpoint+" (сравните со старым - если у сервиса сервер один, он не сменится)")
	}
	return created, err
}
