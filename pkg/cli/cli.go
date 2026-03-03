package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

func Run(ctx context.Context, rt *toolkit.Runtime, args []string) (int, error) {
	return RunWithProfile(ctx, rt, args, TapProfile())
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
	cmd := NewRootCmd(deps)
	cmd.SetArgs(args)
	cmd.SetIn(streams.In)
	cmd.SetOut(streams.Out)
	cmd.SetErr(streams.Err)

	if err := cmd.ExecuteContext(ctx); err != nil {
		_, _ = fmt.Fprintf(streams.Err, "Error: %s\n", renderUserError(err, deps))

		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return 130, err
		}
		return 1, err
	}
	return 0, nil
}

func RunCompletion(ctx context.Context, rt *toolkit.Runtime, args []string) (int, error) {
	return Run(ctx, rt, append([]string{"__complete"}, args...))
}

func RunCompletionWithProfile(ctx context.Context, rt *toolkit.Runtime, args []string, profile Profile) (int, error) {
	return RunWithProfile(ctx, rt, append([]string{"__complete"}, args...), profile)
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
		token == "--path",
		token == "-c", token == "--config",
		token == "--log-file", token == "--log-level":
		return true
	case strings.HasPrefix(token, "--keg="),
		strings.HasPrefix(token, "--path="),
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
