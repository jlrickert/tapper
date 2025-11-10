package tap_test

import (
	"embed"
	"path/filepath"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
)

//go:embed all:data/**
var testdata embed.FS

func NewSandbox(t *testing.T, opts ...sandbox.SandboxOption) *sandbox.Sandbox {
	return sandbox.NewSandbox(t,
		&sandbox.SandboxOptions{
			Data: testdata,
			Home: filepath.FromSlash("/home/testuser"),
			User: "testuser",
		}, opts...)
}
