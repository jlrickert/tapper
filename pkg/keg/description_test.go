package keg_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestDescriptionLegacyPresenceAndCanonicalRoundTrips(t *testing.T) {
	long := strings.Repeat("界🙂 long text\n", 1000)
	for _, version := range []string{keg.SettingsV1VersionString, keg.SettingsV2VersionString} {
		for _, tc := range []struct {
			name        string
			description *string
			want        string
		}{
			{"legacy", nil, long}, {"clear", new(string), ""}, {"canonical", &long, long},
		} {
			t.Run(version+tc.name, func(t *testing.T) {
				fields := map[string]any{"kegv": version, "summary": long, "title": "Stored title"}
				if tc.description != nil {
					fields["description"] = *tc.description
				}
				raw, err := yaml.Marshal(fields)
				require.NoError(t, err)
				cfg, err := keg.ParseKegSettings(raw)
				require.NoError(t, err)
				require.Equal(t, tc.want, cfg.Description)
				for _, encode := range []func() ([]byte, error){cfg.ToJSON, cfg.ToYAML} {
					canonical, err := encode()
					require.NoError(t, err)
					var fields map[string]any
					require.NoError(t, yaml.Unmarshal(canonical, &fields))
					require.NotContains(t, fields, "summary")
					require.Equal(t, tc.want, fields["description"])
					back, err := keg.ParseKegSettings(canonical)
					require.NoError(t, err)
					require.Equal(t, tc.want, back.Description)
				}
				data, err := json.Marshal(fields)
				require.NoError(t, err)
				var direct keg.Settings
				require.NoError(t, json.Unmarshal(data, &direct))
				require.Equal(t, tc.want, direct.Description)
			})
		}
	}
}
