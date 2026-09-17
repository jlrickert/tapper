package cli_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestUse_PositionalFlightShorthand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		arg  string
		want string
	}{
		{name: "local slug", arg: "+backend", want: "+backend"},
		{name: "qualified", arg: "@local/+backend", want: "@local/+backend"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			sb := NewSandbox(t)
			project := "/home/testuser/project"
			require.NoError(t, sb.Runtime().Mkdir(project, 0o755, true))
			require.NoError(t, sb.Setwd(project))

			res := NewProcess(t, false, "use", tt.arg).Run(sb.Context(), sb.Runtime())
			require.NoError(t, res.Err)

			cfg, err := tapper.ReadConfig(sb.Runtime(), filepath.Join(project, ".tapper", "config.yaml"))
			require.NoError(t, err)
			require.Equal(t, tt.want, cfg.Flight())
			require.Empty(t, cfg.Keg())
		})
	}
}

func TestUse_BarePositionalStillSetsKeg(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)
	project := "/home/testuser/project"
	require.NoError(t, sb.Runtime().Mkdir(project, 0o755, true))
	require.NoError(t, sb.Setwd(project))

	res := NewProcess(t, false, "use", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	cfg, err := tapper.ReadConfig(sb.Runtime(), filepath.Join(project, ".tapper", "config.yaml"))
	require.NoError(t, err)
	require.Equal(t, "personal", cfg.Keg())
	require.Empty(t, cfg.Flight())
	raw, err := sb.Runtime().ReadFile(filepath.Join(project, ".tapper", "config.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(raw), "updated:")
}

func TestUseCompletion_SuggestsKegsAndFlights(t *testing.T) {
	t.Parallel()
	sb := NewRemoteKegListSandbox(t, remoteCompletionKegs())

	comp := NewCompletionProcess(t, false, 0, "use", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "@team/personal")
	require.NotContains(t, suggestions, "personal")
	require.Contains(t, suggestions, "@team/+backend")
	require.Contains(t, string(comp.Stdout), fmt.Sprintf(":%d", cobra.ShellCompDirectiveNoFileComp))
}

func TestUseCompletion_FiltersFlightsByPrefix(t *testing.T) {
	t.Parallel()
	sb := NewRemoteKegListSandbox(t, remoteCompletionKegs())

	comp := NewCompletionProcess(t, false, 0, "use", "@team/+back").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	require.Equal(t, []string{"@team/+backend"}, parseCompletionSuggestions(string(comp.Stdout)))
}

func TestUseCompletion_StopsAfterOneArg(t *testing.T) {
	t.Parallel()
	sb := NewRemoteKegListSandbox(t, remoteCompletionKegs())

	comp := NewCompletionProcess(t, false, 0, "use", "personal", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	require.Empty(t, parseCompletionSuggestions(string(comp.Stdout)))
	require.Contains(t, string(comp.Stdout), fmt.Sprintf(":%d", cobra.ShellCompDirectiveNoFileComp))
}

func TestUse_RemovesLegacyConfigTimestamp(t *testing.T) {
	for _, user := range []bool{false, true} {
		t.Run(fmt.Sprint(user), func(t *testing.T) {
			sb := NewSandbox(t)
			project := "/home/testuser/project"
			require.NoError(t, sb.Runtime().Mkdir(project, 0755, true))
			require.NoError(t, sb.Setwd(project))
			path := filepath.Join(project, ".tapper", "config.yaml")
			args := []string{"use", "personal"}
			if user {
				path = "/home/testuser/.config/tapper/config.yaml"
				args = append(args, "--user")
			}
			require.NoError(t, sb.Runtime().AtomicWriteFile(path, []byte("updated: not-a-timestamp\ncustom: retained\n"), 0644))
			res := NewProcess(t, false, args...).Run(sb.Context(), sb.Runtime())
			require.NoError(t, res.Err)
			raw, err := sb.Runtime().ReadFile(path)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "updated:")
			require.Contains(t, string(raw), "custom: retained")
		})
	}
}

func TestConfigExplain_DirectoryDefaultsAndFlags(t *testing.T) {
	for _, field := range []string{"keg", "hub", "flight"} {
		t.Run(field, func(t *testing.T) {
			sb := NewSandbox(t)
			require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte("kegMap:\n- {pathPrefix: /, "+field+": mapped}\n"), 0644))
			mapped := NewProcess(t, false, "config", "--explain", field).Run(sb.Context(), sb.Runtime())
			require.NoError(t, mapped.Err)
			require.Contains(t, string(mapped.Stdout), field+" = mapped")
			require.Contains(t, string(mapped.Stdout), "source: kegMap (startup directory)")
			explicit := NewProcess(t, false, "--"+field, "explicit", "config", "--explain", field).Run(sb.Context(), sb.Runtime())
			require.NoError(t, explicit.Err)
			require.Contains(t, string(explicit.Stdout), field+" = explicit")
			require.Contains(t, string(explicit.Stdout), "source: flag")
		})
	}
}
