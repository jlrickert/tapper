package main

import (
	"context"
	"os"

	"github.com/jlrickert/tapper/pkg/cli"
)

func main() {
	ctx := context.Background()

	_, err := cli.Run(ctx, os.Args[1:])
	if err != nil {
		os.Exit(1)
	}
}
