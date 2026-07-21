package cli

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

type recordingInvocationReporter struct {
	mu     sync.Mutex
	events []tapper.InvocationEvent
	closed bool
}

func (r *recordingInvocationReporter) Report(event tapper.InvocationEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *recordingInvocationReporter) Close(context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
}

func (r *recordingInvocationReporter) snapshot() ([]tapper.InvocationEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]tapper.InvocationEvent(nil), r.events...), r.closed
}

func TestRunReportsExactCobraCommandPathsOnSuccessAndFailure(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		success bool
		command string
	}{
		{name: "success", args: []string{"config", "--show-sources"}, success: true, command: "tap config"},
		{name: "failure", args: []string{"cat", "99999"}, success: false, command: "tap cat"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fx := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
			reporter := &recordingInvocationReporter{}
			ctx := WithTestDepsHook(fx.Context(), func(deps *Deps) {
				deps.InvocationReporter = reporter
			})
			_, err := Run(ctx, fx.Runtime(), tc.args)
			if tc.success {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			events, closed := reporter.snapshot()
			require.True(t, closed)
			require.Len(t, events, 1)
			require.Equal(t, "cli", events[0].Surface)
			require.Equal(t, tc.command, events[0].Command)
			require.Equal(t, tc.success, events[0].Success)
			require.NotNil(t, events[0].Interactive)
			require.NotContains(t, events[0].Command, "99999")
		})
	}
}

func TestReportCLIInvocationIncludesTerminatingTapMCP(t *testing.T) {
	fx := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	reporter := &recordingInvocationReporter{}
	deps := &Deps{
		Runtime:            fx.Runtime(),
		InvocationReporter: reporter,
		commandPath:        "tap mcp",
		startTime:          fx.Runtime().Clock().Now().Add(-time.Second),
	}

	reportCLIInvocation(deps, context.Canceled)
	events, _ := reporter.snapshot()
	require.Len(t, events, 1)
	require.Equal(t, "tap mcp", events[0].Command)
	require.False(t, events[0].Success)
	require.GreaterOrEqual(t, events[0].DurationMS, int64(0))
}
