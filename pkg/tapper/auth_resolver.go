package tapper

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/jlrickert/cli-toolkit/toolkit"

	"github.com/jlrickert/tapper/pkg/keg"
)

// authStoreTokenResolver implements keg.TokenResolver by looking up bearer
// tokens in an AuthStore, keyed by the canonical hub root derived from the
// remote target URL. When the cached access token is OAuth2-issued (carries a
// refresh token) and has expired or is about to, the resolver renews it via
// RefreshHubToken and persists the rotated pair before returning. A pasted
// `thub_` API token has no refresh token and is returned as-is.
type authStoreTokenResolver struct {
	store     *AuthStore
	rt        *toolkit.Runtime
	storePath string
	mu        sync.Mutex // serializes refresh so concurrent resolves don't double-renew
}

// NewAuthStoreTokenResolver returns a keg.TokenResolver backed by store. rt and
// storePath enable in-place refresh of expired OAuth2 tokens; pass the same
// runtime and AuthStorePath the store was loaded from. A nil store yields a
// resolver that always returns "", matching AuthStore's nil-safe contract.
func NewAuthStoreTokenResolver(store *AuthStore, rt *toolkit.Runtime, storePath string) keg.TokenResolver {
	return &authStoreTokenResolver{store: store, rt: rt, storePath: storePath}
}

// ResolveToken derives the canonical hub root from target and returns a usable
// access token, refreshing first when the cached one is expired (or near it)
// and a refresh token is available. Returns "" when the target scheme has no
// hub concept (file, memory) or the store has no entry. Refresh is best-effort:
// any failure falls back to the cached token, leaving the hub's 401 as the
// backstop signal.
func (r *authStoreTokenResolver) ResolveToken(target *keg.Target) string {
	if r == nil || r.store == nil || target == nil {
		return ""
	}
	hubRoot := hubRootFromTarget(target)
	if hubRoot == "" {
		return ""
	}
	key := CanonicalHubURL(hubRoot)
	entry, ok := r.store.Get(key)
	if !ok || entry == nil {
		return ""
	}
	if r.shouldRefresh(entry) {
		if next := r.refresh(hubRoot, key, entry); next != nil {
			return next.AccessToken
		}
	}
	return entry.AccessToken
}

// shouldRefresh reports whether entry has a refresh token and an access token
// that is expired or within refreshSkew of expiring.
func (r *authStoreTokenResolver) shouldRefresh(entry *AuthEntry) bool {
	return authEntryNeedsRefresh(r.rt, entry)
}

// refresh renews the token under a lock and persists the rotated pair. Returns
// the fresh entry, or nil on failure (callers fall back to the cached token).
func (r *authStoreTokenResolver) refresh(hubURL, key string, entry *AuthEntry) *AuthEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	next, err := refreshAuthStoreEntryIfNeeded(context.Background(), r.rt, r.store, r.storePath, hubURL, key, entry)
	if err != nil {
		if logger := r.rt.Logger(); logger != nil {
			logger.Debug("token refresh failed", "hub", hubURL, "err", err)
		}
	}
	if next != nil {
		return next
	}
	return nil
}

// ErrAtlasHubDisabled is returned by ResolveLoginHubURL when the chain
// would fall through to the compiled-in DefaultHubURL but the deployment
// has opted out via Config.DisableAtlasHub. Callers surface it verbatim
// so SOC2-conscious users see a stable string they can grep for.
var ErrAtlasHubDisabled = errors.New("no hub configured; implicit atlas hub disabled")

// ResolveLoginHubURL returns the hub URL the login flow should target,
// applying the hub resolution chain:
//
//  1. explicit non-empty → canonicalize and use
//  2. cfg.DefaultHub names a Hubs entry → use that entry's URL
//  3. cfg.FallbackHub names a Hubs entry → use that entry's URL
//  4. cfg.Hubs has exactly one remote/readonly entry → use it
//  5. cfg.DisableAtlasHub is true → ErrAtlasHubDisabled
//  6. fall back to DefaultHubURL
//
// A misconfigured DefaultHub or FallbackHub is a hard
// error rather than a silent fall-through to step 3 — typos should
// surface, not silently route to a different hub.
//
// Returned URLs are canonicalized via CanonicalHubURL so callers can
// compare them against AuthStore keys without re-canonicalizing.
func ResolveLoginHubURL(cfg *Config, explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return CanonicalHubURL(explicit), nil
	}
	if cfg == nil {
		return DefaultHubURL, nil
	}

	if name := strings.TrimSpace(cfg.DefaultHub()); name != "" {
		return loginHubURLFromConfigEntry(cfg, "default", name)
	}

	if name := strings.TrimSpace(cfg.FallbackHub()); name != "" {
		return loginHubURLFromConfigEntry(cfg, "fallback", name)
	}

	var (
		remoteName  string
		remoteEntry HubEntry
		remoteCount int
	)
	for name, h := range cfg.Hubs() {
		kind := strings.TrimSpace(h.Kind)
		if kind == "" {
			kind = HubKindRemote
		}
		if kind == HubKindLocal {
			continue
		}
		remoteName = name
		remoteEntry = h
		remoteCount++
	}
	if remoteCount == 1 {
		return loginHubURLFromEntry("hub", remoteName, remoteEntry)
	}

	if cfg.DisableAtlasHub() {
		return "", ErrAtlasHubDisabled
	}

	return DefaultHubURL, nil
}

func loginHubURLFromConfigEntry(cfg *Config, role, name string) (string, error) {
	h, ok := cfg.Hub(name)
	if !ok {
		return "", fmt.Errorf("auth: %s hub %q not found in hubs", role, name)
	}
	return loginHubURLFromEntry(role+" hub", name, h)
}

func loginHubURLFromEntry(label, name string, entry HubEntry) (string, error) {
	kind := strings.TrimSpace(entry.Kind)
	if kind == "" {
		kind = HubKindRemote
	}
	if kind == HubKindLocal {
		return "", fmt.Errorf("auth: %s %q is local and cannot be used for auth login", label, name)
	}
	if strings.TrimSpace(entry.URL) == "" {
		return "", fmt.Errorf("auth: %s %q has no URL configured", label, name)
	}
	return CanonicalHubURL(hubURLWithScheme(entry.URL)), nil
}

// hubURLWithScheme adds an https:// prefix when the configured Hubs entry
// stores a bare host (e.g. "keg.example.com"). The existing
// DefaultUserConfig template writes a hostname without a scheme, so this
// helper protects the chain from KegHub.Url shapes that do not round-trip
// through url.Parse. Existing scheme prefixes pass through unchanged so
// http://-only test hubs keep working.
func hubURLWithScheme(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, "://") {
		return trimmed
	}
	return "https://" + trimmed
}

// hubRootFromTarget reduces a remote target to the "scheme://host[:port]"
// form the AuthStore uses as its key. Non-remote schemes return "" so
// callers short-circuit without reaching into the store.
func hubRootFromTarget(target *keg.Target) string {
	switch target.Scheme() {
	case keg.SchemeHTTP, keg.SchemeHTTPs:
		parsed, err := url.Parse(strings.TrimSpace(target.Url))
		if err != nil || parsed.Host == "" {
			return ""
		}
		return fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
	case keg.SchemeAlias:
		// Key the auth store by the resolved hub host. HubURL is set from the
		// configured hubs map during resolution; without it there is no hub to
		// authenticate against.
		base := strings.TrimSpace(target.HubURL)
		if base == "" {
			return ""
		}
		parsed, err := url.Parse(hubURLWithScheme(base))
		if err != nil || parsed.Host == "" {
			return ""
		}
		return fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
	}
	return ""
}
