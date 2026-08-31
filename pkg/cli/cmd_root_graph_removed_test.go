package cli

import (
	"context"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/stretchr/testify/require"
)

func TestGraphCommandRemoved(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	profile := TapProfile()
	require.False(t, commandNames(t, sb.Runtime(), profile)["graph"])

	proc := sandbox.NewProcess(func(ctx context.Context, rt *toolkit.Runtime) (int, error) {
		return RunWithProfile(ctx, rt, []string{"graph"}, profile)
	}, false)
	res := proc.Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), `unknown command "graph"`)
}
