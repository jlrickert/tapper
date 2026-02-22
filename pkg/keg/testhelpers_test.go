package keg_test

import (
	"embed"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
)

//go:embed data/**
var testdata embed.FS

func NewSandbox(t *testing.T, opts ...sandbox.Option) *sandbox.Sandbox {
	return sandbox.NewSandbox(t, &sandbox.Options{
		Data: testdata,
		Home: "/home/testuser",
		User: "testuser",
	}, opts...)
}
