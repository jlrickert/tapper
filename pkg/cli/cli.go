package cli

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

func Run(ctx context.Context, args []string) (int, error) {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
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
