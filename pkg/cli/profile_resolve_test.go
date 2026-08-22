package cli_test

import (
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestTap_ProjectResolutionFlags(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))
	sb.Setwd("~")

	initCmd := NewProcess(t, false,
		"init",
		"--project",
		"--cwd",
		"--keg", "project",
		"--creator", "test-user",
	)
	initRes := initCmd.Run(sb.Context(), sb.Runtime())
	require.NoError(t, initRes.Err, "project init should succeed")
	_ = sb.MustReadFile("~/kegs/project/keg")
	projectKeg := keg.NewLocalKeg(keg.NewFsRepo("~/kegs/project", sb.Runtime()), sb.Runtime())
	DisableStrictSchemaPolicy(t, sb.Context(), projectKeg)

	createCmd := NewProcess(t, false,
		"create",
		"--keg", "~/kegs/project",
		"--title", "Project Local Note",
	)
	createRes := createCmd.Run(sb.Context(), sb.Runtime())
	require.NoError(t, createRes.Err, "create against the local keg path should succeed")
	require.Contains(t, string(createRes.Stdout), "1", "expected node id output")

	catCmd := NewProcess(t, false,
		"cat", "1",
		"--keg", "~/kegs/project",
	)
	catRes := catCmd.Run(sb.Context(), sb.Runtime())
	require.NoError(t, catRes.Err, "cat with --keg <path> should resolve the local keg")
	require.Contains(t, string(catRes.Stdout), "# Project Local Note")
	require.NotContains(t, string(catRes.Stdout), "access_count:")
}

// TestTap_ResolvesProjectKegUnderKegsDir verifies that a project-local keg
// initialized under <project>/kegs/<name>/ is resolvable via project-target
// resolution (--cwd). Under the namespace-centric model a bare --keg <name>
// routes to the local hub (fallbackNamespace: local) rather than the project
// tree, so project kegs are reached through the project-target flags.
func TestTap_ResolvesProjectKegUnderKegsDir(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))
	sb.Setwd("~/myproject")

	initCmd := NewProcess(t, false,
		"init",
		"--project",
		"--cwd",
		"--keg", "tapper",
		"--creator", "test-user",
	)
	initRes := initCmd.Run(sb.Context(), sb.Runtime())
	require.NoError(t, initRes.Err, "project init should succeed")
	_ = sb.MustReadFile("~/myproject/kegs/tapper/keg")

	catCmd := NewProcess(t, false,
		"cat", "0",
		"--keg", "~/myproject/kegs/tapper",
	)
	catRes := catCmd.Run(sb.Context(), sb.Runtime())
	require.NoError(t, catRes.Err, "cat with --keg <path> should resolve the keg under kegs/")
	require.Contains(t, string(catRes.Stdout), "# Sorry, planned but not yet available")
}

func TestKeg_UsesProjectKegOnly(t *testing.T) {
	t.Run("errors_when_project_keg_missing", func(innerT *testing.T) {
		innerT.Parallel()
		sb := NewSandbox(innerT, testutils.WithFixture("testuser", "~"))
		sb.Setwd("~")

		h := NewKegProcess(innerT, false, "cat", "0")
		res := h.Run(sb.Context(), sb.Runtime())

		require.Error(innerT, res.Err)
		require.Contains(innerT, string(res.Stderr), "project keg not found")
	})

	t.Run("does_not_fallback_to_legacy_docs_keg", func(innerT *testing.T) {
		innerT.Parallel()
		sb := NewSandbox(innerT, testutils.WithFixture("testuser", "~"))
		sb.Setwd("~")

		legacyInit := NewProcess(innerT, false,
			"init",
			"--project",
			"--path", "~/docs",
			"--keg", "legacy",
			"--creator", "test-user",
		)
		legacyRes := legacyInit.Run(sb.Context(), sb.Runtime())
		require.NoError(innerT, legacyRes.Err, "legacy docs keg init should succeed")
		_ = sb.MustReadFile("~/docs/keg")

		h := NewKegProcess(innerT, false, "cat", "0")
		res := h.Run(sb.Context(), sb.Runtime())

		require.Error(innerT, res.Err)
		require.Contains(innerT, string(res.Stderr), "project keg not found")
	})

	t.Run("resolves_local_project_keg", func(innerT *testing.T) {
		innerT.Parallel()
		sb := NewSandbox(innerT, testutils.WithFixture("testuser", "~"))
		sb.Setwd("~")

		initCmd := NewProcess(innerT, false,
			"init",
			"--project",
			"--cwd",
			"--keg", "project",
			"--creator", "test-user",
		)
		initRes := initCmd.Run(sb.Context(), sb.Runtime())
		require.NoError(innerT, initRes.Err, "project init should succeed")
		_ = sb.MustReadFile("~/kegs/project/keg")

		h := NewKegProcess(innerT, false, "cat", "0")
		res := h.Run(sb.Context(), sb.Runtime())

		require.NoError(innerT, res.Err)
		require.Contains(innerT, string(res.Stdout), "# Sorry, planned but not yet available")
		require.NotContains(innerT, string(res.Stdout), "access_count:")
	})

	t.Run("does_not_expose_keg_alias_flag", func(innerT *testing.T) {
		innerT.Parallel()
		sb := NewSandbox(innerT, testutils.WithFixture("testuser", "~"))

		h := NewKegProcess(innerT, false, "cat", "0", "--keg", "example")
		res := h.Run(sb.Context(), sb.Runtime())

		require.Error(innerT, res.Err)
		require.Contains(innerT, string(res.Stderr), "unknown flag: --keg")
	})
}

func TestTap_CwdStandaloneResolution(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))
	sb.Setwd("~")

	initCmd := NewProcess(t, false,
		"init",
		"--cwd",
		"--keg", "project",
		"--creator", "test-user",
	)
	initRes := initCmd.Run(sb.Context(), sb.Runtime())
	require.NoError(t, initRes.Err, "init with --cwd should succeed")
	_ = sb.MustReadFile("~/kegs/project/keg")

	catCmd := NewProcess(t, false,
		"cat", "0",
		"--keg", "~/kegs/project",
	)
	catRes := catCmd.Run(sb.Context(), sb.Runtime())
	require.NoError(t, catRes.Err, "cat with --keg <path> should resolve the local keg")
	require.Contains(t, string(catRes.Stdout), "# Sorry, planned but not yet available")
}
