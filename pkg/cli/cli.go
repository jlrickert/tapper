package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func Run(ctx context.Context, rt *toolkit.Runtime, args []string) (int, error) {
	return RunWithProfile(ctx, rt, args, TapProfile())
}

// testDepsHookKey types values stashed in the context by
// WithTestDepsHook. Using a package-private type prevents cross-package
// collision on context.Value keys. Production callers never set this;
// it exists only for test helpers in cmd_auth_test.go.
type testDepsHookKey struct{}

// WithTestDepsHook returns a context carrying a per-invocation hook
// that Run will apply to the freshly-constructed Deps. Parallel tests
// attach their own hook to their own ctx, so there is no shared state
// for the race detector to flag.
//
// Exported so tests in this package (and sibling test packages in
// pkg/cli_test) can drive the seam without reaching into unexported
// state. Production callers must not use it — wiring AuthLoginDeviceFn
// into Deps directly is the intended seam for non-test callers.
func WithTestDepsHook(ctx context.Context, hook func(*Deps)) context.Context {
	if hook == nil {
		return ctx
	}
	return context.WithValue(ctx, testDepsHookKey{}, hook)
}

// testDepsHookFromContext extracts a hook attached via WithTestDepsHook.
// Returns nil when the context carries no hook, which is the common
// production case.
func testDepsHookFromContext(ctx context.Context) func(*Deps) {
	if ctx == nil {
		return nil
	}
	v, _ := ctx.Value(testDepsHookKey{}).(func(*Deps))
	return v
}

func RunWithProfile(ctx context.Context, rt *toolkit.Runtime, args []string, profile Profile) (int, error) {
	if rt == nil {
		var err error
		rt, err = toolkit.NewRuntime()
		if err != nil {
			return 1, err
		}
	}
	if err := rt.Validate(); err != nil {
		return 1, err
	}

	// Make it so that cat is the default subcommand if no explicit subcommand is
	// given before the first numeric positional argument.
	if rewritten, ok := rewriteDefaultCatArgs(args); ok {
		return RunWithProfile(ctx, rt, rewritten, profile)
	}

	streams := rt.Stream()
	deps := &Deps{
		Root:     "",
		Runtime:  rt,
		Shutdown: func() {},
		Profile:  profile,
	}
	// testDepsHookFromContext gives tests a chance to mutate the
	// freshly-constructed Deps (e.g. swap AuthLoginDeviceFn) before
	// NewRootCmd applies lazy defaults. Production callers pass a ctx with
	// no hook, so this is a cheap type-assertion in the hot path.
	if hook := testDepsHookFromContext(ctx); hook != nil {
		hook(deps)
	}
	cmd := NewRootCmd(deps)
	cmd.SetArgs(args)
	cmd.SetIn(streams.In)
	cmd.SetOut(streams.Out)
	cmd.SetErr(streams.Err)

	execErr := cmd.ExecuteContext(ctx)

	// Emit CLI invocation log entry. This runs after ExecuteContext
	// regardless of success or failure, ensuring every invocation is
	// logged. We log before error rendering so the entry is captured
	// even when the command fails.
	logCLIInvocation(deps, args, execErr)
	reportCLIInvocation(deps, execErr)
	if deps.InvocationReporter != nil {
		flushCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		deps.InvocationReporter.Close(flushCtx)
		cancel()
	}

	// Sync and close the log file handle unconditionally. Sync ensures the
	// invocation entry written by logCLIInvocation is flushed to disk
	// before the process exits. PersistentPostRunE does not close the
	// handle (the comment there explains why), so we close here after
	// the invocation log entry has been written.
	if deps.logFileHandle != nil {
		// Sync if the underlying writer supports it (e.g., *os.File).
		if syncer, ok := deps.logFileHandle.(interface{ Sync() error }); ok {
			_ = syncer.Sync()
		}
		_ = deps.logFileHandle.Close()
		deps.logFileHandle = nil
	}

	if execErr != nil {
		_, _ = fmt.Fprintf(streams.Err, "Error: %s\n", renderUserError(execErr, deps))

		var inputErr *hookInputError
		if errors.As(execErr, &inputErr) {
			return 2, execErr
		}
		if errors.Is(execErr, context.Canceled) ||
			errors.Is(execErr, context.DeadlineExceeded) {
			return 130, execErr
		}
		return 1, execErr
	}
	return 0, nil
}

