package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"mawg/internal/platform/keenetic"
	"mawg/internal/platform/openwrt"
	"mawg/internal/premium"
	"mawg/internal/store"
)

type freePoolReq struct {
	Name            string `json:"name"`
	Fallback        string `json:"fallback"`
	ProbeHost       string `json:"probeHost"`
	KeeneticSlot    string `json:"keeneticSlot"`
	CaptchaID       string `json:"captchaId"`
	CaptchaSolution string `json:"captchaSolution"`
}

// nativeAWG3 - роутер поднимает AWG 3.x нативно (OpenWrt с kmod
// amneziawg 3.x, Keenetic 5.2+): /status отдаёт флаг, панели по нему
// прячут блок нативного импорта Premium в карточке пула.
func (s *Server) nativeAWG3() bool {
	if ob, ok := s.backend.(*openwrt.Backend); ok {
		return ob.SupportsNativeAWG3()
	}
	if kb, ok := s.backend.(*keenetic.Backend); ok {
		return kb.SupportsNativeAWG3()
	}
	return false
}

func (s *Server) createFreePool(w http.ResponseWriter, r *http.Request) {
	s.serveFreePool(w, r, false)
}

// answerFreeCaptcha - повтор создания Free-пула с решением капчи от человека.
func (s *Server) answerFreeCaptcha(w http.ResponseWriter, r *http.Request) {
	s.serveFreePool(w, r, true)
}

func (s *Server) serveFreePool(w http.ResponseWriter, r *http.Request, withAnswer bool) {
	var req freePoolReq
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || req.Name == "" || d.Decode(new(any)) != io.EOF {
		writeErr(w, errors.New("нужно имя пула для Amnezia Free - без ключей, аккаунтов и выбора региона"))
		return
	}
	if withAnswer && (req.CaptchaID == "" || req.CaptchaSolution == "") {
		writeErr(w, errors.New("нет решения капчи: введите цифры с картинки"))
		return
	}
	// Путь Free зависит от платформы:
	//  - OpenWrt с kmod amneziawg 3.x - НАТИВНО (без движка sing-box:
	//    юзеру без ядра или места под него Free всё равно доступен);
	//  - иначе (Keenetic; OpenWrt со старым kmod) - движок sing-box-lx
	//    (tun-интерфейс). Ядро может быть не установлено на момент
	//    создания - пул создаётся выключенным, предупредим в плане.
	native := false
	if ob, ok := s.backend.(*openwrt.Backend); ok && ob.SupportsNativeAWG3() {
		native = true
	}
	engineMissing := false
	if !native {
		if _, err := s.sb(); err != nil {
			engineMissing = true
		}
	}
	// Проба Free ФИКСИРОВАНА: 1.1.1.1 - адрес из allowlist-а сервиса.
	// Free - сплит-туннель: сервер пускает только к адресам из выданного
	// списка (у конфига ~472 префикса, IPv6 нет). gstatic резолвится то в
	// AAAA, то во вне-списочные IPv4 - произвольная цель пробы мигает;
	// 1.1.1.1/32 в списке всегда, IP без DNS - детерминированно. Юзеру цель
	// для Free менять нельзя - иначе «работает/падает» без причины.
	settings := store.PoolSettings{
		Platform: s.backend.Name(), Fallback: req.Fallback, ProbeHost: FreeProbeURL,
	}
	if native {
		settings.NativeAWG3 = true
		settings.OpenwrtProto = "amneziawg"
	} else {
		settings.EngineMode = engineMode
	}
	if err := s.validFallback(req.Name, settings.Fallback); err != nil {
		writeErr(w, err)
		return
	}
	if !native {
		s.tunMu.Lock()
		settings.TunName = s.allocTun()
		s.tunMu.Unlock()
	}
	var plan sourcePlan
	if withAnswer {
		pool, err := s.engine.AnswerFreeCaptcha(r.Context(), req.Name, settings, premium.CaptchaAnswer{ID: req.CaptchaID, Solution: req.CaptchaSolution})
		if err != nil {
			s.writeFreeErr(w, err)
			return
		}
		plan = sourcePlan{Pool: pool.Name, Added: len(pool.Configs)}
	} else {
		pool, err := s.engine.CreateFreePool(r.Context(), req.Name, settings)
		if err != nil {
			s.writeFreeErr(w, err)
			return
		}
		plan = sourcePlan{Pool: pool.Name, Added: len(pool.Configs)}
	}
	via := "через ядро sing-box, " + settings.TunName
	if native {
		via = "нативно (kmod amneziawg 3.x), движок sing-box не требуется"
	}
	plan.Warnings = []string{"Amnezia Free получен без аккаунта (конфиг AmneziaWG 3.x идёт " + via + "). Пул не активирован: используйте обычную кнопку активации после проверки настроек."}
	if engineMissing {
		plan.Warnings = append(plan.Warnings, "Ядро sing-box-lx не установлено - пул заработает после установки в «Система -> Зависимости».")
	}
	writeJSON(w, http.StatusOK, plan)
}

// writeFreeErr - капча уходит в панель как задание для человека, прочие
// ошибки - как обычный отказ.
func (s *Server) writeFreeErr(w http.ResponseWriter, err error) {
	var cap *premium.CaptchaError
	if errors.As(err, &cap) && cap.Captcha.Image != "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"captchaRequired": true,
			"captcha":         cap.Captcha,
		})
		return
	}
	writeErr(w, err)
}
