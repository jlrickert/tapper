// Package tapper — hub keg-administration client calls (grants + visibility).
//
// These talk to a remote hub's per-keg admin endpoints under
// /api/v1/@{namespace}/kegs/{keg}: listing/creating/revoking grants and
// updating visibility. Like ValidateToken (auth_whoami.go) and CreateKeg
// (hub_kegs.go) they are slim, dependency-free functions over
// http.DefaultClient so tests can point hubURL at an httptest.Server.
package tapper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// HubGrant is one per-(user, role) grant on a keg. Mirrors the hub's
// handler.ListGrants JSON body — keep the two in sync. (granted_at is sent by
// the hub but unused here.)
type HubGrant struct {
	Username string `json:"username"`
	Role     string `json:"role"` // viewer|editor|admin
}

func kegGrantsPath(namespace, alias string) string {
	return fmt.Sprintf("/api/v1/@%s/kegs/%s/grants", namespace, alias)
}

// ListGrants returns the grants on @namespace/alias via
// GET /api/v1/@{namespace}/kegs/{keg}/grants (admin-only on the hub).
func ListGrants(ctx context.Context, hubURL, token, namespace, alias string) ([]HubGrant, error) {
	var out []HubGrant
	if err := doHubJSON(ctx, http.MethodGet, hubURL, token, kegGrantsPath(namespace, alias), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateGrant upserts a grant via POST .../grants. role is viewer|editor|admin.
func CreateGrant(ctx context.Context, hubURL, token, namespace, alias, username, role string) error {
	payload := map[string]string{"username": strings.TrimPrefix(strings.TrimSpace(username), "@"), "role": role}
	return doHubJSON(ctx, http.MethodPost, hubURL, token, kegGrantsPath(namespace, alias), payload, nil)
}

// RevokeGrant removes a grant via DELETE .../grants/@{username}.
func RevokeGrant(ctx context.Context, hubURL, token, namespace, alias, username string) error {
	path := kegGrantsPath(namespace, alias) + "/@" + strings.TrimPrefix(strings.TrimSpace(username), "@")
	return doHubJSON(ctx, http.MethodDelete, hubURL, token, path, nil, nil)
}

// SetKegVisibility updates a keg's visibility via PATCH .../settings. visibility
// is public|private.
func SetKegVisibility(ctx context.Context, hubURL, token, namespace, alias, visibility string) error {
	path := fmt.Sprintf("/api/v1/@%s/kegs/%s/settings", namespace, alias)
	return doHubJSON(ctx, http.MethodPatch, hubURL, token, path, map[string]string{"visibility": visibility}, nil)
}

// doHubJSON performs one JSON round-trip against the hub, decoding a non-nil
// out on success. It maps hub statuses to the shared sentinels (409→ErrExist,
// 404→ErrNotExist, 401/403→ErrTokenRejected) so callers can branch with
// errors.Is, and surfaces the hub's {"error": ...} message otherwise. Shared by
// the grants and namespace client calls; the flight calls use the parallel
// doHubFlightJSON.
func doHubJSON(ctx context.Context, method, hubURL, token, path string, payload, out any) error {
	base, err := normalizeHubURL(hubURL)
	if err != nil {
		return err
	}
	var body io.Reader
	if payload != nil {
		b, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return fmt.Errorf("hub: encode request: %w", marshalErr)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return fmt.Errorf("hub: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("hub: contact hub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
	case http.StatusNoContent:
		return nil
	case http.StatusConflict:
		return fmt.Errorf("hub: %w%s", keg.ErrExist, readHubError(resp))
	case http.StatusNotFound:
		return fmt.Errorf("hub: %w%s", keg.ErrNotExist, readHubError(resp))
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("hub: %w (%s)%s", ErrTokenRejected, resp.Status, readHubError(resp))
	default:
		return fmt.Errorf("hub: request failed: %s%s", resp.Status, readHubError(resp))
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("hub: parse response: %w", err)
	}
	return nil
}
