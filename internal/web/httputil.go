package web

// Хендлеры/логика HTTP-хелперов и кэша ответов (из server.go).

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

// respCache - короткий кэш готовых JSON-ответов тяжёлых GET-эндпоинтов:
// вкладки панели опрашивают их по интервалу, строить ответ заново на
// каждый запрос на softfloat-MIPS дорого.
type respCache struct {
	mu   sync.Mutex
	at   time.Time
	body []byte
}

func (c *respCache) serve(w http.ResponseWriter, ttl time.Duration, build func() ([]byte, error)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.body != nil && time.Since(c.at) < ttl {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(c.body)
		return
	}
	body, err := build()
	if err != nil {
		writeErr(w, err)
		return
	}
	c.at, c.body = time.Now(), body
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(body)
}

// reset - выкинуть кэш (после установки компонента ответ должен стать свежим).
func (c *respCache) reset() {
	c.mu.Lock()
	c.body = nil
	c.mu.Unlock()
}
