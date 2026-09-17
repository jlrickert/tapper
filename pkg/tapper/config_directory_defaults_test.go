package tapper

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestDirectoryDefaultsCascadeAndProvenance(t *testing.T) {
	for _, field := range []string{"keg", "hub", "flight"} {
		for _, tier := range []string{"user", "mapping", "project", "env", "flag"} {
			t.Run(field+"/"+tier, func(t *testing.T) {
				sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
				rt := sb.Runtime()
				tap, err := NewTap(TapOptions{Runtime: rt, Root: "/home/testuser/repos/bitbucket/team/project"})
				require.NoError(t, err)
				user := fmt.Sprintf("%s: user\n", field)
				if tier != "user" {
					user += fmt.Sprintf("kegMap:\n- {pathPrefix: ~/repos/bitbucket, %s: mapped}\n", field)
				}
				require.NoError(t, rt.AtomicWriteFile(tap.PathService.UserConfig(), []byte(user), 0644))
				if tier == "project" || tier == "env" || tier == "flag" {
					require.NoError(t, rt.AtomicWriteFile("/home/testuser/repos/bitbucket/.tapper/config.yaml", []byte(field+": project\n"), 0644))
				}
				if tier == "env" || tier == "flag" {
					require.NoError(t, rt.Env().Set("TAP_"+strings.ToUpper(field), "environment"))
				}
				opts := KegTargetOptions{}
				if tier == "flag" {
					switch field {
					case "keg":
						tap.ConfigService.KegOverride = "explicit"
						opts.Keg = "explicit"
					case "hub":
						tap.ConfigService.HubOverride = "explicit"
						opts.Hub = "explicit"
					case "flight":
						tap.ConfigService.FlightOverride = "explicit"
						opts.Flight = "explicit"
					}
				}
				want := map[string]string{"user": "user", "mapping": "mapped", "project": "project", "env": "environment", "flag": "explicit"}[tier]
				explain, err := tap.ConfigExplain(t.Context(), ConfigExplainOptions{Field: field})
				require.NoError(t, err)
				require.Equal(t, want, explain[0].Value)
				require.Equal(t, map[string]string{"user": "user config", "mapping": "kegMap (startup directory)", "project": "project config", "env": "env vars", "flag": "flag"}[tier], explain[0].Source)

				raw, err := tap.UseStatus(t.Context(), opts)
				require.NoError(t, err)
				var status map[string]map[string]string
				require.NoError(t, yaml.Unmarshal([]byte(raw), &status))
				require.Equal(t, want, status[field]["value"])
				scope := tier
				if tier == "mapping" {
					scope = "kegMap"
				}
				require.Equal(t, scope, status[field]["scope"])
			})
		}
	}
}

func TestPartialDirectoryDefaults(t *testing.T) {
	for _, tc := range []struct{ name, rules, wantKeg, wantHub, wantFlight string }{
		{"flight only", "- {pathPrefix: '~/repos/bitbucket', flight: mapped}", "user", "user", "mapped"},
		{"hub only", "- {pathPrefix: '$REPOS/bitbucket', hub: mapped}", "user", "mapped", "user"},
		{"keg only", "- {pathPrefix: '~/repos/bitbucket', keg: mapped}", "mapped", "user", "user"},
		{"no inheritance", "- {pathPrefix: '~/repos', keg: broad, hub: broad, flight: broad}\n- {pathPrefix: '~/repos/bitbucket', flight: narrow}", "user", "user", "narrow"},
		{"regex priority", "- {pathPrefix: '~/repos/bitbucket', keg: narrow}\n- {pathRegex: '~/repos/bitbucket/', flight: regex}\n- {pathRegex: '~/repos', flight: later}", "user", "user", "regex"},
		{"tie", "- {pathPrefix: '~/repos/bitbucket', flight: first}\n- {pathPrefix: '~/repos/bitbucket', hub: later}", "user", "user", "first"},
		{"boundary", "- {pathPrefix: '~/repos/bit', flight: wrong}", "user", "user", "user"},
		{"empty flight does not clear", "- {pathPrefix: '~/repos/bitbucket', keg: mapped, flight: ''}", "mapped", "user", "user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
			rt := sb.Runtime()
			require.NoError(t, rt.Env().Set("REPOS", "/home/testuser/repos"))
			s, err := NewConfigService("/home/testuser/repos/bitbucket/team/project", rt)
			require.NoError(t, err)
			raw := "keg: user\nhub: user\nflight: user\nkegMap:\n" + tc.rules + "\n"
			require.NoError(t, rt.AtomicWriteFile(s.PathService.UserConfig(), []byte(raw), 0644))
			cfg, err := s.Config()
			require.NoError(t, err)
			require.Equal(t, tc.wantKeg, cfg.Keg())
			require.Equal(t, tc.wantHub, cfg.HubName())
			require.Equal(t, tc.wantFlight, cfg.Flight())
		})
	}
}

