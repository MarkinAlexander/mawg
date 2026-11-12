package magitrickle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Уровень лога форка (rev22): кноб «Уровень лога MT» в панели. Форк
// применяет глобальный уровень zerolog сразу и пишет app.logLevel в
// config.yaml (переживает рестарт демона).

// MtLogLevel - текущий уровень лога демона (debug/info/warn/...).
func (c *Client) MtLogLevel(ctx context.Context) (string, error) {
	var out struct {
		Level string `json:"level"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/v1/app/loglevel", nil, &out); err != nil {
		return "", err
	}
	return out.Level, nil
}

// SetMtLogLevel - смена уровня на лету; кривое имя форк отклоняет 400 -
// ошибка отдаётся с её текстом (панель показывает, что не применилось).
func (c *Client) SetMtLogLevel(ctx context.Context, level string) error {
	data, err := json.Marshal(map[string]string{"level": level})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.BaseURL+"/api/v1/app/loglevel", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 {
		return nil
	}
	errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var ferr struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(errBody, &ferr) == nil && ferr.Error != "" {
		return fmt.Errorf("MT: %s", ferr.Error)
	}
	return &statusError{method: http.MethodPut, path: "/api/v1/app/loglevel", code: resp.StatusCode}
}
