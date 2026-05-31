// Package tapper — hub token validation via the whoami probe.
//
// ValidateToken confirms a bearer token resolves to a user on the hub. It
// backs the "paste an authentication token" login path: the token the user
// pastes (an API token minted on the hub's account page) is checked against
// GET /api/v1/whoami before it is written to the AuthStore, so a typo or a
// revoked token fails fast at login rather than on the first real request.
package tapper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

// whoamiPath is the hub's identity probe, mounted under the SessionOrBearer
// route group. The wire shape mirrors the hub's handler.WhoamiResponse.
const whoamiPath = "/api/v1/whoami"

// WhoAmI is the authenticated user the hub reports for a token. It mirrors
// the hub's GET /api/v1/whoami JSON body.
type WhoAmI struct {
	UserID    int64     `json:"user_id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

// ValidateToken calls GET {hubURL}/api/v1/whoami with the bearer token and
// returns the resolved user. A 401/403 is reported as a rejected token; any
// other non-200 surfaces the hub's status so misconfigurations are visible.
// The default HTTP client is used; callers that need to inject one for tests
// can hit an httptest.Server, whose URL works with http.DefaultClient.
func ValidateToken(ctx context.Context, rt *toolkit.Runtime, hubURL, token string) (*WhoAmI, error) {
	if rt == nil {
		return nil, fmt.Errorf("auth: runtime is required")
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("auth: token is required")
	}
	base, err := normalizeHubURL(hubURL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+whoamiPath, nil)
	if err != nil {
		return nil, fmt.Errorf("auth: build whoami request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth: contact hub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through to decode
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, fmt.Errorf("auth: hub rejected the token (%s); check that it was copied correctly and has not been revoked", resp.Status)
	default:
		return nil, fmt.Errorf("auth: hub returned %s for %s", resp.Status, whoamiPath)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("auth: read whoami response: %w", err)
	}
	var who WhoAmI
	if err := json.Unmarshal(body, &who); err != nil {
		return nil, fmt.Errorf("auth: parse whoami response: %w", err)
	}
	return &who, nil
}
