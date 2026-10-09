package premium

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func freePayload(id string) map[string]any {
	v := payload("", id, "")
	delete(v, "auth_data")
	delete(v, "service_type")
	return v
}

func freeError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(strings.ReplaceAll(err.Error(), "Premium", "Amnezia Free"))
}

func (c *Client) FreeService(ctx context.Context, id string) (string, string, error) {
	raw, err := c.post(ctx, "services", freePayload(id))
	if err != nil {
		return "", "", freeError(err)
	}
	var response struct {
		Country  string `json:"user_country_code"`
		Services []struct {
			Type      string          `json:"service_type"`
			Protocol  string          `json:"service_protocol"`
			Available json.RawMessage `json:"is_available"`
		} `json:"services"`
	}
	if json.Unmarshal(raw, &response) != nil || (response.Country != "default" && !countryCode.MatchString(response.Country)) || response.Services == nil {
		return "", "", errors.New("invalid Amnezia Free services catalog")
	}
	for _, service := range response.Services {
		if service.Type != "amnezia-free" {
			continue
		}
		if string(service.Available) == "false" {
			return "", "", fmt.Errorf("Amnezia Free is unavailable in detected region %s", response.Country)
		}
		if service.Protocol == "" {
			return "", "", errors.New("invalid Amnezia Free service protocol")
		}
		return response.Country, service.Protocol, nil
	}
	return "", "", fmt.Errorf("Amnezia Free is not offered in detected region %s", response.Country)
}

func (c *Client) FreeConfig(ctx context.Context, id, country, private string) ([]byte, json.RawMessage, error) {
	bad := errors.New("unsupported or invalid Amnezia Free AWG configuration")
	b, err := base64.StdEncoding.DecodeString(private)
	if err != nil {
		return nil, nil, bad
	}
	key, err := ecdh.X25519().NewPrivateKey(b)
	if err != nil {
		return nil, nil, bad
	}
	v := freePayload(id)
	v["user_country_code"], v["service_type"], v["service_protocol"] = country, "amnezia-free", "awg"
	v["public_key"] = base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
	raw, err := c.post(ctx, "config", v)
	if err != nil {
		return nil, nil, freeError(err)
	}
	var response struct {
		Config string `json:"config"`
	}
	if json.Unmarshal(raw, &response) != nil {
		return nil, nil, bad
	}
	doc, err := decodeLink(strings.Trim(response.Config, "\r\n"))
	if err != nil {
		return nil, nil, bad
	}
	var root struct {
		Version int `json:"config_version"`
		API     struct {
			Type     string `json:"service_type"`
			Protocol string `json:"service_protocol"`
		} `json:"api_config"`
		Auth json.RawMessage `json:"auth_data"`
	}
	if json.Unmarshal(doc, &root) != nil || root.Version != 2 || (root.API.Type != "" && root.API.Type != "amnezia-free") || (root.API.Protocol != "" && root.API.Protocol != "awg") {
		return nil, nil, bad
	}
	if len(root.Auth) > 0 && string(root.Auth) != "null" {
		var auth map[string]json.RawMessage
		if json.Unmarshal(root.Auth, &auth) != nil {
			return nil, nil, bad
		}
	}
	data, _, err := deviceConfig(response.Config, private)
	return data, root.Auth, freeError(err)
}
