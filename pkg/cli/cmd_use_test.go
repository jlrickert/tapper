package cli_test

import (
	"fmt"
	"path/filepath"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
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
			require.Empty(t, cfg.DefaultKeg())
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
	require.Equal(t, "personal", cfg.DefaultKeg())
	require.Empty(t, cfg.Flight())
}

func TestUseCompletion_SuggestsKegsAndFlights(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))
	sb.MustWriteFile("/home/testuser/kegs/flights.d/backend.yaml", []byte("title: Backend\n"), 0o644)

	comp := NewCompletionProcess(t, false, 0, "use", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "@local/personal")
	require.Contains(t, suggestions, "personal")
	require.Contains(t, suggestions, "@local/+backend")
	require.Contains(t, string(comp.Stdout), fmt.Sprintf(":%d", cobra.ShellCompDirectiveNoFileComp))
}

func TestUseCompletion_FiltersFlightsByPrefix(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))
	sb.MustWriteFile("/home/testuser/kegs/flights.d/backend.yaml", []byte("title: Backend\n"), 0o644)

	comp := NewCompletionProcess(t, false, 0, "use", "@local/+").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	require.Equal(t, []string{"@local/+backend"}, parseCompletionSuggestions(string(comp.Stdout)))
}

func TestUseCompletion_StopsAfterOneArg(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))
	sb.MustWriteFile("/home/testuser/kegs/flights.d/backend.yaml", []byte("title: Backend\n"), 0o644)

	comp := NewCompletionProcess(t, false, 0, "use", "personal", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	require.Empty(t, parseCompletionSuggestions(string(comp.Stdout)))
	require.Contains(t, string(comp.Stdout), fmt.Sprintf(":%d", cobra.ShellCompDirectiveNoFileComp))
}
