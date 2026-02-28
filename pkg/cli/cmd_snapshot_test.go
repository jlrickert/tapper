package cli_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestKegV2SnapshotHistoryAndRestore(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t,
		testutils.WithFixture("joe", "~"),
		testutils.WithWd("~/kegs/personal"),
	)

	res := NewKegV2Process(t, false, "snapshot", "1", "-m", "before change").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "1\n", string(res.Stdout))

	sb.MustWriteFile("~/kegs/personal/1/README.md", []byte("# Personal Overview\n\nUpdated snapshot body.\n\n- [Project Alpha](../2)\n- [Meeting Notes](../3)\n"), 0o644)

	res = NewKegV2Process(t, false, "reindex").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	res = NewKegV2Process(t, false, "snapshot", "1", "-m", "after change").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "2\n", string(res.Stdout))

	res = NewKegV2Process(t, false, "snapshot", "history", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	stdout := string(res.Stdout)
	require.Contains(t, stdout, "before change")
	require.Contains(t, stdout, "after change")

	res = NewKegV2Process(t, false, "snapshot", "restore", "1", "1", "--yes").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	res = NewKegV2Process(t, false, "cat", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "An index of personal notes and projects.")
	require.NotContains(t, string(res.Stdout), "Updated snapshot body.")

	res = NewKegV2Process(t, false, "snapshot", "history", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "restore from rev 1")
}

func TestKegV2ArchiveImportOverwritesExistingNodes(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t,
		testutils.WithFixture("joe", "~"),
		testutils.WithWd("~/kegs/personal"),
	)

	res := NewKegV2Process(t, false, "snapshot", "1", "-m", "before export").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	exportPath := "~/export.keg.tar.gz"
	res = NewKegV2Process(t, false, "archive", "export", "--nodes", "1,2,3", "--with-history", "-o", exportPath).Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "export.keg.tar.gz")

	targetRepo := keg.NewFsRepo("~/import-target", sb.Runtime())
	targetKeg := keg.NewKeg(targetRepo, sb.Runtime())
	require.NoError(t, targetKeg.Init(sb.Context()))
	id, err := targetKeg.Create(sb.Context(), &keg.CreateOptions{Title: "Existing node"})
	require.NoError(t, err)
	require.Equal(t, keg.NodeId{ID: 1}, id)
	_, err = targetKeg.AppendSnapshot(sb.Context(), id, "old target")
	require.NoError(t, err)
	require.NoError(t, targetRepo.WriteFile(sb.Context(), id, "keep.txt", []byte("keep me")))
	require.NoError(t, sb.Runtime().Setwd("~/import-target"))

	res = NewKegV2Process(t, false, "archive", "import", exportPath).Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	lines := strings.Fields(string(res.Stdout))
	require.Equal(t, []string{"1", "2", "3"}, lines)

	res = NewKegV2Process(t, false, "cat", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	stdout := string(res.Stdout)
	require.Contains(t, stdout, "Personal Overview")
	require.NotContains(t, stdout, "Existing node")

	hasNode4, err := targetRepo.HasNode(sb.Context(), keg.NodeId{ID: 4})
	require.NoError(t, err)
	require.False(t, hasNode4)

	res = NewKegV2Process(t, false, "snapshot", "history", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "before export")
	require.NotContains(t, string(res.Stdout), "old target")

	asset, err := targetRepo.ReadFile(sb.Context(), id, "keep.txt")
	require.NoError(t, err)
	require.Equal(t, "keep me", string(asset))
}

func TestTapSnapshotArchiveCommandsWithAliasAndPath(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t,
		testutils.WithFixture("joe", "~"),
		testutils.WithWd("~/kegs/personal"),
	)

	res := NewProcess(t, false, "snapshot", "1", "--keg", "personal", "-m", "tap snapshot").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "1\n", string(res.Stdout))

	res = NewProcess(t, false, "snapshot", "history", "1", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "tap snapshot")

	exportPath := "~/tap-export.keg.tar.gz"
	res = NewProcess(t, false, "archive", "export", "--keg", "personal", "--nodes", "1", "--with-history", "-o", exportPath).Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "tap-export.keg.tar.gz")

	targetRepo := keg.NewFsRepo("~/tap-import-target", sb.Runtime())
	targetKeg := keg.NewKeg(targetRepo, sb.Runtime())
	require.NoError(t, targetKeg.Init(sb.Context()))

	res = NewProcess(t, false, "archive", "import", exportPath, "--path", "~/tap-import-target").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "1\n", string(res.Stdout))

	res = NewProcess(t, false, "cat", "1", "--path", "~/tap-import-target").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "Personal Overview")

	res = NewProcess(t, false, "snapshot", "history", "1", "--path", "~/tap-import-target").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "tap snapshot")
}

func TestRootCompletionSuggestsSnapshotArchiveCommands(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)

	comp := NewCompletionProcess(t, false, 0, "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "snapshot")
	require.Contains(t, suggestions, "archive")
	require.NotContains(t, suggestions, "node")
	require.NotContains(t, suggestions, "import")
	require.NotContains(t, suggestions, "export")
}

func TestArchiveCommand_SuggestsImportAndExport(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)

	comp := NewCompletionProcess(t, false, 0, "archive", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "import")
	require.Contains(t, suggestions, "export")
}

func TestArchiveImportCommand_CompletionUsesFileDirective(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)

	comp := NewCompletionProcess(t, false, 0, "archive", "import", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)
	require.Contains(t, string(comp.Stdout), ":0")
}

func TestArchiveImportCommand_MissingArchiveShowsResolvedPath(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "archive", "import", "~/Downloads/does-not-exist.keg.tar.gz", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "archive not found:")
	require.Contains(t, string(res.Stderr), "/home/testuser/Downloads/does-not-exist.keg.tar.gz")
}

func TestArchiveImportCommand_AcceptsPlainTarArchive(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t,
		testutils.WithFixture("joe", "~"),
		testutils.WithWd("~/kegs/personal"),
	)

	exportPath := "~/plain-export.keg.tar.gz"
	res := NewProcess(t, false, "archive", "export", "--keg", "personal", "--nodes", "1", "-o", exportPath).Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	gzData := sb.MustReadFile(exportPath)
	gzr, err := gzip.NewReader(bytes.NewReader(gzData))
	require.NoError(t, err)
	tarData, err := io.ReadAll(gzr)
	require.NoError(t, err)
	require.NoError(t, gzr.Close())

	plainTarPath := "~/plain-export-tar.keg.tar.gz"
	sb.MustWriteFile(plainTarPath, tarData, 0o644)

	targetRepo := keg.NewFsRepo("~/plain-import-target", sb.Runtime())
	targetKeg := keg.NewKeg(targetRepo, sb.Runtime())
	require.NoError(t, targetKeg.Init(sb.Context()))

	res = NewProcess(t, false, "archive", "import", plainTarPath, "--path", "~/plain-import-target").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "1\n", string(res.Stdout))
}
