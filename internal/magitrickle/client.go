package magitrickle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
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
		HTTP:    &http.Client{Timeout: 5 * time.Second},
	}
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
		return fmt.Errorf("%s %s: status %d", method, path, resp.StatusCode)
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
	return c.call(ctx, http.MethodPut, "/api/v1/groups?save="+strconv.FormatBool(save), body, nil)
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
