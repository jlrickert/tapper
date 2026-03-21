package tapper_test

import (
	"context"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// setupDoctorKeg creates a keg with the given config mutation applied and
// returns a Tap instance and context ready for doctor tests.
func setupDoctorKeg(t *testing.T, mutate func(*keg.Config)) (*tapper.Tap, context.Context) {
	t.Helper()
	fx := NewSandbox(t)
	ctx := fx.Context()

	root := "/home/testuser/work"
	require.NoError(t, fx.Runtime().Mkdir(root, 0o755, true))
	require.NoError(t, fx.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    root,
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	userCfg := `fallbackKeg: test
kegs: {}
defaultRegistry: ""
kegSearchPaths:
  - /home/testuser/kegs
`
	require.NoError(t, fx.Runtime().Mkdir(tap.PathService.ConfigRoot, 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(userCfg), 0o644))

	kegDir := "/home/testuser/kegs/test"
	require.NoError(t, fx.Runtime().Mkdir(kegDir, 0o755, true))

	k, err := keg.NewKegFromTarget(ctx, kegurl.NewFile(kegDir), fx.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(ctx))

	// Create a node without an entity attribute in meta.
	_, err = k.Create(ctx, &keg.CreateOptions{
		Body: []byte("# Test Node\n\nSome content.\n"),
	})
	require.NoError(t, err)

	if mutate != nil {
		require.NoError(t, k.UpdateConfig(ctx, mutate))
	}

	return tap, ctx
}

func TestDoctor_EntityCheckDisabledByDefault(t *testing.T) {
	t.Parallel()
	tap, ctx := setupDoctorKeg(t, nil)

	issues, err := tap.Doctor(ctx, tapper.DoctorOptions{})
	require.NoError(t, err)

	for _, issue := range issues {
		require.NotEqual(t, "entity-missing", issue.Kind, "entity-missing issues should not appear when entity check is disabled")
		require.NotEqual(t, "entity-attr", issue.Kind, "entity-attr issues should not appear when entity check is disabled")
	}
}

func TestDoctor_EntityCheckEnabledReportsMissingEntity(t *testing.T) {
	t.Parallel()
	tap, ctx := setupDoctorKeg(t, func(cfg *keg.Config) {
		cfg.Doctor = &keg.DoctorConfig{EntityCheck: true}
	})

	issues, err := tap.Doctor(ctx, tapper.DoctorOptions{})
	require.NoError(t, err)

	var entityAttrIssues []tapper.Issue
	for _, issue := range issues {
		if issue.Kind == "entity-attr" {
			entityAttrIssues = append(entityAttrIssues, issue)
		}
	}
	require.NotEmpty(t, entityAttrIssues, "should report missing entity attributes when entity check is enabled")
	require.Equal(t, "warning", entityAttrIssues[0].Level)
	require.Contains(t, entityAttrIssues[0].Message, "missing entity attribute")
}

func TestDoctor_EntityCheckExplicitlyDisabled(t *testing.T) {
	t.Parallel()
	tap, ctx := setupDoctorKeg(t, func(cfg *keg.Config) {
		cfg.Doctor = &keg.DoctorConfig{EntityCheck: false}
	})

	issues, err := tap.Doctor(ctx, tapper.DoctorOptions{})
	require.NoError(t, err)

	for _, issue := range issues {
		require.NotEqual(t, "entity-missing", issue.Kind, "entity-missing issues should not appear when entity check is explicitly disabled")
		require.NotEqual(t, "entity-attr", issue.Kind, "entity-attr issues should not appear when entity check is explicitly disabled")
	}
}
