package tapper_test

import (
	"testing"

	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// TestAuthStoreTokenResolver_ResolveToken exercises hub-root derivation and
// canonicalisation across every target shape the resolver cares about.
func TestAuthStoreTokenResolver_ResolveToken(t *testing.T) {
	t.Parallel()

	const hubURL = "https://hub.example.com"
	const altHubHost = "alt-hub.example.com"
	const hubToken = "hub-token"
	const altHubToken = "alt-hub-token"

	newStore := func() *tapper.AuthStore {
		s := &tapper.AuthStore{}
		s.Set(tapper.CanonicalHubURL(hubURL), tapper.AuthEntry{AccessToken: hubToken})
		s.Set(tapper.CanonicalHubURL("https://"+altHubHost), tapper.AuthEntry{AccessToken: altHubToken})
		return s
	}

	cases := []struct {
		name   string
		store  *tapper.AuthStore
		target kegurl.Target
		want   string
	}{
		{
			name:   "http target with matching hub root hits",
			store:  newStore(),
			target: kegurl.Target{Url: "http://hub.example.com/api/v1/kegs/@me/demo"},
			want:   "", // no http entry seeded; ensures scheme-sensitivity
		},
		{
			name:   "https target with matching hub root hits",
			store:  newStore(),
			target: kegurl.Target{Url: "https://hub.example.com/api/v1/kegs/@me/demo"},
			want:   hubToken,
		},
		{
			name:   "https target with trailing slash still matches",
			store:  newStore(),
			target: kegurl.Target{Url: "https://hub.example.com/"},
			want:   hubToken,
		},
		{
			name:   "uppercase scheme still matches via canonicalisation",
			store:  newStore(),
			target: kegurl.Target{Url: "HTTPS://Hub.Example.com/api/v1/kegs/@me/demo"},
			want:   hubToken,
		},
		{
			name:   "https store miss returns empty",
			store:  newStore(),
			target: kegurl.Target{Url: "https://other.example.com"},
			want:   "",
		},
		{
			name:   "hub target derives https://<hub>",
			store:  newStore(),
			target: kegurl.Target{Hub: altHubHost, User: "me", Keg: "demo"},
			want:   altHubToken,
		},
		{
			name:   "file target short-circuits to empty",
			store:  newStore(),
			target: kegurl.Target{File: "/tmp/keg"},
			want:   "",
		},
		{
			name:   "memory target short-circuits to empty",
			store:  newStore(),
			target: kegurl.Target{Memory: true, Keg: "m"},
			want:   "",
		},
		{
			name:   "nil store yields empty for every input",
			store:  nil,
			target: kegurl.Target{Url: "https://hub.example.com"},
			want:   "",
		},
		{
			name:   "malformed url yields empty without panicking",
			store:  newStore(),
			target: kegurl.Target{Url: "https://"},
			want:   "",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resolver := tapper.NewAuthStoreTokenResolver(tc.store)
			require.NotNil(t, resolver)
			got := resolver.ResolveToken(&tc.target)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestAuthStoreTokenResolver_NilTargetSafe guards the nil-target contract —
// the resolver should not panic even when a malformed call path passes nil.
func TestAuthStoreTokenResolver_NilTargetSafe(t *testing.T) {
	t.Parallel()

	store := &tapper.AuthStore{}
	store.Set(tapper.CanonicalHubURL("https://hub.example.com"), tapper.AuthEntry{AccessToken: "t"})

	resolver := tapper.NewAuthStoreTokenResolver(store)
	require.Equal(t, "", resolver.ResolveToken(nil))
}
