package main

import (
	"context"
	"os"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/cli"
)

func main() {
	ctx := context.Background()
	//ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	//defer stop()

	rt, err := toolkit.NewRuntime()
	if err != nil {
		os.Exit(1)
	}

	_, err = cli.Run(ctx, rt, os.Args[1:])
	if err != nil {
		os.Exit(1)
	}
}
