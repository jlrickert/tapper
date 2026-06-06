// Package tapper — OAuth2 refresh-token renewal for cached hub credentials.
//
// The device-login flow now persists a refresh token (plus the client_id and
// token endpoint it must be presented to) alongside the short-lived access
// token. RefreshHubToken exchanges that refresh token for a fresh access token
// so a command whose cached token has expired renews silently instead of
// forcing the user back through `tap auth login`.
package tapper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

// refreshGrantType is the RFC 6749 §6 grant for trading a refresh token for a
// fresh access token.
const refreshGrantType = "refresh_token"

// RefreshHubToken exchanges entry.RefreshToken at the hub's token endpoint and
// returns a new AuthEntry carrying the rotated access + refresh tokens and a
// recomputed expiry. The hub rotates refresh tokens single-use, so the caller
// must persist the returned entry (the old refresh token is now spent).
//
// Endpoint/client resolution: the device-login flow records TokenEndpoint and
// ClientID on the entry, so a refresh is a single POST with no rediscovery.
// Entries that predate that (or omit the fields) fall back to rediscovering the
// token endpoint from hubURL and to the stock public client id.
func RefreshHubToken(ctx context.Context, rt *toolkit.Runtime, hubURL string, entry *AuthEntry) (*AuthEntry, error) {
	if entry == nil || entry.RefreshToken == "" {
		return nil, fmt.Errorf("auth refresh: no refresh token available")
	}

	tokenEndpoint := entry.TokenEndpoint
	if tokenEndpoint == "" {
		md, err := discoverAuthServerMetadata(ctx, http.DefaultClient, hubURL)
		if err != nil {
			return nil, err
		}
		tokenEndpoint = md.TokenEndpoint
	}

	clientID := entry.ClientID
	if clientID == "" {
		clientID = "tapper-cli"
	}

	form := url.Values{}
	form.Set("grant_type", refreshGrantType)
	form.Set("refresh_token", entry.RefreshToken)
	form.Set("client_id", clientID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("auth refresh: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth refresh: contact hub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("auth refresh: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// A rejected/expired/replayed refresh token returns invalid_grant.
		// Surface the status so callers can fall back to a full re-login.
		return nil, fmt.Errorf("auth refresh: hub rejected refresh token (%s)", resp.Status)
	}

	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("auth refresh: parse response: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("auth refresh: response missing access_token")
	}

	next := &AuthEntry{
		AccessToken:   tok.AccessToken,
		TokenType:     tok.TokenType,
		Scope:         tok.Scope,
		RefreshToken:  tok.RefreshToken,
		ClientID:      clientID,
		TokenEndpoint: tokenEndpoint,
	}
	// Defensive carry-overs: never drop the ability to refresh again or lose
	// metadata if the hub omits a field on the refresh response.
	if next.RefreshToken == "" {
		next.RefreshToken = entry.RefreshToken
	}
	if next.TokenType == "" {
		next.TokenType = entry.TokenType
	}
	if next.Scope == "" {
		next.Scope = entry.Scope
	}
	if tok.ExpiresIn > 0 {
		next.ExpiresAt = rt.Clock().Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	return next, nil
}
