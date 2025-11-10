package cli_test

import (
	"context"
	"embed"
	"testing"

	tu "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/cli"
)

// NOTE: Production code should call streams.IsStdoutTTY() (method) instead of
// performing raw terminal detection. Tests can override IsStdoutTTYFn to
// simulate TTY or non-TTY environments.
//
// testdata is an optional embedded data FS for fixtures. Previously an embed
// pattern attempted to include empty directories which caused an embed error.
//
//go:embed all:data/**
var testdata embed.FS

func NewSandbox(t *testing.T, opts ...tu.SandboxOption) *tu.Sandbox {
	return tu.NewSandbox(t, &tu.SandboxOptions{
		Data: testdata,
		Home: "/home/testuser",
		User: "testuser",
	}, opts...)
}

func NewCliRunner(t *testing.T) *tu.Process {
	return nil
}

func NewProcess(t *testing.T, isTTY bool, args ...string) *tu.Process {
	return tu.NewProcess(func(ctx context.Context, stream *toolkit.Stream) (int, error) {
		procCtx := toolkit.WithStream(ctx, stream)
		return cli.Run(procCtx, args)
	}, isTTY)
}

func NewCompletionProcess(t *testing.T, isTTY bool, pos int, words ...string) *tu.Process {
	return tu.NewProcess(func(ctx context.Context, stream *toolkit.Stream) (int, error) {
		// Build completion request arguments for cobra
		args := []string{"__complete"}
		args = append(args, words...)

		procCtx := toolkit.WithStream(ctx, stream)
		return cli.Run(procCtx, args)
	}, isTTY)
}
