package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

// imageFixture returns a sandbox pre-loaded with the testuser keg fixture and
// the shared images fixture (which provides data/images/default.png).
func imageFixture(t *testing.T) *testutils.Sandbox {
	t.Helper()
	return NewSandbox(t,
		testutils.WithFixture("testuser", "~"),
		testutils.WithFixture("images", "~/test-images"),
	)
}

func TestImageUpload_OutputsStoredName(t *testing.T) {
	t.Parallel()
	sb := imageFixture(t)

	res := NewProcess(t, false, "image", "upload", "0", "~/test-images/default.png").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "default.png", strings.TrimSpace(string(res.Stdout)))
}

func TestImageUpload_StoresInImagesDir(t *testing.T) {
	t.Parallel()
	sb := imageFixture(t)

	res := NewProcess(t, false, "image", "upload", "0", "~/test-images/default.png").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	uploaded := sb.MustReadFile("~/kegs/example/0/images/default.png")
	require.NotEmpty(t, uploaded)
}

func TestImageUpload_CustomName(t *testing.T) {
	t.Parallel()
	sb := imageFixture(t)

	res := NewProcess(t, false, "image", "upload", "0", "~/test-images/default.png", "--name", "hero.png").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "hero.png", strings.TrimSpace(string(res.Stdout)))

	uploaded := sb.MustReadFile("~/kegs/example/0/images/hero.png")
	require.NotEmpty(t, uploaded)
}

func TestImageUpload_ContentsPreserved(t *testing.T) {
	t.Parallel()
	sb := imageFixture(t)

	original := sb.MustReadFile("~/test-images/default.png")

	NewProcess(t, false, "image", "upload", "0", "~/test-images/default.png").
		Run(sb.Context(), sb.Runtime())

	stored := sb.MustReadFile("~/kegs/example/0/images/default.png")
	require.Equal(t, original, stored)
}

func TestImageList_ShowsUploadedImages(t *testing.T) {
	t.Parallel()
	sb := imageFixture(t)

	NewProcess(t, false, "image", "upload", "0", "~/test-images/default.png").
		Run(sb.Context(), sb.Runtime())
	sb.MustWriteFile("~/test-images/banner.png", []byte("fake banner"), 0o644)
	NewProcess(t, false, "image", "upload", "0", "~/test-images/banner.png").
		Run(sb.Context(), sb.Runtime())

	res := NewProcess(t, false, "image", "ls", "0").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := strings.TrimSpace(string(res.Stdout))
	require.Contains(t, out, "default.png")
	require.Contains(t, out, "banner.png")
}

func TestImageList_EmptyWhenNoImages(t *testing.T) {
	t.Parallel()
	sb := imageFixture(t)

	res := NewProcess(t, false, "image", "ls", "0").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Empty(t, strings.TrimSpace(string(res.Stdout)))
}

func TestImageDownload_WritesToDest(t *testing.T) {
	t.Parallel()
	sb := imageFixture(t)

	original := sb.MustReadFile("~/test-images/default.png")
	NewProcess(t, false, "image", "upload", "0", "~/test-images/default.png").
		Run(sb.Context(), sb.Runtime())

	res := NewProcess(t, false, "image", "download", "0", "default.png", "--dest", "~/dl-default.png").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "dl-default.png")

	got := sb.MustReadFile("~/dl-default.png")
	require.Equal(t, original, got)
}

func TestImageDownload_DefaultDestIsNameInCwd(t *testing.T) {
	t.Parallel()
	sb := imageFixture(t)

	NewProcess(t, false, "image", "upload", "0", "~/test-images/default.png").
		Run(sb.Context(), sb.Runtime())

	res := NewProcess(t, false, "image", "download", "0", "default.png").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := strings.TrimSpace(string(res.Stdout))
	require.True(t, strings.HasSuffix(out, "default.png"), "expected dest to end with default.png, got %q", out)
}

func TestImageRm_RemovesImage(t *testing.T) {
	t.Parallel()
	sb := imageFixture(t)

	NewProcess(t, false, "image", "upload", "0", "~/test-images/default.png").
		Run(sb.Context(), sb.Runtime())

	rmRes := NewProcess(t, false, "image", "rm", "0", "default.png").Run(sb.Context(), sb.Runtime())
	require.NoError(t, rmRes.Err)
	require.Empty(t, strings.TrimSpace(string(rmRes.Stdout)), "rm should produce no output on success")

	lsRes := NewProcess(t, false, "image", "ls", "0").Run(sb.Context(), sb.Runtime())
	require.NoError(t, lsRes.Err)
	require.Empty(t, strings.TrimSpace(string(lsRes.Stdout)))
}

func TestImage_ErrorCases(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantErrFrag string
	}{
		{
			name:        "upload_to_missing_node",
			args:        []string{"image", "upload", "999", "~/test-images/default.png"},
			wantErrFrag: "node 999 not found",
		},
		{
			name:        "upload_invalid_node_id",
			args:        []string{"image", "upload", "bad-id", "~/test-images/default.png"},
			wantErrFrag: "invalid node ID",
		},
		{
			name:        "download_missing_image",
			args:        []string{"image", "download", "0", "ghost.png"},
			wantErrFrag: "ghost.png",
		},
		{
			name:        "rm_missing_image",
			args:        []string{"image", "rm", "0", "ghost.png"},
			wantErrFrag: "ghost.png",
		},
		{
			name:        "ls_missing_node",
			args:        []string{"image", "ls", "999"},
			wantErrFrag: "999",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sb := imageFixture(t)
			res := NewProcess(t, false, tc.args...).Run(sb.Context(), sb.Runtime())
			require.Error(t, res.Err)
			require.Contains(t, string(res.Stderr), tc.wantErrFrag)
		})
	}
}
