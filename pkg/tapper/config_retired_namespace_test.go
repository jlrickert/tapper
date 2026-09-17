package tapper

import (
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestRetiredNamespaceSettingsHaveNoEffect(t *testing.T) {
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	rt := sb.Runtime()
	tap, err := NewTap(TapOptions{Root: "/workspace", Runtime: rt})
	require.NoError(t, err)
	require.NoError(t, rt.AtomicWriteFile(tap.PathService.UserConfig(), []byte(`hub: home
defaultNamespace: [ignored]
fallbackNamespace: {ignored: true}
hubs:
  home:
    url: https://example.invalid
    defaultNamespace: [also, ignored]
`), 0644))
	require.NoError(t, rt.AtomicWriteFile("/workspace/.tapper/config.yaml", []byte("defaultNamespace: {ignored: true}\nfallbackNamespace: [ignored]\n"), 0644))
	require.NoError(t, rt.Env().Set("TAP_DEFAULT_NAMESPACE", "ignored"))
	require.NoError(t, rt.Env().Set("TAP_FALLBACK_NAMESPACE", "ignored"))
	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Empty(t, ValidateConfig(cfg))
	_, err = tap.ConfigService.ResolveTarget("notes", "", "")
	require.ErrorContains(t, err, "namespace is required")
	for _, ref := range []string{"@acme/notes", "notes"} {
		ns := ""
		if ref == "notes" {
			ns = "acme"
		}
		target, err := tap.ConfigService.ResolveTarget(ref, ns, "")
		require.NoError(t, err)
		require.Equal(t, "acme", target.Namespace)
		require.Equal(t, "notes", target.KegName)
	}
	fields, err := tap.ConfigExplain(t.Context(), ConfigExplainOptions{})
	require.NoError(t, err)
	for _, field := range fields {
		require.NotContains(t, field.Field, "Namespace")
	}
}
