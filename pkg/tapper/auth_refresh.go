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
	"errors"
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

// ErrRefreshRejected means the hub refused the refresh token itself: its
// token endpoint answered with an OAuth error body whose `error` is
// invalid_grant or invalid_client, whatever the HTTP status. The sign-in is
// gone and only a new `tap auth login` restores it. Every other failure is
// not a refusal and may succeed on a later attempt: an unreachable hub, a
// 5xx, a server_error or invalid_request answer, or a body that does not
// parse as an OAuth error.
var ErrRefreshRejected = errors.New("auth refresh: hub rejected refresh token")

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
// rotation race: the refresh runs under an OS lock on the store file
// (lockAuthStore), so of several processes sharing one store only the first
// spends the refresh token, and the rest adopt the pair it persisted when they
// get the lock. A process that cannot get the lock in time never refreshes
// unlocked: it adopts a fresher on-disk entry if there is one, and otherwise
// fails so the caller keeps its cached token. Under the lock the store file is
// authoritative: a refresh spends the refresh token stored there, not a stale
// in-memory one, does not run at all once a logout removed the entry, and
// persists only over the entry it started from.
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

	if storePath != "" {
		unlock, err := lockAuthStore(ctx, rt, storePath, authStoreLockWait)
		if err != nil {
			if disk := adoptFresherAuthEntryFromDisk(ctx, rt, store, storePath, key); disk != nil {
				return disk, nil
			}
			return nil, fmt.Errorf("auth refresh: %w", err)
		}
		defer unlock()
		// Another process may have refreshed while this one waited.
		if disk := adoptFresherAuthEntryFromDisk(ctx, rt, store, storePath, key); disk != nil {
			return disk, nil
		}
		// The store file, not this process's copy, holds the live login. A
		// logout may have removed it, and another process may have rotated
		// the refresh token this copy holds: presenting the spent one is a
		// replay, and the hub revokes the whole sign-in for it.
		if disk, err := LoadAuthStore(ctx, rt, storePath); err == nil {
			cur, ok := disk.Get(key)
			if !ok || cur == nil {
				store.Delete(key)
				return nil, fmt.Errorf("auth refresh: login for %s was removed", key)
			}
			if cur.RefreshToken != entry.RefreshToken {
				entry = cur
			}
		}
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

	keepSpoolAcrossRotation(ctx, rt, storePath, key, hubURL, entry, next)
	if err := persistAuthStoreEntry(ctx, rt, storePath, key, entry.RefreshToken, next); err != nil {
		if errors.Is(err, errAuthStoreEntryChanged) {
			// A logout or a new login replaced the entry this refresh
			// started from; that write wins over the rotated pair.
			if disk := adoptFresherAuthEntryFromDisk(ctx, rt, store, storePath, key); disk != nil {
				return disk, nil
			}
			if disk, lerr := LoadAuthStore(ctx, rt, storePath); lerr == nil {
				if cur, ok := disk.Get(key); ok && cur != nil {
					store.Set(key, *cur)
				} else {
					store.Delete(key)
				}
			}
			return nil, fmt.Errorf("auth refresh: %w", err)
		}
		store.Set(key, *next)
		return next, fmt.Errorf("auth refresh: persist: %w", err)
	}
	store.Set(key, *next)
	return next, nil
}

// errAuthStoreEntryChanged means the stored entry a refresh started from was
// removed or replaced before the refresh could persist its rotated pair.
var errAuthStoreEntryChanged = errors.New("auth store entry changed during refresh")

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

// persistAuthStoreEntry writes entry under key only while the stored entry is
// still the one the refresh started from, the one holding refreshToken.
// Otherwise it writes nothing and returns errAuthStoreEntryChanged, so a
// refresh never brings back a logged-out entry or replaces a newer login.
func persistAuthStoreEntry(ctx context.Context, rt *toolkit.Runtime, storePath, key, refreshToken string, entry *AuthEntry) error {
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
	prev, ok := disk.Get(key)
	if !ok || prev == nil || prev.RefreshToken != refreshToken {
		return errAuthStoreEntryChanged
	}
	carryAuthAccountKey(entry, prev)
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

	resp, err := hubHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth refresh: contact hub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("auth refresh: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, refreshFailure(resp.Status, body)
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
	carryAuthAccountKey(next, entry)
	if tok.ExpiresIn > 0 {
		next.ExpiresAt = rt.Clock().Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	return next, nil
}

// refreshFailure classifies a non-200 token-endpoint answer. Only an OAuth
// error body naming invalid_grant (the refresh token is expired, revoked, or
// spent) or invalid_client (the client is gone) is ErrRefreshRejected; the
// status code alone decides nothing, because a hub whose store is failing can
// answer 400 or 401 for a credential that is still good. Everything else is
// an ordinary error the caller may retry.
func refreshFailure(status string, body []byte) error {
	var oauthErr tokenErrorResponse
	if json.Unmarshal(body, &oauthErr) == nil {
		switch oauthErr.Error {
		case "invalid_grant", "invalid_client":
			return fmt.Errorf("%w (%s: %s)", ErrRefreshRejected, status, oauthErr.Error)
		case "":
		default:
			return fmt.Errorf("auth refresh: hub answered %s: %s", status, oauthErr.Error)
		}
	}
	return fmt.Errorf("auth refresh: hub answered %s", status)
}
