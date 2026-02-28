package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestKegV2NodeSnapshotHistoryAndRestore(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t,
		testutils.WithFixture("joe", "~"),
		testutils.WithWd("~/kegs/personal"),
	)

	res := NewKegV2Process(t, false, "node", "snapshot", "1", "-m", "before change").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "1\n", string(res.Stdout))

	sb.MustWriteFile("~/kegs/personal/1/README.md", []byte("# Personal Overview\n\nUpdated snapshot body.\n\n- [Project Alpha](../2)\n- [Meeting Notes](../3)\n"), 0o644)

	res = NewKegV2Process(t, false, "reindex").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	res = NewKegV2Process(t, false, "node", "snapshot", "1", "-m", "after change").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "2\n", string(res.Stdout))

	res = NewKegV2Process(t, false, "node", "history", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	stdout := string(res.Stdout)
	require.Contains(t, stdout, "before change")
	require.Contains(t, stdout, "after change")

	res = NewKegV2Process(t, false, "node", "restore", "1", "1", "--yes").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	res = NewKegV2Process(t, false, "cat", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "An index of personal notes and projects.")
	require.NotContains(t, string(res.Stdout), "Updated snapshot body.")

	res = NewKegV2Process(t, false, "node", "history", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "restore from rev 1")
}

func TestKegV2ExportImportRoundTrip(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t,
		testutils.WithFixture("joe", "~"),
		testutils.WithWd("~/kegs/personal"),
	)

	res := NewKegV2Process(t, false, "node", "snapshot", "1", "-m", "before export").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	exportPath := "~/export.keg.tar.gz"
	res = NewKegV2Process(t, false, "export", "--nodes", "1,2,3", "--with-history", "-o", exportPath).Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "export.keg.tar.gz")

	targetRepo := keg.NewFsRepo("~/import-target", sb.Runtime())
	targetKeg := keg.NewKeg(targetRepo, sb.Runtime())
	require.NoError(t, targetKeg.Init(sb.Context()))
	_, err := targetKeg.Create(sb.Context(), &keg.CreateOptions{Title: "Existing node"})
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().Setwd("~/import-target"))

	res = NewKegV2Process(t, false, "import", exportPath).Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	lines := strings.Fields(string(res.Stdout))
	require.Equal(t, []string{"2", "3", "4"}, lines)

	res = NewKegV2Process(t, false, "cat", "2").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	stdout := string(res.Stdout)
	require.Contains(t, stdout, "../3")
	require.Contains(t, stdout, "../4")
	require.NotContains(t, stdout, "../1")

	res = NewKegV2Process(t, false, "node", "history", "2").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "before export")
}
