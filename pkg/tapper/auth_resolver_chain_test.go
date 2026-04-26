package tapper_test

import (
	"errors"
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// TestResolveLoginHubURL exercises every branch of the five-step
// resolution chain documented in keg-dev/1035 §Decision. Each case
// configures a Config + explicit input shape and asserts the resolved
// URL or the expected error class. Branches that overlap (e.g. an
// explicit URL with both DefaultHub and Hubs configured) verify
// precedence: earlier steps unconditionally win over later ones.
func TestResolveLoginHubURL(t *testing.T) {
	t.Parallel()

	type tc struct {
		name     string
		yaml     string
		explicit string
		want     string
		wantErr  error
		errMatch string
	}

	cases := []tc{
		{
			name:     "step 1: explicit URL wins over everything",
			yaml:     "defaultHub: knut\ndisableDefaultHub: true\nhubs:\n  - name: knut\n    url: keg.example.com\n",
			explicit: "https://override.example.com",
			want:     "https://override.example.com",
		},
		{
			name:     "step 1: explicit URL is canonicalized",
			yaml:     "",
			explicit: "HTTPS://Hub.Example.COM/",
			want:     "https://hub.example.com",
		},
		{
			name: "step 2: DefaultHub names a Hubs entry",
			yaml: "defaultHub: primary\nhubs:\n  - name: other\n    url: other.example.com\n  - name: primary\n    url: keg.example.com\n",
			want: "https://keg.example.com",
		},
		{
			name: "step 2: DefaultHub entry already has scheme",
			yaml: "defaultHub: knut\nhubs:\n  - name: knut\n    url: http://localhost:8080\n",
			want: "http://localhost:8080",
		},
		{
			name:     "step 2: DefaultHub names missing entry → error",
			yaml:     "defaultHub: missing\nhubs:\n  - name: knut\n    url: keg.example.com\n",
			errMatch: `default hub "missing" not found`,
		},
		{
			name:     "step 2: named entry with empty URL → error",
			yaml:     "defaultHub: empty\nhubs:\n  - name: empty\n",
			errMatch: `default hub "empty" has no URL configured`,
		},
		{
			name: "step 3: exactly one Hubs entry, no DefaultHub",
			yaml: "hubs:\n  - name: solo\n    url: solo.example.com\n",
			want: "https://solo.example.com",
		},
		{
			name:    "step 4: DisableDefaultHub blocks fallback when nothing else matches",
			yaml:    "disableDefaultHub: true\n",
			wantErr: tapper.ErrDefaultHubDisabled,
		},
		{
			name:    "step 4: DisableDefaultHub fires even with multiple Hubs entries",
			yaml:    "disableDefaultHub: true\nhubs:\n  - name: a\n    url: a.example.com\n  - name: b\n    url: b.example.com\n",
			wantErr: tapper.ErrDefaultHubDisabled,
		},
		{
			name: "step 5: empty config falls back to DefaultHubURL",
			yaml: "",
			want: tapper.DefaultHubURL,
		},
		{
			name: "step 5: multiple Hubs without DefaultHub falls through to DefaultHubURL",
			yaml: "hubs:\n  - name: a\n    url: a.example.com\n  - name: b\n    url: b.example.com\n",
			want: tapper.DefaultHubURL,
		},
		{
			name: "nil config still resolves to DefaultHubURL",
			yaml: "", // sentinel: passes nil
			want: tapper.DefaultHubURL,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var cfg *tapper.Config
			if tc.yaml != "" || tc.name != "nil config still resolves to DefaultHubURL" {
				parsed, err := tapper.ParseConfig([]byte(tc.yaml))
				require.NoError(t, err, "fixture YAML should parse")
				cfg = parsed
			}

			got, err := tapper.ResolveLoginHubURL(cfg, tc.explicit)
			switch {
			case tc.wantErr != nil:
				require.Error(t, err)
				require.True(t, errors.Is(err, tc.wantErr),
					"want sentinel %v, got %v", tc.wantErr, err)
			case tc.errMatch != "":
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.errMatch)
			default:
				require.NoError(t, err)
				require.Equal(t, tc.want, got)
			}
		})
	}
}

// TestResolveLoginHubURL_StepOrdering pins the step ordering invariant:
// adding a Hubs entry to a config that has DisableDefaultHub set must not
// alter the result for an explicit URL (step 1 always wins). This guards
// against future refactors that re-order the chain.
func TestResolveLoginHubURL_StepOrdering(t *testing.T) {
	t.Parallel()

	cfg, err := tapper.ParseConfig([]byte(
		"defaultHub: a\ndisableDefaultHub: true\nhubs:\n  - name: a\n    url: a.example.com\n  - name: b\n    url: b.example.com\n",
	))
	require.NoError(t, err)

	got, err := tapper.ResolveLoginHubURL(cfg, "https://explicit.example.com")
	require.NoError(t, err, "explicit URL must short-circuit before disable check")
	require.Equal(t, "https://explicit.example.com", got)
}
