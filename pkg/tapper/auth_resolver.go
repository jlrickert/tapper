package tapper

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
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
func (r *authStoreTokenResolver) ResolveToken(target *kegurl.Target) string {
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

// hubRootFromTarget reduces a remote target to the "scheme://host[:port]"
// form the AuthStore uses as its key. Non-remote schemes return "" so
// callers short-circuit without reaching into the store.
func hubRootFromTarget(target *kegurl.Target) string {
	switch target.Scheme() {
	case kegurl.SchemeHTTP, kegurl.SchemeHTTPs:
		parsed, err := url.Parse(strings.TrimSpace(target.Url))
		if err != nil || parsed.Host == "" {
			return ""
		}
		return fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
	case kegurl.SchemeRegistry:
		repo := strings.TrimSpace(target.Repo)
		if repo == "" {
			return ""
		}
		return "https://" + repo
	}
	return ""
}
