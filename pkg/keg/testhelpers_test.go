package keg_test

import (
	"embed"
	"github.com/jlrickert/go-std/sandbox"
	"testing"
)

//go:embed data/**
var testdata embed.FS

func NewSandbox(t *testing.T, opts ...sandbox.SandboxOption) *sandbox.Sandbox {
	return sandbox.NewSandbox(t, &sandbox.SandboxOptions{
		Data: testdata,
		Home: "/home/testuser",
		User: "testuser",
	}, opts...)
}