func TestDirectoryDefaultsExplicitConfig(t *testing.T) {
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	rt := sb.Runtime()
	s, err := NewConfigService("/workspace", rt)
	require.NoError(t, err)
	require.NoError(t, rt.AtomicWriteFile(s.PathService.UserConfig(), []byte("flight: ignored-user"), 0644))
	require.NoError(t, rt.AtomicWriteFile(s.PathService.ProjectConfig(), []byte("flight: ignored-project"), 0644))
	require.NoError(t, rt.AtomicWriteFile("/explicit.yaml", []byte("flight: baseline\nkegMap:\n- {pathPrefix: /workspace, flight: mapped}"), 0644))
	s.ConfigPath = "/explicit.yaml"
	cfg, err := s.Config()
	require.NoError(t, err)
	require.Equal(t, "mapped", cfg.Flight())
	require.NoError(t, rt.Env().Set("TAP_FLIGHT", "environment"))
	s.Reload()
	cfg, err = s.Config()
	require.NoError(t, err)
	require.Equal(t, "environment", cfg.Flight())
}

func TestMappingValidationAndLegacyTimestampSerialization(t *testing.T) {
	for _, value := range []string{"2026-01-01T00:00:00Z", "not-a-date", "[legacy, data]", "{anything: true}"} {
		cfg, err := ParseConfig([]byte("updated: " + value + "\n# keep this\ncustom: yes\nkegMap:\n- pathPrefix: ~/repos # directory\n  flight: '@work/+dev' # flight\n  extension: keep\n"))
		require.NoError(t, err)
		require.Empty(t, ValidateConfig(cfg))
		out, err := cfg.ToYAML()
		require.NoError(t, err)
		require.NotContains(t, string(out), "updated:")
		for _, fragment := range []string{"# keep this", "custom: yes", "# directory", "# flight", "extension: keep", "@work/+dev"} {
			require.Contains(t, string(out), fragment)
		}
		again, err := ParseConfig(out)
		require.NoError(t, err)
		require.Equal(t, "@work/+dev", again.KegMap()[0].Flight)
	}
	for _, entry := range []KegMapEntry{{PathPrefix: "/work", Flight: "+work"}, {PathPrefix: "/work", Hub: "work"}, {PathRegex: "work", Keg: "work", Hub: "work", Flight: "+work"}} {
		cfg := DefaultUserConfig("user")
		require.NoError(t, cfg.AddKegMap(entry))
		require.Empty(t, ValidateConfig(cfg))
	}
	cfg := DefaultUserConfig("user")
	require.Error(t, cfg.AddKegMap(KegMapEntry{Flight: "+work"}))
	require.Error(t, cfg.AddKegMap(KegMapEntry{PathPrefix: "/work"}))
}

func TestInvalidMappingDefaultsCanBeOverridden(t *testing.T) {
	for _, field := range []string{"keg", "hub", "flight"} {
		for _, override := range []string{"", "project", "env", "flag"} {
			t.Run(field+"/"+override, func(t *testing.T) {
				sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
				rt := sb.Runtime()
				tap, err := NewTap(TapOptions{Runtime: rt, Root: "/workspace"})
				require.NoError(t, err)
				raw := "keg: valid\nflight: +valid\nfallbackNamespace: team\nkegMap:\n- {pathPrefix: /workspace, " + field + ": [invalid]}\n"
				require.NoError(t, rt.AtomicWriteFile(tap.PathService.UserConfig(), []byte(raw), 0644))
				good := map[string]string{"keg": "valid", "hub": "atlas", "flight": "+valid"}[field]
				if override == "project" {
					require.NoError(t, rt.AtomicWriteFile(tap.PathService.ProjectConfig(), []byte(field+": "+good), 0644))
				}
				if override == "env" {
					require.NoError(t, rt.Env().Set("TAP_"+strings.ToUpper(field), good))
				}
				var explicit string
				if override == "flag" {
					switch field {
					case "keg":
						tap.ConfigService.KegOverride = good
					case "hub":
						tap.ConfigService.HubOverride = good
					case "flight":
						explicit = good
					}
				}
				if field == "flight" {
					_, err = ParseFlightRef(tap.ActiveFlightName(explicit), "team")
				} else {
					_, err = tap.ConfigService.ResolveTarget("", "team", "")
				}
				if override == "" {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
}
