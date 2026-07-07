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

const (
	// refreshSkew is how far ahead of the access-token expiry we renew, so an
	// in-flight request doesn't race a 401 against a token that lapses mid-call.
	refreshSkew = 30 * time.Second

	authRefreshTimeout = 10 * time.Second
)

// authEntryNeedsRefresh reports whether entry has a refresh token and an access
// token that is expired or within refreshSkew of expiring.
func authEntryNeedsRefresh(rt *toolkit.Runtime, entry *AuthEntry) bool {
	if entry == nil || entry.RefreshToken == "" || entry.ExpiresAt.IsZero() || rt == nil {
		return false
	}
	return !rt.Clock().Now().Add(refreshSkew).Before(entry.ExpiresAt)
}

// refreshAuthStoreEntryIfNeeded renews one stored hub credential when its
// access token is expired or close to expiry. It also handles the cross-process
// rotation race: before contacting the hub, and again after a rejected refresh,
// it adopts a fresher on-disk entry when another process has already persisted
// one.
func refreshAuthStoreEntryIfNeeded(ctx context.Context, rt *toolkit.Runtime, store *AuthStore, storePath, hubURL, key string, entry *AuthEntry) (*AuthEntry, error) {
	if !authEntryNeedsRefresh(rt, entry) {
		return entry, nil
	}
	if store == nil {
		return nil, fmt.Errorf("auth refresh: store is nil")
	}

	// Re-read the in-memory store first: the resolver calls this under its
	// mutex, so a sibling goroutine may have already rotated the token.
	if cur, ok := store.Get(key); ok && cur != nil {
		if !authEntryNeedsRefresh(rt, cur) {
			return cur, nil
		}
		entry = cur
	}

	if disk := adoptFresherAuthEntryFromDisk(ctx, rt, store, storePath, key); disk != nil {
		return disk, nil
	}

	rctx, cancel := context.WithTimeout(ctx, authRefreshTimeout)
	next, err := RefreshHubToken(rctx, rt, hubURL, entry)
	cancel()
	if err != nil {
		if disk := adoptFresherAuthEntryFromDisk(ctx, rt, store, storePath, key); disk != nil {
			return disk, nil
		}
		return nil, err
	}

	store.Set(key, *next)
	if err := persistAuthStoreEntry(ctx, rt, storePath, key, next); err != nil {
		return next, fmt.Errorf("auth refresh: persist: %w", err)
	}
	return next, nil
}

// adoptFresherAuthEntryFromDisk re-reads the auth store file and, when it holds
// an entry for key that does not itself need refreshing, copies it into store
// and returns it. This lets long-running processes and parallel commands pick
// up a rotated token pair without spending an already-consumed refresh token.
func adoptFresherAuthEntryFromDisk(ctx context.Context, rt *toolkit.Runtime, store *AuthStore, storePath, key string) *AuthEntry {
	if storePath == "" || rt == nil || store == nil {
		return nil
	}
	disk, err := LoadAuthStore(ctx, rt, storePath)
	if err != nil {
		if logger := rt.Logger(); logger != nil {
			logger.Debug("auth store reload failed", "path", storePath, "err", err)
		}
		return nil
	}
	entry, ok := disk.Get(key)
	if !ok || entry == nil || authEntryNeedsRefresh(rt, entry) {
		return nil
	}
	store.Set(key, *entry)
	return entry
}

func persistAuthStoreEntry(ctx context.Context, rt *toolkit.Runtime, storePath, key string, entry *AuthEntry) error {
	if rt == nil {
		return fmt.Errorf("auth refresh: runtime is nil")
	}
	if storePath == "" {
		return fmt.Errorf("auth refresh: store path is empty")
	}
	if entry == nil {
		return fmt.Errorf("auth refresh: entry is nil")
	}
	disk, err := LoadAuthStore(ctx, rt, storePath)
	if err != nil {
		return err
	}
	disk.Set(key, *entry)
	return disk.Save(ctx, rt, storePath)
}

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
