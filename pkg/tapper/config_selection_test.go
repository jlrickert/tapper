package tapper

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestSelectionPrecedence(t *testing.T) {
	cases := []struct{ name, project, mapping, envKeg, envHub, flagKeg, flagHub, wantKeg, wantHub string }{
		{name: "user", wantKeg: "user", wantHub: "user"},
		{name: "project", project: "keg: project\nhub: project\n", wantKeg: "project", wantHub: "project"},
		{name: "project keg beats mapping", project: "keg: project\n", mapping: "{pathPrefix: /workspace, keg: mapped, hub: mapped}", wantKeg: "project", wantHub: "mapped"},
		{name: "project hub beats mapping", project: "keg: project\nhub: project\n", mapping: "{pathPrefix: /workspace, keg: mapped, hub: mapped}", wantKeg: "project", wantHub: "project"},
		{name: "mapping miss", mapping: "{pathPrefix: /elsewhere, keg: mapped, hub: mapped}", wantKeg: "user", wantHub: "user"},
		{name: "prefix requires boundary", mapping: "{pathPrefix: /work, keg: mapped, hub: mapped}", wantKeg: "user", wantHub: "user"},
		{name: "mapping inherits hub", mapping: "{pathPrefix: /workspace, keg: mapped}", wantKeg: "mapped", wantHub: "user"},
		{name: "environment", project: "keg: project\nhub: project\n", mapping: "{pathPrefix: /workspace, keg: mapped, hub: mapped}", envKeg: "env", envHub: "env", wantKeg: "env", wantHub: "env"},
		{name: "flags", mapping: "{pathPrefix: /workspace, keg: mapped, hub: mapped}", envKeg: "env", envHub: "env", flagKeg: "flag", flagHub: "flag", wantKeg: "flag", wantHub: "flag"},
		{name: "retired alias ignored", mapping: "{pathPrefix: /workspace, alias: old}", wantKeg: "user", wantHub: "user"},
		{name: "keg beats alias", mapping: "{pathPrefix: /workspace, alias: old, keg: new}", wantKeg: "new", wantHub: "user"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
			rt := sb.Runtime()
			s, err := NewConfigService("/workspace/nested", rt)
			require.NoError(t, err)
			raw := "keg: user\nhub: user\nfallbackNamespace: team\nhubs:\n"
			for _, h := range []string{"user", "project", "mapped", "env", "flag"} {
				raw += fmt.Sprintf("  %s: {url: https://%s.example}\n", h, h)
			}
			if tc.mapping != "" {
				raw += "kegMap:\n  - " + tc.mapping + "\n"
			}
			require.NoError(t, rt.AtomicWriteFile(s.PathService.UserConfig(), []byte(raw), 0644))
			require.NoError(t, rt.AtomicWriteFile("/workspace/.tapper/config.yaml", []byte(tc.project), 0644))
			rt.Env().Set("TAP_KEG", tc.envKeg)
			rt.Env().Set("TAP_HUB", tc.envHub)
			target, err := s.ResolveTarget(tc.flagKeg, "team", tc.flagHub)
			require.NoError(t, err)
			require.Equal(t, tc.wantKeg, target.KegName)
			require.Equal(t, tc.wantHub, target.Hub)
		})
	}
}

func TestMappingOrderAndInvalidSelection(t *testing.T) {
	cases := []struct {
		name, rules, want string
		fail              bool
	}{
		{name: "longest prefix", rules: "- {pathPrefix: /workspace, keg: broad}\n- {pathPrefix: /workspace/nested, keg: narrow}", want: "narrow"},
		{name: "prefix tie keeps first", rules: "- {pathPrefix: /workspace, keg: first}\n- {pathPrefix: /workspace, keg: second}", want: "first"},
		{name: "regex first", rules: "- {pathPrefix: /workspace/nested, keg: prefix}\n- {pathRegex: '^/workspace', keg: regex}\n- {pathRegex: '^/workspace', keg: later}", want: "regex"},
		{name: "invalid nonmatching regex", rules: "- {pathRegex: '[', keg: bad}\n- {pathPrefix: /workspace, keg: good}", want: "good"},
		{name: "invalid nonmatching selector", rules: "- {pathPrefix: /elsewhere, keg: [wrong]}\n- {pathPrefix: /workspace, keg: good}", want: "good"},
		{name: "invalid matching selector", rules: "- {pathPrefix: /workspace, keg: [wrong]}", fail: true},
		{name: "hub-only mapping", rules: "- {pathPrefix: /workspace, hub: atlas}", want: "fallback"},
		{name: "empty keg beats alias", rules: "- {pathPrefix: /workspace, keg: '', alias: valid}", fail: true},
		{name: "keg beats invalid alias", rules: "- {pathPrefix: /workspace, keg: good, alias: [wrong]}", want: "good"},
		{name: "invalid matching hub", rules: "- {pathPrefix: /workspace, keg: good, hub: [wrong]}", fail: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
			rt := sb.Runtime()
			s, err := NewConfigService("/workspace/nested", rt)
			require.NoError(t, err)
			raw := "keg: fallback\nfallbackNamespace: team\nkegMap:\n" + tc.rules + "\n"
			require.NoError(t, rt.AtomicWriteFile(s.PathService.UserConfig(), []byte(raw), 0644))
			target, err := s.ResolveTarget("", "team", "")
			if tc.fail {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, target.KegName)
		})
	}
}

