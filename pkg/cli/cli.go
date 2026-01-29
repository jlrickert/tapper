package cli

import (
	"context"
	"errors"
	"strconv"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

func Run(ctx context.Context, args []string) (int, error) {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Make it so that cat is the default subcommand if no valid subcommand is given
	if len(args) >= 2 && args[0] == "__complete" {
		if _, err := strconv.Atoi(args[1]); err == nil {
			args = append([]string{"__complete", "cat"}, args[2:]...)
			return Run(ctx, args)
		}
	} else if len(args) >= 1 {
		if _, err := strconv.Atoi(args[0]); err == nil {
			args = append([]string{"cat"}, args...)
			return Run(ctx, args)
		}
	}
	streams := toolkit.StreamFromContext(ctx)
	cmd := NewRootCmd()
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
