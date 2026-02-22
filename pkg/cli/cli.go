package cli

import (
	"context"
	"errors"
	"strconv"

	"github.com/jlrickert/cli-toolkit/clock"
	"github.com/jlrickert/cli-toolkit/mylog"
	"github.com/jlrickert/cli-toolkit/toolkit"
)

func Run(ctx context.Context, rt *toolkit.Runtime, args []string) (int, error) {
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

	ctx = mylog.WithLogger(ctx, rt.Logger())
	ctx = clock.WithClock(ctx, rt.Clock())
	ctx = toolkit.WithHasher(ctx, rt.Hasher())
	ctx = toolkit.WithStream(ctx, rt.Stream())

	// Make it so that cat is the default subcommand if no valid subcommand is given
	if len(args) >= 2 && args[0] == "__complete" {
		if _, err := strconv.Atoi(args[1]); err == nil {
			args = append([]string{"__complete", "cat"}, args[2:]...)
			return Run(ctx, rt, args)
		}
	} else if len(args) >= 1 {
		if _, err := strconv.Atoi(args[0]); err == nil {
			args = append([]string{"cat"}, args...)
			return Run(ctx, rt, args)
		}
	}

	streams := rt.Stream()
	deps := &Deps{
		Root:     "",
		Runtime:  rt,
		Shutdown: func() {},
	}
	cmd := NewRootCmd(deps)
	cmd.SetArgs(args)
	cmd.SetIn(streams.In)
	cmd.SetOut(streams.Out)
	cmd.SetErr(streams.Err)

	if err := cmd.ExecuteContext(ctx); err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			return 130, err
		}
		return 1, err
	}
	return 0, nil
}
