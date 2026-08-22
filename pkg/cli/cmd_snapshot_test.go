package cli_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
	"time"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestKegSnapshotHistoryAndRestore(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t,
		testutils.WithFixture("joe", "~"),
		testutils.WithWd("~/kegs/@local/personal"),
	)

	res := NewKegProcess(t, false, "snapshot", "create", "1", "-m", "before change").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "1\n", string(res.Stdout))

	sb.MustWriteFile("~/kegs/@local/personal/1/README.md", []byte("# Personal Overview\n\nUpdated snapshot body.\n\n- [Project Alpha](../2)\n- [Meeting Notes](../3)\n"), 0o644)

	res = NewKegProcess(t, false, "index", "rebuild").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	res = NewKegProcess(t, false, "snapshot", "create", "1", "-m", "after change").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "2\n", string(res.Stdout))

	res = NewKegProcess(t, false, "snapshot", "history", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	stdout := string(res.Stdout)
	require.Contains(t, stdout, "before change")
	require.Contains(t, stdout, "after change")

	res = NewKegProcess(t, false, "snapshot", "view", "1", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "An index of personal notes and projects.")
	require.NotContains(t, string(res.Stdout), "Updated snapshot body.")

	res = NewKegProcess(t, false, "snapshot", "restore", "1", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	res = NewKegProcess(t, false, "cat", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "An index of personal notes and projects.")
	require.NotContains(t, string(res.Stdout), "Updated snapshot body.")

	res = NewKegProcess(t, false, "snapshot", "history", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "restore from rev 1")
}

func TestKegArchiveImportOverwritesExistingNodes(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t,
		testutils.WithFixture("joe", "~"),
		testutils.WithWd("~/kegs/@local/personal"),
	)

	res := NewKegProcess(t, false, "snapshot", "create", "1", "-m", "before export").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	exportPath := "~/export.keg.tar.gz"
	res = NewKegProcess(t, false, "archive", "export", "--nodes", "1,2,3", "-o", exportPath).Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "export.keg.tar.gz")

	targetRepo := keg.NewFsRepo("~/import-target", sb.Runtime())
	targetKeg := keg.NewLocalKeg(targetRepo, sb.Runtime())
	require.NoError(t, targetKeg.Init(sb.Context()))
	DisableStrictSchemaPolicy(t, sb.Context(), targetKeg)
	id, err := targetKeg.Create(sb.Context(), &keg.CreateOptions{Title: "Existing node"})
	require.NoError(t, err)
	require.Equal(t, keg.NodeId{ID: 1}, id.ID)
	_, err = targetKeg.AppendSnapshot(sb.Context(), id.ID, "old target")
	require.NoError(t, err)
	require.NoError(t, targetRepo.WriteFile(sb.Context(), id.ID, "keep.txt", []byte("keep me")))
	require.NoError(t, sb.Runtime().Setwd("~/import-target"))

	res = NewKegProcess(t, false, "archive", "import", exportPath).Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	lines := strings.Fields(string(res.Stdout))
	require.Equal(t, []string{"1", "2", "3"}, lines)

	res = NewKegProcess(t, false, "cat", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	stdout := string(res.Stdout)
	require.Contains(t, stdout, "Personal Overview")
	require.NotContains(t, stdout, "Existing node")

	hasNode4, err := targetRepo.HasNode(sb.Context(), keg.NodeId{ID: 4})
	require.NoError(t, err)
	require.False(t, hasNode4)

	res = NewKegProcess(t, false, "snapshot", "history", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "before export")
	require.NotContains(t, string(res.Stdout), "old target")

	asset, err := targetRepo.ReadFile(sb.Context(), id.ID, "keep.txt")
	require.NoError(t, err)
	require.Equal(t, "keep me", string(asset))
}

func TestTapSnapshotArchiveCommandsWithAliasAndPath(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t,
		testutils.WithFixture("joe", "~"),
		testutils.WithWd("~/kegs/@local/personal"),
	)

	res := NewProcess(t, false, "snapshot", "create", "1", "--keg", "personal", "-m", "tap snapshot").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "1\n", string(res.Stdout))

	res = NewProcess(t, false, "snapshot", "history", "1", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "tap snapshot")

	exportPath := "~/tap-export.keg.tar.gz"
	res = NewProcess(t, false, "archive", "export", "--keg", "personal", "--nodes", "1", "-o", exportPath).Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "tap-export.keg.tar.gz")

	targetRepo := keg.NewFsRepo("~/tap-import-target", sb.Runtime())
	targetKeg := keg.NewLocalKeg(targetRepo, sb.Runtime())
	require.NoError(t, targetKeg.Init(sb.Context()))
	DisableStrictSchemaPolicy(t, sb.Context(), targetKeg)

	res = NewProcess(t, false, "archive", "import", exportPath, "--keg", "~/tap-import-target").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "1\n", string(res.Stdout))

	res = NewProcess(t, false, "cat", "1", "--keg", "~/tap-import-target").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "Personal Overview")

	res = NewProcess(t, false, "snapshot", "history", "1", "--keg", "~/tap-import-target").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "tap snapshot")
}

