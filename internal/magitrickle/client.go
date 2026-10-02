package magitrickle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type Rule struct {
	ID     string `json:"id,omitempty"`
	Name   string `json:"name,omitempty"`
	Type   string `json:"type"`
	Rule   string `json:"rule"`
	Enable bool   `json:"enable"`
}

type Group struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Color     string `json:"color,omitempty"`
	Interface string `json:"interface"`
	Enable    bool   `json:"enable"`
	Rules     []Rule `json:"rules,omitempty"`
}

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New(baseURL string) *Client {
	return &Client{
		BaseURL: baseURL,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

type statusError struct {
	method string
	path   string
	code   int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("%s %s: status %d", e.method, e.path, e.code)
}

// ретраить можно только ошибки до доставки запроса: ответ с ошибкой или
// таймаут означают, что magitrickle уже начал обрабатывать PUT, и повтор
// параллелится с ним (для массового PUT это добивает список групп).
func retryable(err error) bool {
	var se *statusError
	if errors.As(err, &se) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return false
	}
	return true
}

func (c *Client) call(ctx context.Context, method, path string, body any, out any) error {
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return &statusError{method: method, path: path, code: resp.StatusCode}
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) Groups(ctx context.Context) ([]Group, error) {
	var out struct {
		Groups []Group `json:"groups"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/v1/groups", nil, &out); err != nil {
		return nil, err
	}
	return out.Groups, nil
}

func (c *Client) GroupsWithRules(ctx context.Context) ([]Group, error) {
	var out struct {
		Groups []Group `json:"groups"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/v1/groups?with_rules=true", nil, &out); err != nil {
		return nil, err
	}
	return out.Groups, nil
}

func (c *Client) CreateGroup(ctx context.Context, g Group) (Group, error) {
	var out Group
	if err := c.call(ctx, http.MethodPost, "/api/v1/groups?save=true", g, &out); err != nil {
		return Group{}, err
	}
	return out, nil
}

func (c *Client) DeleteGroup(ctx context.Context, id string) error {
	return c.call(ctx, http.MethodDelete, "/api/v1/groups/"+id+"?save=true", nil, nil)
}

func (c *Client) CreateRule(ctx context.Context, groupID string, r Rule) (Rule, error) {
	var out Rule
	if err := c.call(ctx, http.MethodPost, "/api/v1/groups/"+groupID+"/rules?save=true", r, &out); err != nil {
		return Rule{}, err
	}
	return out, nil
}

func (c *Client) UpdateRule(ctx context.Context, groupID string, r Rule) error {
	path := "/api/v1/groups/" + groupID + "/rules/" + r.ID + "?save=true"
	return c.call(ctx, http.MethodPut, path, r, nil)
}

func (c *Client) DeleteRule(ctx context.Context, groupID, ruleID string) error {
	return c.call(ctx, http.MethodDelete, "/api/v1/groups/"+groupID+"/rules/"+ruleID+"?save=true", nil, nil)
}

func (c *Client) UpdateGroup(ctx context.Context, g Group, save bool) error {
	path := "/api/v1/groups/" + g.ID + "?save=" + strconv.FormatBool(save)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return lastErr
			}
		}
		lastErr = c.call(ctx, http.MethodPut, path, g, nil)
		if lastErr == nil {
			return nil
		}
	}
	return lastErr
}

func (c *Client) UpdateGroups(ctx context.Context, groups []Group, save bool) error {
	body := struct {
		Groups []Group `json:"groups"`
	}{Groups: groups}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return lastErr
			}
		}
		lastErr = c.call(ctx, http.MethodPut, "/api/v1/groups?save="+strconv.FormatBool(save), body, nil)
		if lastErr == nil {
			return nil
		}
		if !retryable(lastErr) {
			return lastErr
		}
	}
	return lastErr
}

func (c *Client) GroupByID(ctx context.Context, id string, withRules bool) (Group, error) {
	var g Group
	path := "/api/v1/groups/" + id
	if withRules {
		path += "?with_rules=true"
	}
	if err := c.call(ctx, http.MethodGet, path, nil, &g); err != nil {
		return Group{}, err
	}
	return g, nil
}

// массовый PUT в magitrickle не атомарен: конкурентные вызовы или сбой
// iptables-restore посреди обработки усекают список групп в его рантайме.
// mawg держит теневую копию полного списка и при обнаружении утери
// возвращает пропавшие группы в очередном сохранении. все мутации идут
// через MutateGroups: лок на весь цикл, правила в теле и контроль
// усечения после записи. одиночные удаления (пользователь в UI
// magitrickle) уважаются и переносятся в тень.
var (
	groupsMu     sync.Mutex
	mtShadow     []Group
	LoadShadow   func() []Group
	SaveShadow   func(groups []Group)
	OnRepair     func(restoredIDs []string)
	restoredOnce map[string]int
)

func SetShadowStorage(load func() []Group, save func([]Group)) {
	groupsMu.Lock()
	defer groupsMu.Unlock()
	LoadShadow = load
	SaveShadow = save
	restoredOnce = map[string]int{}
	if mtShadow == nil && load != nil {
		mtShadow = load()
	}
}

