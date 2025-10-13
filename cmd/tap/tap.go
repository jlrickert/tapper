package main

import (
	"context"
	"os"

	"github.com/jlrickert/tapper/pkg/app"
	"github.com/jlrickert/tapper/pkg/cli"
)

func main() {
	ctx := context.Background()

	err := cli.Run(ctx, app.Streams{
		In:  os.Stdin,
		Out: os.Stdout,
		Err: os.Stderr,
	}, os.Args[1:])
	if err != nil {
		os.Exit(1)
	}
}
