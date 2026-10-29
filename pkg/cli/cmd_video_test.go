package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

// cliMP4 is the opening of an MP4 file: enough for its container to be
// recognized as video.
var cliMP4 = []byte{0, 0, 0, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm', 0, 0, 2, 0, 'i', 's', 'o', 'm', 'm', 'p', '4', '1'}

// videoFixture is the testuser keg with a short video at ~/clip.mp4.
func videoFixture(t *testing.T) *testutils.Sandbox {
	t.Helper()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))
	sb.MustWriteFile("~/clip.mp4", cliMP4, 0o644)
	return sb
}

// A video uploads to a node, lists, downloads byte for byte, and deletes.
func TestVideo_RoundTrip(t *testing.T) {
	t.Parallel()
	sb := videoFixture(t)

	up := NewProcess(t, false, "video", "upload", "0", "~/clip.mp4").Run(sb.Context(), sb.Runtime())
	require.NoError(t, up.Err)
	require.Equal(t, "clip.mp4", strings.TrimSpace(string(up.Stdout)))

	ls := NewProcess(t, false, "video", "list", "0").Run(sb.Context(), sb.Runtime())
	require.NoError(t, ls.Err)
	require.Equal(t, "clip.mp4", strings.TrimSpace(string(ls.Stdout)))

	dl := NewProcess(t, false, "video", "download", "0", "clip.mp4", "~/copy.mp4").Run(sb.Context(), sb.Runtime())
	require.NoError(t, dl.Err)
	require.Equal(t, cliMP4, sb.MustReadFile("~/copy.mp4"))

	out := NewProcess(t, false, "video", "download", "0", "clip.mp4", "-").Run(sb.Context(), sb.Runtime())
	require.NoError(t, out.Err)
	require.Equal(t, cliMP4, out.Stdout, "- writes the video to stdout")

	rm := NewProcess(t, false, "video", "delete", "0", "clip.mp4").Run(sb.Context(), sb.Runtime())
	require.NoError(t, rm.Err)
	ls = NewProcess(t, false, "video", "list", "0").Run(sb.Context(), sb.Runtime())
	require.NoError(t, ls.Err)
	require.Empty(t, strings.TrimSpace(string(ls.Stdout)))
}

// A file that is not a supported video container is refused.
func TestVideo_UploadRefusesNonVideo(t *testing.T) {
	t.Parallel()
	sb := videoFixture(t)
	sb.MustWriteFile("~/notes.mp4", []byte("just some text"), 0o644)

	res := NewProcess(t, false, "video", "upload", "0", "~/notes.mp4").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
}