func updateShadowLocked(groups []Group) {
	mtShadow = append([]Group(nil), groups...)
	if SaveShadow != nil {
		SaveShadow(mtShadow)
	}
}

// repairFromShadow дополняет усечённый список группами из тени, если
// пропало сразу много (транкация). порядок берётся из тени, новые группы
// дописываются в конец. группа, уже дважды спасённая и снова пропавшая,
// считается удалённой пользователем и больше не возвращается.
func repairFromShadow(groups []Group) ([]Group, bool) {
	if len(mtShadow) == 0 {
		return groups, false
	}
	have := make(map[string]bool, len(groups))
	for _, g := range groups {
		have[g.ID] = true
	}
	var lost []Group
	fought := 0
	for _, g := range mtShadow {
		if !have[g.ID] {
			if restoredOnce[g.ID] >= 2 {
				fought++
				continue
			}
			lost = append(lost, g)
		}
	}
	if len(lost) == 0 {
		if fought > 0 {
			dropFought(have)
		}
		return groups, false
	}
	if len(lost) < 3 && len(lost)*2 < len(mtShadow) {
		return groups, false
	}
	for _, g := range lost {
		restoredOnce[g.ID]++
	}
	if OnRepair != nil {
		ids := make([]string, 0, len(lost))
		for _, g := range lost {
			ids = append(ids, g.Name)
		}
		OnRepair(ids)
	}
	out := make([]Group, 0, len(groups)+len(lost))
	for _, sg := range mtShadow {
		for _, g := range groups {
			if g.ID == sg.ID {
				out = append(out, g)
				break
			}
		}
	}
	for _, g := range lost {
		out = append(out, g)
	}
	for _, g := range groups {
		known := false
		for _, sg := range mtShadow {
			if sg.ID == g.ID {
				known = true
				break
			}
		}
		if !known {
			out = append(out, g)
		}
	}
	return out, true
}

func dropFought(have map[string]bool) {
	kept := mtShadow[:0]
	for _, g := range mtShadow {
		if !have[g.ID] && restoredOnce[g.ID] >= 2 {
			continue
		}
		kept = append(kept, g)
	}
	mtShadow = kept
	if SaveShadow != nil {
		SaveShadow(mtShadow)
	}
}

func (c *Client) MutateGroups(ctx context.Context, mutate func(groups []Group) bool) error {
	return c.transformGroups(ctx, func(groups []Group) ([]Group, bool) {
		return groups, mutate(groups)
	})
}

// AddGroup добавляет группу с правилами одним bulk PUT: атомарно, одна
// запись рантайма (mutate-путь append не поддерживает - срез в замыкании
// уходит по значению).
func (c *Client) AddGroup(ctx context.Context, g Group) error {
	return c.transformGroups(ctx, func(groups []Group) ([]Group, bool) {
		return append(groups, g), true
	})
}

func (c *Client) transformGroups(ctx context.Context, transform func([]Group) ([]Group, bool)) error {
	groupsMu.Lock()
	defer groupsMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	groups, err := c.GroupsWithRules(ctx)
	if err != nil {
		return err
	}
	groups, repaired := repairFromShadow(groups)
	groups, changed := transform(groups)
	if !changed && !repaired {
		updateShadowLocked(groups)
		return nil
	}
	if err := c.UpdateGroups(ctx, groups, true); err != nil {
		return err
	}
	check, err := c.GroupsWithRules(ctx)
	if err != nil {
		updateShadowLocked(groups)
		return nil
	}
	rules, checkRules := 0, 0
	for _, g := range groups {
		rules += len(g.Rules)
	}
	for _, g := range check {
		checkRules += len(g.Rules)
	}
	if len(check) != len(groups) || checkRules != rules {
		return fmt.Errorf("magitrickle усёк список: %d групп и %d правил вместо %d и %d, нужен рестарт magitrickle", len(check), checkRules, len(groups), rules)
	}
	updateShadowLocked(groups)
	return nil
}

// EnsureHealthy чинит усечённый рантайм magitrickle до любой другой
// нашей записи в него: одиночные PUT (создание группы, правило) тоже
// сохраняют весь рантайм на диск, и усечённое состояние стало бы
// постоянным.
func (c *Client) EnsureHealthy(ctx context.Context) error {
	return c.MutateGroups(ctx, func(groups []Group) bool { return false })
}

// RefreshShadow перечитывает список групп в тень после одиночных
// операций (создание, удаление) и из периодического монитора. усечённый
// рантайм тень не затирает: утерянным группам занимается ремонт.
func (c *Client) RefreshShadow(ctx context.Context) {
	groupsMu.Lock()
	defer groupsMu.Unlock()
	if groups, err := c.GroupsWithRules(ctx); err == nil {
		if len(mtShadow) > 0 && len(groups)+2 < len(mtShadow) {
			return
		}
		updateShadowLocked(groups)
	}
}

func (c *Client) Available(ctx context.Context) bool {
	_, err := c.Groups(ctx)
	return err == nil
}

func (c *Client) GroupsByInterface(ctx context.Context, device string) ([]Group, error) {
	groups, err := c.Groups(ctx)
	if err != nil {
		return nil, err
	}
	var out []Group
	for _, g := range groups {
		if g.Interface == device {
			out = append(out, g)
		}
	}
	return out, nil
}
