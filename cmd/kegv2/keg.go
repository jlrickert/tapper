package main

import (
	"context"
	"os"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/cli"
)

func main() {
	ctx := context.Background()
	//ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	//defer cancel()

	rt, err := toolkit.NewRuntime()
	if err != nil {
		os.Exit(1)
	}

	if exitCode, err := cli.RunWithProfile(
		ctx,
		rt,
		os.Args[1:],
		cli.KegV2Profile(),
	); err != nil {
		os.Exit(exitCode)
	}
	os.Exit(0)
}
