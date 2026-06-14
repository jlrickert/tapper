package tapper_test

import (
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestHubAdd_WritesUserConfig(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)

	require.NoError(t, tap.HubAdd(fx.Context(), tapper.HubAddOptions{
		Name:     "work",
		URL:      "https://work.example",
		TokenEnv: "WORK_TOKEN",
	}))

	saved := string(fx.MustReadFile(tap.PathService.UserConfig()))
	require.Contains(t, saved, "work")
	require.Contains(t, saved, "https://work.example")
	require.Contains(t, saved, "WORK_TOKEN")
}

func TestHubRemove_DeletesFromUserConfig(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)

	require.NoError(t, tap.HubAdd(fx.Context(), tapper.HubAddOptions{Name: "work", URL: "https://work.example"}))
	require.NoError(t, tap.HubRemove(fx.Context(), tapper.HubRemoveOptions{Name: "work"}))

	saved := string(fx.MustReadFile(tap.PathService.UserConfig()))
	require.NotContains(t, saved, "https://work.example")

	// Removing a hub that isn't configured is an error.
	err = tap.HubRemove(fx.Context(), tapper.HubRemoveOptions{Name: "work"})
	require.Error(t, err)
}

func TestHubSetDefault_ProjectByDefault(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Runtime().Mkdir("/home/testuser/project/.tapper", 0o755, true))
	require.NoError(t, fx.Setwd("/home/testuser/project"))
	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser/project", Runtime: fx.Runtime()})
	require.NoError(t, err)

	// atlas is a built-in remote hub, so it validates without explicit config.
	require.NoError(t, tap.HubSetDefault(fx.Context(), tapper.HubSetDefaultOptions{Name: "atlas"}))

	proj := string(fx.MustReadFile("/home/testuser/project/.tapper/config.yaml"))
	require.Contains(t, proj, "atlas")
	require.True(t, strings.Contains(proj, "defaultHub"))

	// The default write targets the project, not the user config — which the
	// sandbox starts without, so it must still be absent.
	userResolved, rerr := fx.Runtime().ResolvePath(tap.PathService.UserConfig(), false)
	require.NoError(t, rerr)
	_, readErr := fx.Runtime().ReadFile(userResolved)
	require.Error(t, readErr)
}

func TestHubSetDefault_UserFlag(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)

	require.NoError(t, tap.HubSetDefault(fx.Context(), tapper.HubSetDefaultOptions{Name: "atlas", User: true}))

	saved := string(fx.MustReadFile(tap.PathService.UserConfig()))
	require.Contains(t, saved, "defaultHub")
	require.Contains(t, saved, "atlas")
}
