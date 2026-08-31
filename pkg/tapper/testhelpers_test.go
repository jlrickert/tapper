package tapper_test

import (
	"context"
	"embed"
	"path/filepath"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

//go:embed all:data/**
var testdata embed.FS

func NewSandbox(t *testing.T, opts ...sandbox.Option) *sandbox.Sandbox {
	t.Helper()
	return sandbox.NewSandbox(t,
		&sandbox.Options{
			Data: testdata,
			Home: filepath.FromSlash("/home/testuser"),
			User: "testuser",
		}, opts...)
}

func makeKegNonStrict(t *testing.T, ctx context.Context, k keg.Keg) {
	t.Helper()
	require.NoError(t, keg.UpdateSettings(ctx, k, func(cfg *keg.Settings) {
		if cfg.SchemaPolicy == nil {
			cfg.SchemaPolicy = &keg.SchemaPolicy{}
		}
		cfg.SchemaPolicy.Strict = false
	}))
}
