package tapper_test

import (
	"path/filepath"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestNewTap_UsesRuntimeWorkingDirectoryWhenRootEmpty(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("example", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	wd, err := fx.Runtime().Getwd()
	require.NoError(t, err)
	require.Equal(t, wd, tap.Root)
}

func TestNewTap_InitializesServicesAndPaths(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("example", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	root := "/home/testuser/work"
	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    root,
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NotNil(t, tap.PathService)
	require.NotNil(t, tap.ConfigService)
	require.NotNil(t, tap.KegService)
	require.Equal(t, tap.Runtime, tap.ConfigService.Runtime)
	require.Equal(t, tap.Runtime, tap.KegService.Runtime)
	require.Equal(t, filepath.Join(tap.PathService.ConfigRoot, "config.yaml"), tap.PathService.UserConfig())
	require.Equal(t, filepath.Join(tap.PathService.LocalConfigRoot, "config.yaml"), tap.PathService.ProjectConfig())
}
