package cli_test

import (
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestTap_ProjectResolutionFlags(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))
	sb.Setwd("~")

	initCmd := NewProcess(t, false,
		"repo", "init",
		".",
		"--project",
		"--cwd",
		"--keg", "project",
		"--creator", "test-user",
	)
	initRes := initCmd.Run(sb.Context(), sb.Runtime())
	require.NoError(t, initRes.Err, "project init should succeed")
	_ = sb.MustReadFile("~/docs/keg")

	createCmd := NewProcess(t, false,
		"create",
		"--project",
		"--cwd",
		"--title", "Project Local Note",
	)
	createRes := createCmd.Run(sb.Context(), sb.Runtime())
	require.NoError(t, createRes.Err, "create with --project should succeed")
	require.Contains(t, string(createRes.Stdout), "1", "expected node id output")

	catCmd := NewProcess(t, false,
		"cat", "1",
		"--project",
		"--cwd",
	)
	catRes := catCmd.Run(sb.Context(), sb.Runtime())
	require.NoError(t, catRes.Err, "cat with --project should resolve local project keg")
	require.Contains(t, string(catRes.Stdout), "title: Project Local Note")
}

func TestKegV2_UsesProjectKegOnly(t *testing.T) {
	t.Run("errors_when_project_keg_missing", func(innerT *testing.T) {
		innerT.Parallel()
		sb := NewSandbox(innerT, testutils.WithFixture("testuser", "~"))
		sb.Setwd("~")

		h := NewKegV2Process(innerT, false, "cat", "0")
		res := h.Run(sb.Context(), sb.Runtime())

		require.Error(innerT, res.Err)
		require.Contains(innerT, string(res.Stderr), "project keg not found")
	})

	t.Run("resolves_local_project_keg", func(innerT *testing.T) {
		innerT.Parallel()
		sb := NewSandbox(innerT, testutils.WithFixture("testuser", "~"))
		sb.Setwd("~")

		initCmd := NewProcess(innerT, false,
			"repo", "init",
			".",
			"--project",
			"--cwd",
			"--keg", "project",
			"--creator", "test-user",
		)
		initRes := initCmd.Run(sb.Context(), sb.Runtime())
		require.NoError(innerT, initRes.Err, "project init should succeed")
		_ = sb.MustReadFile("~/docs/keg")

		h := NewKegV2Process(innerT, false, "cat", "0")
		res := h.Run(sb.Context(), sb.Runtime())

		require.NoError(innerT, res.Err)
		require.Contains(innerT, string(res.Stdout), "title:")
	})

	t.Run("does_not_expose_keg_alias_flag", func(innerT *testing.T) {
		innerT.Parallel()
		sb := NewSandbox(innerT, testutils.WithFixture("testuser", "~"))

		h := NewKegV2Process(innerT, false, "cat", "0", "--keg", "example")
		res := h.Run(sb.Context(), sb.Runtime())

		require.Error(innerT, res.Err)
		require.Contains(innerT, string(res.Stderr), "unknown flag: --keg")
	})
}