func TestArchiveImportPreservesSnapshotTimestamps(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t,
		testutils.WithFixture("joe", "~"),
		testutils.WithWd("~/kegs/@local/personal"),
	)

	sourceRepo := keg.NewFsRepo("~/kegs/@local/personal", sb.Runtime())
	nodeID := keg.NodeId{ID: 1}

	res := NewProcess(t, false, "snapshot", "create", "1", "--keg", "personal", "-m", "baseline").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	sourceHistory, err := sourceRepo.ListSnapshots(sb.Context(), nodeID)
	require.NoError(t, err)
	require.Len(t, sourceHistory, 1)

	sb.Advance(45 * time.Minute)
	sb.MustWriteFile("~/kegs/@local/personal/1/README.md", []byte("# Personal Overview\n\nTimestamp preservation update.\n\n- [Project Alpha](../2)\n"), 0o644)

	res = NewProcess(t, false, "index", "rebuild", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	res = NewProcess(t, false, "snapshot", "create", "1", "--keg", "personal", "-m", "updated").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	sourceHistory, err = sourceRepo.ListSnapshots(sb.Context(), nodeID)
	require.NoError(t, err)
	require.Len(t, sourceHistory, 2)

	exportPath := "~/timestamp-history.keg.tar.gz"
	res = NewProcess(t, false, "archive", "export", "--keg", "personal", "--nodes", "1", "-o", exportPath).Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	sb.Advance(4 * time.Hour)

	targetRepo := keg.NewFsRepo("~/timestamp-import-target", sb.Runtime())
	targetKeg := keg.NewLocalKeg(targetRepo, sb.Runtime())
	require.NoError(t, targetKeg.Init(sb.Context()))
	DisableStrictSchemaPolicy(t, sb.Context(), targetKeg)

	res = NewProcess(t, false, "archive", "import", exportPath, "--keg", "~/timestamp-import-target").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	importedHistory, err := targetRepo.ListSnapshots(sb.Context(), nodeID)
	require.NoError(t, err)
	require.Len(t, importedHistory, len(sourceHistory))

	for i := range sourceHistory {
		require.True(t, importedHistory[i].CreatedAt.Equal(sourceHistory[i].CreatedAt))
		require.Equal(t, sourceHistory[i].Message, importedHistory[i].Message)
	}
	require.False(t, importedHistory[0].CreatedAt.Equal(sb.Now()))
}

func TestArchiveCommandsRoundTripSchemas(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)
	ctx := sb.Context()
	rt := sb.Runtime()

	sourceRepo := keg.NewFsRepo("~/schema-archive-source", rt)
	sourceKeg := keg.NewLocalKeg(sourceRepo, rt)
	require.NoError(t, sourceKeg.Init(ctx))
	sourceSchema := `type: task
summary: Archived tasks
meta:
  type: object
  required: ["type"]
  properties:
    type:
      const: task
markdown:
  requireTitle: true
`
	require.NoError(t, sourceKeg.WriteSchema(ctx, "task", []byte(sourceSchema)))
	require.NoError(t, sourceKeg.SetContent(ctx, keg.NodeId{ID: 0}, []byte("---\ntype: task\n---\n# Zero\n")))
	_, err := sourceKeg.Create(ctx, &keg.CreateOptions{
		Schema: "task",
		Body:   []byte("---\ntype: task\n---\n# CLI Imported Task\n"),
	})
	require.NoError(t, err)

	targetRepo := keg.NewFsRepo("~/schema-archive-target", rt)
	targetKeg := keg.NewLocalKeg(targetRepo, rt)
	require.NoError(t, targetKeg.Init(ctx))
	require.NoError(t, targetKeg.WriteSchema(ctx, "task", []byte("type: task\nsummary: Target tasks\n")))
	require.NoError(t, targetKeg.WriteSchema(ctx, "decision", []byte("type: decision\nsummary: Target-only decisions\n")))

	exportPath := "~/schema-roundtrip.keg.tar.gz"
	res := NewProcess(t, false, "archive", "export", "--keg", "~/schema-archive-source", "-o", exportPath).Run(ctx, rt)
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "schema-roundtrip.keg.tar.gz")

	res = NewProcess(t, false, "archive", "import", exportPath, "--keg", "~/schema-archive-target").Run(ctx, rt)
	require.NoError(t, res.Err)

	res = NewProcess(t, false, "schema", "get", "--keg", "~/schema-archive-target", "task").Run(ctx, rt)
	require.NoError(t, res.Err)
	require.Equal(t, sourceSchema, string(res.Stdout))

	res = NewProcess(t, false, "schema", "get", "--keg", "~/schema-archive-target", "decision").Run(ctx, rt)
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "Target-only decisions")
}

