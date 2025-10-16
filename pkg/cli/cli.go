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

type interactiveKey int

var ctxInteractiveKey interactiveKey

func WithInteractive(ctx context.Context, interactive bool) context.Context {
	return context.WithValue(ctx, ctxInteractiveKey, interactive)
}

func Run(ctx context.Context, streams app.Streams, args []string) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	ctx = WithInteractive(ctx, streams.Interactive)
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
