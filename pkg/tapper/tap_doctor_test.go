package tapper_test

import (
	"context"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func setupDoctorKeg(t *testing.T) (*tapper.Tap, *keg.LocalKeg, context.Context) {
	t.Helper()
	fx := NewSandbox(t)
	ctx := fx.Context()

	root := "/home/testuser/work"
	require.NoError(t, fx.Runtime().Mkdir(root, 0o755, true))
	require.NoError(t, fx.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{Root: root, Runtime: fx.Runtime()})
	require.NoError(t, err)
	userCfg := `fallbackKeg: test
fallbackNamespace: local
hubs:
  home:
    kind: local
    basePath: /home/testuser/kegs
`
	require.NoError(t, fx.Runtime().Mkdir(tap.PathService.ConfigRoot, 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(userCfg), 0o644))

	kegDir := "/home/testuser/kegs/@local/test"
	require.NoError(t, fx.Runtime().Mkdir(kegDir, 0o755, true))
	k, err := keg.NewKegFromTarget(ctx, keg.NewFile(kegDir), fx.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(ctx))
	makeKegNonStrict(t, ctx, k)
	local, ok := k.(*keg.LocalKeg)
	require.True(t, ok)
	return tap, local, ctx
}

func TestDoctorRetainsContentLinkMetadataStatsAndSchemaChecks(t *testing.T) {
	tap, k, ctx := setupDoctorKeg(t)

	id, err := k.Create(ctx, &keg.CreateOptions{
		Title: "Needs attention",
		Body:  []byte("# Needs attention\n\n[missing](../99)\n"),
	})
	require.NoError(t, err)
	require.NoError(t, k.Repo.WriteMeta(ctx, id.ID, []byte("tags: [\n")))
	require.NoError(t, k.WriteSchema(ctx, "task", []byte(`type: task
meta:
  type: object
  required: [type]
  properties:
    type:
      const: task
`)))

	issues, err := tap.Doctor(ctx, tapper.DoctorOptions{})
	require.NoError(t, err)

	kinds := map[string]bool{}
	for _, issue := range issues {
		kinds[issue.Kind] = true
		require.NotEqual(t, "entity-missing", issue.Kind)
		require.NotEqual(t, "entity-attr", issue.Kind)
		require.NotEqual(t, "tag-missing", issue.Kind)
	}
	require.True(t, kinds["broken-link"], "doctor should retain broken-link checks: %#v", issues)
	require.True(t, kinds["meta"], "doctor should retain metadata parsing checks: %#v", issues)
	require.True(t, kinds["schema"], "doctor should retain schema checks: %#v", issues)
}

func TestDoctorReportsContentAndStatsProblems(t *testing.T) {
	tap, k, ctx := setupDoctorKeg(t)
	id, err := k.Create(ctx, &keg.CreateOptions{Title: "Temporary", Body: []byte("# Temporary\n")})
	require.NoError(t, err)
	require.NoError(t, k.Repo.WriteContent(ctx, id.ID, nil))
	require.NoError(t, k.Repo.WriteStats(ctx, id.ID, &keg.NodeStats{}))

	issues, err := tap.Doctor(ctx, tapper.DoctorOptions{})
	require.NoError(t, err)
	kinds := map[string]bool{}
	for _, issue := range issues {
		kinds[issue.Kind] = true
	}
	require.True(t, kinds["content"])
	require.True(t, kinds["timestamp"])
}
