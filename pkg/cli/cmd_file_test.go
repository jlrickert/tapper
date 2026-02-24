package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

// fileFixture returns a sandbox pre-loaded with the testuser keg fixture and
// the shared images fixture (which provides data/images/default.png).
func fileFixture(t *testing.T) *testutils.Sandbox {
	t.Helper()
	return NewSandbox(t,
		testutils.WithFixture("testuser", "~"),
		testutils.WithFixture("images", "~/test-images"),
	)
}

func TestFileUpload_OutputsStoredName(t *testing.T) {
	t.Parallel()
	sb := fileFixture(t)

	res := NewProcess(t, false, "file", "upload", "0", "~/test-images/default.png").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "default.png", strings.TrimSpace(string(res.Stdout)))
}

func TestFileUpload_StoresInAssetsDir(t *testing.T) {
	t.Parallel()
	sb := fileFixture(t)

	res := NewProcess(t, false, "file", "upload", "0", "~/test-images/default.png").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	// Confirm the file landed in assets/ (not attachments/).
	uploaded := sb.MustReadFile("~/kegs/example/0/assets/default.png")
	require.NotEmpty(t, uploaded)
}

func TestFileUpload_CustomName(t *testing.T) {
	t.Parallel()
	sb := fileFixture(t)

	res := NewProcess(t, false, "file", "upload", "0", "~/test-images/default.png", "--name", "renamed.png").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "renamed.png", strings.TrimSpace(string(res.Stdout)))

	uploaded := sb.MustReadFile("~/kegs/example/0/assets/renamed.png")
	require.NotEmpty(t, uploaded)
}

func TestFileUpload_ContentsPreserved(t *testing.T) {
	t.Parallel()
	sb := fileFixture(t)

	original := sb.MustReadFile("~/test-images/default.png")

	NewProcess(t, false, "file", "upload", "0", "~/test-images/default.png").
		Run(sb.Context(), sb.Runtime())

	stored := sb.MustReadFile("~/kegs/example/0/assets/default.png")
	require.Equal(t, original, stored)
}

func TestFileList_ShowsUploadedFiles(t *testing.T) {
	t.Parallel()
	sb := fileFixture(t)

	NewProcess(t, false, "file", "upload", "0", "~/test-images/default.png").
		Run(sb.Context(), sb.Runtime())
	sb.MustWriteFile("~/test-images/other.txt", []byte("other"), 0o644)
	NewProcess(t, false, "file", "upload", "0", "~/test-images/other.txt").
		Run(sb.Context(), sb.Runtime())

	res := NewProcess(t, false, "file", "ls", "0").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := strings.TrimSpace(string(res.Stdout))
	require.Contains(t, out, "default.png")
	require.Contains(t, out, "other.txt")
}

func TestFileList_EmptyWhenNoFiles(t *testing.T) {
	t.Parallel()
	sb := fileFixture(t)

	res := NewProcess(t, false, "file", "ls", "0").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Empty(t, strings.TrimSpace(string(res.Stdout)))
}

func TestFileDownload_WritesToDest(t *testing.T) {
	t.Parallel()
	sb := fileFixture(t)

	original := sb.MustReadFile("~/test-images/default.png")
	NewProcess(t, false, "file", "upload", "0", "~/test-images/default.png").
		Run(sb.Context(), sb.Runtime())

	res := NewProcess(t, false, "file", "download", "0", "default.png", "--dest", "~/dl-default.png").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "dl-default.png")

	got := sb.MustReadFile("~/dl-default.png")
	require.Equal(t, original, got)
}

func TestFileDownload_DefaultDestIsNameInCwd(t *testing.T) {
	t.Parallel()
	sb := fileFixture(t)

	NewProcess(t, false, "file", "upload", "0", "~/test-images/default.png").
		Run(sb.Context(), sb.Runtime())

	res := NewProcess(t, false, "file", "download", "0", "default.png").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	// The destination path printed should end with the filename.
	out := strings.TrimSpace(string(res.Stdout))
	require.True(t, strings.HasSuffix(out, "default.png"), "expected dest to end with default.png, got %q", out)
}

func TestFileRm_RemovesFile(t *testing.T) {
	t.Parallel()
	sb := fileFixture(t)

	NewProcess(t, false, "file", "upload", "0", "~/test-images/default.png").
		Run(sb.Context(), sb.Runtime())

	rmRes := NewProcess(t, false, "file", "rm", "0", "default.png").Run(sb.Context(), sb.Runtime())
	require.NoError(t, rmRes.Err)
	require.Empty(t, strings.TrimSpace(string(rmRes.Stdout)), "rm should produce no output on success")

	lsRes := NewProcess(t, false, "file", "ls", "0").Run(sb.Context(), sb.Runtime())
	require.NoError(t, lsRes.Err)
	require.Empty(t, strings.TrimSpace(string(lsRes.Stdout)))
}

func TestFile_ErrorCases(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantErrFrag string
	}{
		{
			name:        "upload_to_missing_node",
			args:        []string{"file", "upload", "999", "~/test-images/default.png"},
			wantErrFrag: "node 999 not found",
		},
		{
			name:        "upload_invalid_node_id",
			args:        []string{"file", "upload", "bad-id", "~/test-images/default.png"},
			wantErrFrag: "invalid node ID",
		},
		{
			name:        "download_missing_file",
			args:        []string{"file", "download", "0", "ghost.txt"},
			wantErrFrag: "ghost.txt",
		},
		{
			name:        "rm_missing_file",
			args:        []string{"file", "rm", "0", "ghost.txt"},
			wantErrFrag: "ghost.txt",
		},
		{
			name:        "ls_missing_node",
			args:        []string{"file", "ls", "999"},
			wantErrFrag: "999",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sb := fileFixture(t)
			res := NewProcess(t, false, tc.args...).Run(sb.Context(), sb.Runtime())
			require.Error(t, res.Err)
			require.Contains(t, string(res.Stderr), tc.wantErrFrag)
		})
	}
}
