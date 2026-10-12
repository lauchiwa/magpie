package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/identity"
)

// ArmedIdentity is the safe control-plane view of a live identity capture.
// It contains no request headers, credentials or request/session ids.
type ArmedIdentity struct {
	Provider string
	Client   identity.Client
	Expires  *time.Time
}

// ArmIdentity asks a running gateway on baseURL to hold one matching request
// for providerID. The gateway, not this caller, owns the memory-only window.
func ArmIdentity(ctx context.Context, baseURL, providerID string, client identity.Client) error {
	q := url.Values{"provider": {providerID}, "client": {string(client)}}
	_, err := controlRequest(ctx, http.MethodPost, controlURL(baseURL, IdentityArmPath), q)
	return err
}

// DisarmIdentity revokes a provider's live capture window on the gateway.
func DisarmIdentity(ctx context.Context, baseURL, providerID string) error {
	q := url.Values{"provider": {providerID}}
	_, err := controlRequest(ctx, http.MethodPost, controlURL(baseURL, IdentityDisarmPath), q)
	return err
}

// ArmedIdentities returns the live windows. New gateways return client and
// expiry in details. Older gateways returned only data provider ids, which
// remains a useful, safe result with Client and Expires left empty.
func ArmedIdentities(ctx context.Context, baseURL string) ([]ArmedIdentity, error) {
	b, err := controlRequest(ctx, http.MethodGet, controlURL(baseURL, IdentityArmedPath), nil)
	if err != nil {
		return nil, err
	}
	var reply struct {
		Data    []string `json:"data"`
		Details []struct {
			Provider string `json:"provider"`
			Client   string `json:"client"`
			Expires  string `json:"expiresAt"`
		} `json:"details"`
	}
	if err := json.Unmarshal(b, &reply); err != nil {
		return nil, fmt.Errorf("decode gateway identity status: %w", err)
	}
	out := make([]ArmedIdentity, 0, len(reply.Data)+len(reply.Details))
	seen := map[string]bool{}
	for _, detail := range reply.Details {
		if detail.Provider == "" || seen[detail.Provider] {
			continue
		}
		item := ArmedIdentity{Provider: detail.Provider, Client: identity.Client(detail.Client)}
		if detail.Expires != "" {
			expires, err := time.Parse(time.RFC3339Nano, detail.Expires)
			if err != nil {
				return nil, fmt.Errorf("decode gateway identity expiry for %q: %w", detail.Provider, err)
			}
			item.Expires = &expires
		}
		seen[item.Provider] = true
		out = append(out, item)
	}
	for _, provider := range reply.Data {
		if provider == "" || seen[provider] {
			continue
		}
		seen[provider] = true
		out = append(out, ArmedIdentity{Provider: provider})
	}
	return out, nil
}

func controlURL(baseURL, path string) string {
	return strings.TrimRight(baseURL, "/") + path
}

func controlRequest(ctx context.Context, method, endpoint string, query url.Values) ([]byte, error) {
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, err
	}
	res, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("gateway identity control: %w", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 8192))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("gateway identity control: %s: %s", res.Status, controlError(b))
	}
	return b, nil
}

func controlError(b []byte) string {
	var reply struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &reply) == nil && reply.Error.Message != "" {
		return reply.Error.Message
	}
	if s := strings.TrimSpace(string(b)); s != "" {
		return s
	}
	return "no error message"
}
