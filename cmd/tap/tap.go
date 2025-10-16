package main

import (
	"context"
	"os"

	std "github.com/jlrickert/go-std/pkg"
	"github.com/jlrickert/tapper/pkg/app"
	"github.com/jlrickert/tapper/pkg/cli"
)

func main() {
	ctx := context.Background()

	interactive := std.IsInteractiveTerminal(os.Stdin)

	err := cli.Run(ctx, app.Streams{
		Interactive: interactive,
		In:          os.Stdin,
		Out:         os.Stdout,
		Err:         os.Stderr,
	}, os.Args[1:])
	if err != nil {
		os.Exit(1)
	}
}
