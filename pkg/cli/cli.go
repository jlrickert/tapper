package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jlrickert/tapper/pkg/app"
)

func Run(ctx context.Context, streams app.Streams, args []string) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd := NewRootCmd()
	cmd.SetArgs(args)
	cmd.SetIn(streams.In)
	cmd.SetOut(streams.Out)
	cmd.SetErr(streams.Err)

	if err := cmd.ExecuteContext(ctx); err != nil {
		if errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			os.Exit(130)
		}
		fmt.Fprintln(streams.Err, err)
		os.Exit(1)
	}
	return nil
}
