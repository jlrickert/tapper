package tapper

import (
	"fmt"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestRetiredMappingAndNamespaceData(t *testing.T) {
	for _, retired := range []string{"old", "[malformed, retired]", "{hub: foreign}", "null", "42"} {
		for _, supported := range []string{"keg: mapped", "hub: mapped", "flight: +mapped", "keg: mapped, hub: mapped, flight: +mapped"} {
			t.Run(retired+"/"+supported, func(t *testing.T) {
				sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
				rt := sb.Runtime()
				s, err := NewConfigService("/workspace/nested", rt)
				require.NoError(t, err)
				raw := fmt.Sprintf(`keg: user
hub: user
flight: +user
namespaces: %s # retired routing
hubs:
  user: {url: https://user.example}
  mapped: {url: https://mapped.example}
kegMap:
- {pathRegex: '^/workspace', alias: %s} # retired regex
- {pathPrefix: /workspace/nested, alias: %s} # retired prefix
- {pathPrefix: /workspace, alias: %s, %s} # supported defaults
`, retired, retired, retired, retired, supported)
				cfg, err := ParseConfig([]byte(raw))
				require.NoError(t, err)
				require.Empty(t, ValidateConfig(cfg))
				mapping, ok := cfg.LookupMapping(rt, "/workspace/nested")
				require.True(t, ok)
				require.Equal(t, "/workspace", mapping.PathPrefix)
				require.NoError(t, cfg.SetKeg("changed"))
				out, err := cfg.ToYAML()
				require.NoError(t, err)
				var before, after map[string]any
				require.NoError(t, yaml.Unmarshal([]byte(raw), &before))
				require.NoError(t, yaml.Unmarshal(out, &after))
				require.Equal(t, before["namespaces"], after["namespaces"])
				require.Equal(t, before["kegMap"], after["kegMap"])
				for _, comment := range []string{"# retired routing", "# retired regex", "# retired prefix", "# supported defaults"} {
					require.Contains(t, string(out), comment)
				}
				require.NoError(t, rt.AtomicWriteFile(s.PathService.UserConfig(), out, 0644))
				resolved, err := s.Config()
				require.NoError(t, err)
				wantKeg, wantHub, wantFlight := "changed", "user", "+user"
				if mapping.Keg != "" {
					wantKeg = mapping.Keg
				}
				if mapping.Hub != "" {
					wantHub = mapping.Hub
				}
				if mapping.Flight != "" {
					wantFlight = mapping.Flight
				}
				require.Equal(t, wantKeg, resolved.Keg())
				require.Equal(t, wantHub, resolved.HubName())
				require.Equal(t, wantFlight, resolved.Flight())
				target, err := s.ResolveTarget("@team/notes", "", "")
				require.NoError(t, err)
				require.Equal(t, wantHub, target.Hub)
				require.Equal(t, "team", target.Namespace)
			})
		}
	}
}

func TestMappingDuplicatesIncludeAllDefaults(t *testing.T) {
	cfg, err := ParseConfig([]byte(`kegMap:
- {pathPrefix: /work, keg: notes, hub: first, flight: +one}
- {pathPrefix: /work, keg: notes, hub: second, flight: +one}
- {pathPrefix: /work, keg: notes, hub: first, flight: +two}
- {pathPrefix: /work, hub: first}
- {pathPrefix: /work, hub: second}
- {pathPrefix: /work, flight: +one}
- {pathPrefix: /work, flight: +two}
`))
	require.NoError(t, err)
	require.Empty(t, ValidateConfig(cfg))
	original := cfg.KegMap()
	require.NoError(t, cfg.AddKegMap(KegMapEntry{PathPrefix: "/work", Flight: "+three"}))
	require.Len(t, cfg.KegMap(), len(original)+1)
	require.NoError(t, cfg.AddKegMap(original[0]))
	require.Len(t, cfg.KegMap(), len(original)+1)
	cfg.data.KegMap = append(cfg.data.KegMap, original[0])
	warnings := ValidateConfig(cfg)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0].Message, `keg="notes" hub="first" flight="+one"`)
	require.NotContains(t, warnings[0].Message, "alias")
}
