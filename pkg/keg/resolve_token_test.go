package keg_test

import (
	"testing"

	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// stubResolver is a table-driven TokenResolver used to assert precedence
// without reaching into pkg/tapper. A canned return value is enough —
// we never need to inspect the Target.
type stubResolver struct {
	token  string
	called int
}

func (s *stubResolver) ResolveToken(_ *kegpkg.Target) string {
	s.called++
	return s.token
}

// TestResolveTargetToken_Precedence exercises the TokenEnv → Token → resolver
// precedence indirectly through NewKegFromTarget, inspecting RemoteKeg.Token
// on the resulting keg. Using the public entry point keeps the test honest:
// it verifies the wiring, not just the raw helper.
func TestResolveTargetToken_Precedence(t *testing.T) {
	t.Parallel()

	type want struct {
		token          string
		resolverCalled bool
	}

	cases := []struct {
		name     string
		env      map[string]string
		target   kegpkg.Target
		resolver *stubResolver
		want     want
	}{
		{
			name: "env wins when TokenEnv is set and value is present",
			env:  map[string]string{"HUB_TOKEN": "env-value"},
			target: kegpkg.Target{
				Url:      "https://hub.example.com",
				TokenEnv: "HUB_TOKEN",
				Token:    "literal-ignored",
			},
			resolver: &stubResolver{token: "resolver-ignored"},
			want:     want{token: "env-value", resolverCalled: false},
		},
		{
			name: "literal Token wins when TokenEnv is empty/unset",
			target: kegpkg.Target{
				Url:      "https://hub.example.com",
				TokenEnv: "HUB_TOKEN",
				Token:    "literal-value",
			},
			resolver: &stubResolver{token: "resolver-ignored"},
			want:     want{token: "literal-value", resolverCalled: false},
		},
		{
			name: "resolver fills in when env and literal are both empty",
			target: kegpkg.Target{
				Url: "https://hub.example.com",
			},
			resolver: &stubResolver{token: "resolver-value"},
			want:     want{token: "resolver-value", resolverCalled: true},
		},
		{
			name: "nil resolver is safe; no credentials means empty token",
			target: kegpkg.Target{
				Url: "https://hub.example.com",
			},
			resolver: nil,
			want:     want{token: ""},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := NewSandbox(t)
			for k, v := range tc.env {
				require.NoError(t, f.Runtime().Set(k, v))
			}

			var opts []kegpkg.KegOption
			if tc.resolver != nil {
				opts = append(opts, kegpkg.WithTokenResolver(tc.resolver))
			}

			k, err := kegpkg.NewKegFromTarget(f.Context(), tc.target, f.Runtime(), opts...)
			require.NoError(t, err)
			require.NotNil(t, k)

			remote, ok := k.(*kegpkg.RemoteKeg)
			require.True(t, ok, "expected *RemoteKeg, got %T", k)
			require.Equal(t, tc.want.token, remote.Token())
			if tc.resolver != nil {
				if tc.want.resolverCalled {
					require.Equal(t, 1, tc.resolver.called, "resolver should have been consulted")
				} else {
					require.Equal(t, 0, tc.resolver.called, "resolver must not be consulted when earlier fallbacks hit")
				}
			}
		})
	}
}
