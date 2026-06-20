package main

import (
	"context"
	_ "embed"
	"os"

	"github.com/jlrickert/cli-toolkit/clock"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/cli"
)

//go:embed LICENSE
var licenseText string

func main() {
	cli.LicenseText = licenseText

	ctx := context.Background()
	// Signal handling is intentionally not registered at the entrypoint.
	// Long-lived commands (e.g., serve) install their own signal handlers
	// with narrower scope so that short-lived commands exit immediately
	// without intercepting SIGINT.

	rt, err := toolkit.NewRuntime(toolkit.WithProcessInfo(toolkit.NewProcessInfo(clock.OsClock{})))
	if err != nil {
		os.Exit(1)
	}

	if exitCode, err := cli.RunWithProfile(
		ctx,
		rt,
		os.Args[1:],
		cli.KegProfile(),
	); err != nil {
		os.Exit(exitCode)
	}
	os.Exit(0)
}