func TestRetiredAndUnusedConfigSurvivesRewrite(t *testing.T) {
	raw := `# header
hub: active # selection
keg: notes
defaultKeg: [retired]
fallbackKeg: wrong
defaultHub: {retired: yes}
fallbackHub: wrong
namespaces: {team: foreign}
custom: {keep: true}
hubs:
  active:
    url: https://active.example # endpoint
    kind: [retired]
  broken: [not, a, hub]
kegMap:
  - pathPrefix: /elsewhere # rule
    keg: {not: a-string}
`
	cfg, err := ParseConfig([]byte(raw))
	require.NoError(t, err)
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	target, err := cfg.ResolveRef(sb.Runtime(), KegRef{Namespace: "team", Name: "notes"})
	require.NoError(t, err)
	require.Equal(t, "active", target.Hub)
	require.NoError(t, cfg.SetKeg("changed"))
	out, err := cfg.ToYAML()
	require.NoError(t, err)
	for _, fragment := range []string{"# header", "# selection", "# endpoint", "# rule", "defaultKeg: [retired]", "fallbackKeg: wrong", "defaultHub: {retired: yes}", "fallbackHub: wrong", "namespaces: {team: foreign}", "custom: {keep: true}", "broken: [not, a, hub]", "kind: [retired]", "not: a-string"} {
		require.Contains(t, string(out), fragment)
	}
	require.Contains(t, string(out), "keg: changed")
}

func TestRetiredEnvironmentIgnoredAndNestedProjects(t *testing.T) {
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	rt := sb.Runtime()
	s, err := NewConfigService("/workspace/nested", rt)
	require.NoError(t, err)
	require.NoError(t, rt.AtomicWriteFile(s.PathService.UserConfig(), []byte("keg: user\nfallbackNamespace: team\n"), 0644))
	require.NoError(t, rt.AtomicWriteFile("/workspace/.tapper/config.yaml", []byte("keg: parent\n"), 0644))
	require.NoError(t, rt.AtomicWriteFile("/workspace/nested/.tapper/config.yaml", []byte("keg: child\n"), 0644))
	for _, key := range []string{"TAP_DEFAULT_KEG", "TAP_FALLBACK_KEG", "TAP_DEFAULT_HUB", "TAP_FALLBACK_HUB"} {
		rt.Env().Set(key, "wrong")
	}
	target, err := s.ResolveTarget("", "team", "")
	require.NoError(t, err)
	require.Equal(t, "child", target.KegName)
	require.Equal(t, "atlas", target.Hub)
	cfg, err := s.Config()
	require.NoError(t, err)
	require.False(t, strings.Contains(cfg.Keg(), "wrong"))
}

func TestEmptyConfiguredCredentialFailsClosed(t *testing.T) {
	for _, field := range []string{"token", "tokenEnv"} {
		t.Run(field, func(t *testing.T) {
			sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
			s, err := NewConfigService("/workspace", sb.Runtime())
			require.NoError(t, err)
			raw := "hub: example\nhubs:\n  example:\n    url: https://hub.example.com\n    " + field + ": ''\n"
			require.NoError(t, sb.Runtime().AtomicWriteFile(s.PathService.UserConfig(), []byte(raw), 0644))
			_, _, err = s.SelectedHub("")
			require.ErrorContains(t, err, "must not be empty")
			_, err = s.ResolveTarget("@me/demo", "team", "")
			require.Error(t, err)
		})
	}
}

func TestDefaultHubDoesNotRequireEnvironmentCredential(t *testing.T) {
	for _, cfg := range []*Config{DefaultUserConfig("me"), {data: &configDTO{}}} {
		hub, ok := cfg.Hub(DefaultHubName)
		require.True(t, ok)
		require.Empty(t, hub.TokenEnv)
		require.Empty(t, hub.Token)
	}
}
