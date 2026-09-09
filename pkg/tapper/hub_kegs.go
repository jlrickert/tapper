// Package tapper — hub keg-catalog client calls.
//
// These talk to a remote hub's namespace/keg-catalog endpoints (as opposed to
// the per-keg operation API in pkg/keg/keg_remote.go): creating a keg and listing the
// kegs a user can reach. They mirror the slim, dependency-free shape of
// ValidateToken (auth_whoami.go) — a stand-alone function over
// http.DefaultClient so tests can point hubURL at an httptest.Server.
package tapper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/jlrickert/tapper/pkg/keg"
)

// hubKegsPath is the user-scoped keg listing: every keg the bearer token can
// reach across all namespaces on the hub (membership + grants). Mirrors the
// hub's GET /api/v1/kegs handler (handler.ListUserKegs).
const hubKegsPath = "/api/v1/kegs"

// HubKeg is one keg the hub reports the authenticated user can reach. It
// mirrors the hub's handler.UserKegItem JSON body — keep the two in sync.
type HubKeg struct {
	Namespace   string `json:"namespace"`
	Alias       string `json:"alias"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
	Role        string `json:"role"`
}

// CreateKeg asks the hub to create @namespace/alias via
// POST /api/v1/@{namespace}/kegs. A 409 is returned as an error wrapping
// keg.ErrExist so callers can detect "already exists" with errors.Is; 401
// wraps ErrTokenRejected; 403 wraps keg.ErrForbidden; other non-2xx statuses surface the hub's status line.
func CreateKeg(ctx context.Context, hubURL, token, namespace, alias, title, visibility string) error {
	base, err := normalizeHubURL(hubURL)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{
		"alias":      alias,
		"title":      title,
		"visibility": visibility,
	})
	endpoint := fmt.Sprintf("%s/api/v1/@%s/kegs", base, namespace)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("hub: build create-keg request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := hubHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("hub: contact hub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		return nil
	case http.StatusConflict:
		return fmt.Errorf("hub: keg @%s/%s already exists: %w", namespace, alias, keg.ErrExist)
	case http.StatusForbidden:
		return fmt.Errorf("hub: %w (%s)%s", keg.ErrForbidden, resp.Status, readHubError(resp))
	case http.StatusUnauthorized:
		return fmt.Errorf("hub: %w (%s)", ErrTokenRejected, resp.Status)
	default:
		return fmt.Errorf("hub: create keg @%s/%s failed: %s%s", namespace, alias, resp.Status, readHubError(resp))
	}
}

// ListUserKegs returns every keg the hub reports the bearer token can reach
// (namespace membership + grants) via GET /api/v1/kegs.
func ListUserKegs(ctx context.Context, hubURL, token string) ([]HubKeg, error) {
	base, err := normalizeHubURL(hubURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+hubKegsPath, nil)
	if err != nil {
		return nil, fmt.Errorf("hub: build list-kegs request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := hubHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("hub: contact hub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through to decode
	case http.StatusForbidden:
		return nil, fmt.Errorf("hub: %w (%s)%s", keg.ErrForbidden, resp.Status, readHubError(resp))
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("hub: %w (%s)", ErrTokenRejected, resp.Status)
	default:
		return nil, fmt.Errorf("hub: list kegs returned %s for %s", resp.Status, hubKegsPath)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("hub: read list-kegs response: %w", err)
	}
	var kegs []HubKeg
	if err := json.Unmarshal(body, &kegs); err != nil {
		return nil, fmt.Errorf("hub: parse list-kegs response: %w", err)
	}
	return kegs, nil
}

// readHubError best-effort extracts the hub's {"error": ...} message from a
// failed response so the surfaced error carries the hub's own explanation.
func readHubError(resp *http.Response) string {
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil || len(b) == 0 {
		return ""
	}
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error != "" {
		return ": " + e.Error
	}
	return ""
}

// hubHTTPClient refuses redirects so credentials and mutations stay on the selected Hub.
func hubHTTPClient() *http.Client {
	client := *http.DefaultClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}

// UnmarshalJSON accepts summary from older Hubs without overriding an explicit description.
func (k *HubKeg) UnmarshalJSON(data []byte) error {
	type plain HubKeg
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, ok := fields["description"]; !ok {
		if raw, exists := fields["summary"]; exists {
			if err := json.Unmarshal(raw, &v.Description); err != nil {
				return err
			}
		}
	}
	*k = HubKeg(v)
	return nil
}
