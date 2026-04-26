package tapper

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// authStoreTokenResolver implements keg.TokenResolver by looking up bearer
// tokens in an AuthStore, keyed by the canonical hub root derived from the
// remote target URL. Expired tokens are returned as-is — the hub's 401 is
// the authoritative signal, and refresh lives in the auth flow, not here.
type authStoreTokenResolver struct {
	store *AuthStore
}

// NewAuthStoreTokenResolver returns a keg.TokenResolver backed by store.
// A nil store yields a resolver that always returns "", matching the
// nil-safe contract of AuthStore itself.
func NewAuthStoreTokenResolver(store *AuthStore) keg.TokenResolver {
	return &authStoreTokenResolver{store: store}
}

// ResolveToken derives the canonical hub root from target and returns the
// cached access token, or "" when the target scheme has no hub concept
// (file, memory) or the store has no entry.
func (r *authStoreTokenResolver) ResolveToken(target *keg.Target) string {
	if r == nil || r.store == nil || target == nil {
		return ""
	}
	hubRoot := hubRootFromTarget(target)
	if hubRoot == "" {
		return ""
	}
	entry, ok := r.store.Get(CanonicalHubURL(hubRoot))
	if !ok || entry == nil {
		return ""
	}
	return entry.AccessToken
}

// ErrDefaultHubDisabled is returned by ResolveLoginHubURL when the chain
// would fall through to the compiled-in DefaultHubURL but the deployment
// has opted out via Config.DisableDefaultHub. Callers surface it verbatim
// so SOC2-conscious users see a stable string they can grep for.
var ErrDefaultHubDisabled = errors.New("no hub configured; implicit default disabled")

// ResolveLoginHubURL returns the hub URL the login flow should target,
// applying the five-step resolution chain documented in keg-dev/1035:
//
//  1. explicit non-empty → canonicalize and use
//  2. cfg.DefaultHub names a Hubs entry → use that entry's URL
//  3. cfg.Hubs has exactly one entry → use it
//  4. cfg.DisableDefaultHub is true → ErrDefaultHubDisabled
//  5. fall back to DefaultHubURL
//
// A misconfigured DefaultHub (set, but no matching Hubs entry) is a hard
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
		hubs := cfg.Hubs()
		for _, h := range hubs {
			if h.Name == name {
				if strings.TrimSpace(h.Url) == "" {
					return "", fmt.Errorf("auth: default hub %q has no URL configured", name)
				}
				return CanonicalHubURL(hubURLWithScheme(h.Url)), nil
			}
		}
		return "", fmt.Errorf("auth: default hub %q not found in hubs", name)
	}

	hubs := cfg.Hubs()
	if len(hubs) == 1 {
		if strings.TrimSpace(hubs[0].Url) == "" {
			return "", fmt.Errorf("auth: hub %q has no URL configured", hubs[0].Name)
		}
		return CanonicalHubURL(hubURLWithScheme(hubs[0].Url)), nil
	}

	if cfg.DisableDefaultHub() {
		return "", ErrDefaultHubDisabled
	}

	return DefaultHubURL, nil
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
	case keg.SchemeHub:
		hub := strings.TrimSpace(target.Hub)
		if hub == "" {
			return ""
		}
		return "https://" + hub
	}
	return ""
}
