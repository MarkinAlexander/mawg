package singbox

// Клиент clash_api движка (60-док): узлы движковых пулов, их задержки
// (история urltest), ручной выбор в selector и перемер группы. Функции
// платформо-нейтральны (порт параметром - тестируются на любом хосте);
// методы Manager подставляют боевой порт с учётом shared-режима.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ClashProxy - элемент ответа clash_api /proxies: группа (all/now) или
// outbound (history - замеры urltest; delay 0 = не измерен/не отвечал).
type ClashProxy struct {
	Type    string      `json:"type"`
	Now     string      `json:"now"`
	All     []string    `json:"all,omitempty"`
	History []ClashHist `json:"history,omitempty"`
}

// ClashHist - точка истории задержек (мс).
type ClashHist struct {
	Time  time.Time `json:"time"`
	Delay int       `json:"delay"`
}

// LastDelay - последний успешный замер узла (0 = не измерен/не отвечал).
func LastDelay(p ClashProxy) int {
	for i := len(p.History) - 1; i >= 0; i-- {
		if p.History[i].Delay > 0 {
			return p.History[i].Delay
		}
	}
	return 0
}

func clashCall(ctx context.Context, base int, method, path string, body []byte, out any) error {
	if base <= 0 {
		return fmt.Errorf("clash_api движка недоступна")
	}
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s", base, path), rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("движок не отвечает: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("clash_api %s %s: статус %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// ClashProxies - все прокси и группы движка (история задержек включена).
func ClashProxies(ctx context.Context, base int) (map[string]ClashProxy, error) {
	var out struct {
		Proxies map[string]ClashProxy `json:"proxies"`
	}
	if err := clashCall(ctx, base, http.MethodGet, "/proxies", nil, &out); err != nil {
		return nil, err
	}
	return out.Proxies, nil
}

// ClashSelect - ручной выбор узла в selector-группе
// (PUT /proxies/{group} {"name": ...}).
func ClashSelect(ctx context.Context, base int, group, name string) error {
	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return err
	}
	return clashCall(ctx, base, http.MethodPut, "/proxies/"+url.PathEscape(group), body, nil)
}

// ClashGroupDelay - перемерить urltest-группу сейчас
// (GET /group/{name}/delay?url=&timeout=); возвращает задержки по узлам.
func ClashGroupDelay(ctx context.Context, base int, group, testURL string, timeout time.Duration) (map[string]int, error) {
	q := url.Values{}
	q.Set("url", testURL)
	q.Set("timeout", strconv.Itoa(int(timeout.Milliseconds())))
	var out map[string]int
	if err := clashCall(ctx, base, http.MethodGet, "/group/"+url.PathEscape(group)+"?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}