// logCLIInvocation emits a structured log entry for a CLI command invocation.
// It is a no-op when the runtime or start time is unavailable (e.g., when
// PersistentPreRunE failed before recording the start time).
func logCLIInvocation(deps *Deps, args []string, execErr error) {
	if deps.Runtime == nil || deps.startTime.IsZero() {
		return
	}
	rt := deps.Runtime
	duration := rt.Clock().Now().Sub(deps.startTime)
	success := execErr == nil

	truncatedArgs := truncateArgs(args)
	attrs := []slog.Attr{
		slog.String("surface", "cli"),
		slog.String("command", strings.Join(truncatedArgs, " ")),
		slog.Any("args", truncatedArgs),
		slog.Int64("duration_ms", duration.Milliseconds()),
		slog.Bool("success", success),
		slog.Bool("interactive", rt.Stream().IsTTY),
	}
	if deps.KegTargetOptions.Keg != "" {
		attrs = append(attrs, slog.String("keg", deps.KegTargetOptions.Keg))
	}
	if execErr != nil {
		attrs = append(attrs, slog.String("error", execErr.Error()))
	}
	// Completions fire frequently during tab-complete; log them at debug
	// level to avoid spamming the log with noise.
	level := slog.LevelInfo
	if slices.Contains(args, "__complete") {
		level = slog.LevelDebug
	}
	rt.Logger().LogAttrs(context.Background(), level, "invocation", attrs...)
}

func reportCLIInvocation(deps *Deps, execErr error) {
	if deps == nil || deps.Runtime == nil || deps.InvocationReporter == nil ||
		deps.startTime.IsZero() || strings.TrimSpace(deps.commandPath) == "" {
		return
	}
	interactive := deps.Runtime.Stream().IsTTY
	deps.InvocationReporter.Report(tapper.InvocationEvent{
		Surface:     "cli",
		Command:     deps.commandPath,
		DurationMS:  deps.Runtime.Clock().Now().Sub(deps.startTime).Milliseconds(),
		Success:     execErr == nil,
		Interactive: &interactive,
	})
}

// maxArgBytes is the maximum byte length for a single CLI argument in
// invocation log entries. Arguments longer than this are truncated with a
// trailing ellipsis. This prevents tap edit and tap create from dumping
// full file contents into the log.
const maxArgBytes = 512

// truncateArgs returns a copy of args with individual values truncated to
// maxArgBytes. Short arguments are returned as-is.
func truncateArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if len(a) > maxArgBytes {
			out[i] = a[:maxArgBytes] + "...(truncated)"
		} else {
			out[i] = a
		}
	}
	return out
}

func RunCompletion(ctx context.Context, rt *toolkit.Runtime, args []string) (int, error) {
	return Run(ctx, rt, append([]string{"__complete"}, args...))
}

func RunCompletionWithProfile(ctx context.Context, rt *toolkit.Runtime, args []string, profile Profile) (int, error) {
	return RunWithProfile(ctx, rt, append([]string{"__complete"}, args...), profile)
}

// mustRegisterFlagCompletion registers a shell completion function for a flag,
// panicking if the registration fails. Failures here indicate a programming
// error (e.g., the flag name does not match a registered flag) and should be
// caught immediately during development rather than silently ignored.
func mustRegisterFlagCompletion(cmd *cobra.Command, flagName string, f func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective)) {
	if err := cmd.RegisterFlagCompletionFunc(flagName, f); err != nil {
		panic(fmt.Sprintf("cli: failed to register completion for flag %q: %v", flagName, err))
	}
}

func rewriteDefaultCatArgs(args []string) ([]string, bool) {
	if len(args) == 0 {
		return nil, false
	}

	start := 0
	prefix := []string{}
	if args[0] == "__complete" {
		if len(args) == 1 {
			return nil, false
		}
		start = 1
		prefix = append(prefix, "__complete")
	}

	idx, ok := firstPositionalAfterRootFlags(args[start:])
	if !ok {
		return nil, false
	}
	actualIdx := start + idx
	if _, err := strconv.Atoi(args[actualIdx]); err != nil {
		return nil, false
	}

	rewritten := append([]string{}, prefix...)
	rewritten = append(rewritten, args[start:actualIdx]...)
	rewritten = append(rewritten, "cat")
	rewritten = append(rewritten, args[actualIdx:]...)
	return rewritten, true
}

func firstPositionalAfterRootFlags(args []string) (int, bool) {
	for i := 0; i < len(args); i++ {
		token := args[i]
		if token == "--" {
			if i+1 < len(args) {
				return i + 1, true
			}
			return 0, false
		}
		if token == "" {
			continue
		}
		if !strings.HasPrefix(token, "-") || token == "-" {
			return i, true
		}
		if rootFlagConsumesNext(token) && i+1 < len(args) {
			i++
		}
	}
	return 0, false
}

func rootFlagConsumesNext(token string) bool {
	switch {
	case token == "-k", token == "--keg",
		token == "--namespace", token == "--hub", token == "--flight",
		token == "-c", token == "--config",
		token == "--log-file", token == "--log-level":
		return true
	case strings.HasPrefix(token, "--keg="),
		strings.HasPrefix(token, "--namespace="),
		strings.HasPrefix(token, "--hub="),
		strings.HasPrefix(token, "--flight="),
		strings.HasPrefix(token, "--config="),
		strings.HasPrefix(token, "--log-file="),
		strings.HasPrefix(token, "--log-level="):
		return false
	case strings.HasPrefix(token, "-k") && len(token) > 2:
		return false
	case strings.HasPrefix(token, "-c") && len(token) > 2:
		return false
	default:
		return false
	}
}
