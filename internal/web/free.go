package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"mawg/internal/premium"
)

type freePoolReq struct {
	Name            string `json:"name"`
	Fallback        string `json:"fallback"`
	ProbeHost       string `json:"probeHost"`
	KeeneticSlot    string `json:"keeneticSlot"`
	CaptchaID       string `json:"captchaId"`
	CaptchaSolution string `json:"captchaSolution"`
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
		writeErr(w, errors.New("expected a Free pool name, without credentials or region override"))
		return
	}
	if withAnswer && (req.CaptchaID == "" || req.CaptchaSolution == "") {
		writeErr(w, errors.New("нет решения капчи: введите цифры с картинки"))
		return
	}
	settings, err := s.poolSettingsFromReq(poolSourceReq{Name: req.Name, Fallback: req.Fallback, ProbeHost: req.ProbeHost, KeeneticSlot: req.KeeneticSlot, OpenwrtProto: "amneziawg"})
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.validFallback(req.Name, settings.Fallback); err != nil {
		writeErr(w, err)
		return
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
	plan.Warnings = []string{"Amnezia Free получен без аккаунта. Пул не активирован: используйте обычную кнопку активации после проверки настроек."}
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
