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
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/jlrickert/tapper/pkg/keg"
)

// hubKegsPath is the user-scoped keg listing: every keg the bearer token can
// reach across all namespaces on the hub (membership + grants). Mirrors the
// hub's GET /api/v1/kegs handler (handler.ListUserKegs).
const hubKegsPath = "/api/v1/kegs"

const (
	hubOrientPath        = "/api/v1/orient"
	hubOrientDetailsPath = "/api/v1/orient/details"
)

// ErrOrientationUnsupported signals that a hub predates the progressive-
// disclosure orientation endpoints. Callers may safely use compatibility
// fallbacks without treating other HTTP failures as feature absence.
var ErrOrientationUnsupported = errors.New("hub orientation API is unavailable")

// HubKeg is one keg the hub reports the authenticated user can reach. It
// mirrors the hub's handler.UserKegItem JSON body — keep the two in sync.
type HubKeg struct {
	Namespace  string `json:"namespace"`
	Alias      string `json:"alias"`
	Visibility string `json:"visibility"`
	Role       string `json:"role"`
}

// HubOrientationKeg is one compact discovery row returned by a compatible
// Hub. Instructions are intentionally absent.
type HubOrientationKeg struct {
	Namespace  string `json:"namespace"`
	Alias      string `json:"alias"`
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	Visibility string `json:"visibility"`
	Role       string `json:"role"`
}

// HubOrientationDetail is one explicitly requested KEG config projection.
type HubOrientationDetail struct {
	Keg          string `json:"keg"`
	Title        string `json:"title"`
	Summary      string `json:"summary"`
	Updated      string `json:"updated,omitempty"`
	Instructions string `json:"instructions"`
}

// CreateKeg asks the hub to create @namespace/alias via
// POST /api/v1/@{namespace}/kegs. A 409 is returned as an error wrapping
// keg.ErrExist so callers can detect "already exists" with errors.Is; 401/403
// wrap ErrTokenRejected; other non-2xx statuses surface the hub's status line.
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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("hub: contact hub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		return nil
	case http.StatusConflict:
		return fmt.Errorf("hub: keg @%s/%s already exists: %w", namespace, alias, keg.ErrExist)
	case http.StatusUnauthorized, http.StatusForbidden:
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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hub: contact hub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through to decode
	case http.StatusUnauthorized, http.StatusForbidden:
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

// DiscoverOrientationKegs fetches the compact authenticated discovery index.
// A 404 or 405 is reported as ErrOrientationUnsupported so callers can fall
// back to the older /api/v1/kegs surface.
func DiscoverOrientationKegs(ctx context.Context, hubURL, token string) ([]HubOrientationKeg, error) {
	base, err := normalizeHubURL(hubURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+hubOrientPath, nil)
	if err != nil {
		return nil, fmt.Errorf("hub: build orientation discovery request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hub: contact hub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return nil, ErrOrientationUnsupported
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("hub: %w (%s)", ErrTokenRejected, resp.Status)
	default:
		return nil, fmt.Errorf("hub: orientation discovery returned %s for %s", resp.Status, hubOrientPath)
	}
	var out []HubOrientationKeg
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("hub: parse orientation discovery response: %w", err)
	}
	return out, nil
}

// FetchOrientationDetails requests targeted guidance for canonical KEG refs.
// The Hub guarantees all-or-nothing authorization and preserves input order.
func FetchOrientationDetails(ctx context.Context, hubURL, token string, refs []string) ([]HubOrientationDetail, error) {
	base, err := normalizeHubURL(hubURL)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(struct {
		Kegs []string `json:"kegs"`
	}{Kegs: refs})
	if err != nil {
		return nil, fmt.Errorf("hub: encode orientation details request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+hubOrientDetailsPath, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("hub: build orientation details request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hub: contact hub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		// New Hubs use 404 UNAVAILABLE for an invalid/unauthorized target as
		// well as old Hubs for an unknown route. Distinguish by the structured
		// code before deciding whether compatibility fallback is safe.
		var body struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err == nil && body.Code == "UNAVAILABLE" {
			return nil, errors.New(body.Error)
		}
		return nil, ErrOrientationUnsupported
	case http.StatusMethodNotAllowed:
		return nil, ErrOrientationUnsupported
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("hub: %w (%s)", ErrTokenRejected, resp.Status)
	default:
		return nil, fmt.Errorf("hub: orientation details returned %s for %s%s", resp.Status, hubOrientDetailsPath, readHubError(resp))
	}
	var out []HubOrientationDetail
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("hub: parse orientation details response: %w", err)
	}
	return out, nil
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