func TestRootCompletionSuggestsSnapshotArchiveCommands(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)

	comp := NewCompletionProcess(t, false, 0, "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "snapshot")
	require.Contains(t, suggestions, "archive")
	require.Contains(t, suggestions, "import")
	require.NotContains(t, suggestions, "node")
	require.NotContains(t, suggestions, "export")
}

func TestSnapshotCommand_SuggestsCreateHistoryAndRestore(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)

	comp := NewCompletionProcess(t, false, 0, "snapshot", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "create")
	require.Contains(t, suggestions, "history")
	require.Contains(t, suggestions, "view")
	require.Contains(t, suggestions, "restore")
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
		testutils.WithWd("~/kegs/@local/personal"),
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
	targetKeg := keg.NewLocalKeg(targetRepo, sb.Runtime())
	require.NoError(t, targetKeg.Init(sb.Context()))
	DisableStrictSchemaPolicy(t, sb.Context(), targetKeg)

	res = NewProcess(t, false, "archive", "import", plainTarPath, "--keg", "~/plain-import-target").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "1\n", string(res.Stdout))
}

func TestArchiveExportCommand_NoHistoryOmitsSnapshots(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t,
		testutils.WithFixture("joe", "~"),
		testutils.WithWd("~/kegs/@local/personal"),
	)

	res := NewProcess(t, false, "snapshot", "create", "1", "--keg", "personal", "-m", "before export").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	exportPath := "~/no-history.keg.tar.gz"
	res = NewProcess(t, false, "archive", "export", "--keg", "personal", "--nodes", "1", "--no-history", "-o", exportPath).Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	targetRepo := keg.NewFsRepo("~/no-history-import-target", sb.Runtime())
	targetKeg := keg.NewLocalKeg(targetRepo, sb.Runtime())
	require.NoError(t, targetKeg.Init(sb.Context()))
	DisableStrictSchemaPolicy(t, sb.Context(), targetKeg)

	res = NewProcess(t, false, "archive", "import", exportPath, "--keg", "~/no-history-import-target").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	res = NewProcess(t, false, "snapshot", "history", "1", "--keg", "~/no-history-import-target").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.NotContains(t, string(res.Stdout), "before export")
}

func TestArchiveImportCommand_FailsWhenHistoryIndexMissing(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t,
		testutils.WithFixture("joe", "~"),
		testutils.WithWd("~/kegs/@local/personal"),
	)

	res := NewProcess(t, false, "snapshot", "create", "1", "--keg", "personal", "-m", "before export").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	exportPath := "~/broken-history.keg.tar.gz"
	res = NewProcess(t, false, "archive", "export", "--keg", "personal", "--nodes", "1", "-o", exportPath).Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	broken := dropArchivePath(t, sb.MustReadFile(exportPath), "keg-archive/nodes/1/snapshots/index.json")
	brokenPath := "~/broken-history-missing-index.keg.tar.gz"
	sb.MustWriteFile(brokenPath, broken, 0o644)

	targetRepo := keg.NewFsRepo("~/broken-history-import-target", sb.Runtime())
	targetKeg := keg.NewLocalKeg(targetRepo, sb.Runtime())
	require.NoError(t, targetKeg.Init(sb.Context()))
	DisableStrictSchemaPolicy(t, sb.Context(), targetKeg)

	res = NewProcess(t, false, "archive", "import", brokenPath, "--keg", "~/broken-history-import-target").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "missing snapshots/index.json")
}

func dropArchivePath(t *testing.T, archive []byte, dropPath string) []byte {
	t.Helper()

	gzr, err := gzip.NewReader(bytes.NewReader(archive))
	require.NoError(t, err)
	defer gzr.Close()

	var raw bytes.Buffer
	tr := tar.NewReader(gzr)
	gzw := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gzw)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		payload, err := io.ReadAll(tr)
		require.NoError(t, err)
		if header.Name == dropPath {
			continue
		}

		copyHeader := *header
		copyHeader.Size = int64(len(payload))
		require.NoError(t, tw.WriteHeader(&copyHeader))
		_, err = tw.Write(payload)
		require.NoError(t, err)
	}

	require.NoError(t, tw.Close())
	require.NoError(t, gzw.Close())
	return raw.Bytes()
}
