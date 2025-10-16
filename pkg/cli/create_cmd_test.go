package cli_test

import (
	"strings"
	"testing"

	"github.com/jlrickert/go-std/testutils"
	"github.com/stretchr/testify/require"
)

func TestCreate_DefaultKeg(t *testing.T) {
	fx := NewFixture(t, testutils.WithFixture("testuser", "/home/testuser"))
	h := NewHarness(t, fx, true)

	// Running `tap create` without a configured default keg should return
	// an error from the app layer. We assert the error is propagated.
	err := h.Run("create", "--title", "Note", "--lead", "one-line")
	require.NoError(t, err)

	// The CLI prints the created node id/path to stdout.
	out := h.OutBuf.String()
	require.Equal(t, out, "1", "expected stdout to contain the created node id")
	trim := strings.TrimSpace(out)
	require.Regexp(t, `^\d+`, trim, "stdout should start with numeric node id")

	content := fx.MustReadJailFile("~/kegs/example/1/README.md")
	require.Contains(t, string(content), "# Note")
	require.Contains(t, string(content), "one-line")

	meta := fx.MustReadJailFile("~/kegs/example/1/meta.yaml")
	require.Contains(t, string(meta), "title: Note")
	require.Contains(t, string(meta), "lead: one-line")

	// Ensure meta includes timestamps for created and updated.
	require.Contains(t, string(meta), "created:", "meta should include created field")
	require.Contains(t, string(meta), "updated:", "meta should include updated field")
}

// func TestCreate_NoDefaultKeg_returnsError(t *testing.T) {
// 	fx := NewFixture(t)
// 	h := NewHarness(t, fx, true)
//
// 	// Running `tap create` without a configured default keg should return
// 	// an error from the app layer. We assert the error is propagated.
// 	err := h.Run("create", "--title", "Note", "--lead", "one-line")
// 	require.Error(t, err)
// 	require.True(t,
// 		strings.Contains(err.Error(), "unable to create node"),
// 		"unexpected error: %q", err.Error(),
// 	)
//
// 	// No stdout should be produced on failure.
// 	require.Equal(t, "", h.OutBuf.String())
// }
