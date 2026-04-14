package tapper_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// allocateShadowReservation creates a bare node directory via FsRepo.Next()
// without writing content. This models what WithNodeLock or a half-completed
// Create would leave behind. The returned ID has HasNode(true) but
// ReadContent returns ErrNotExist.
//
// It returns the resolved Keg so the caller can hand it to the Tap under test
// — but the bare reservation is visible to any Keg pointed at the same root
// because the filesystem is the source of truth.
func allocateShadowReservation(t *testing.T, fx *sandbox.Sandbox) string {
	t.Helper()
	k, err := keg.NewKegFromTarget(
		fx.Context(),
		kegurl.NewFile("/home/testuser/kegs/test"),
		fx.Runtime(),
	)
	require.NoError(t, err)
	id, err := k.Repo.Next(fx.Context())
	require.NoError(t, err)

	// Sanity: HasNode is true (the bare dir exists), but ReadContent is
	// ErrNotExist (no README.md). This is the shape we want to exercise.
	has, err := k.Repo.HasNode(fx.Context(), id)
	require.NoError(t, err)
	require.True(t, has, "shadow reservation should make HasNode true")
	_, err = k.Repo.ReadContent(fx.Context(), id)
	require.ErrorIs(t, err, keg.ErrNotExist,
		"shadow reservation must have no content (F2 precondition)")

	return id.Path()
}

// TestMeta_ShadowReservationRejected verifies that Tap.Meta refuses to
// operate on a bare node directory left behind by FsRepo.Next() /
// WithNodeLock (F2 of report 842). Before the fix this existence gate
// used Repo.HasNode, which returns true for shadow reservations, and the
// call would proceed to return empty metadata as if the node were real.
func TestMeta_ShadowReservationRejected(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	tap := setupTapWithKeg(t, fx)

	shadowID := allocateShadowReservation(t, fx)

	_, err := tap.Meta(fx.Context(), tapper.MetaOptions{NodeID: shadowID})
	require.Error(t, err, "Meta should reject shadow reservations")
	require.Contains(t, err.Error(), "not found",
		"error should report the node as missing, not proceed")
}

// TestEdit_ShadowReservationRejected verifies that Tap.Edit refuses to
// open a bare node directory for editing (F2 of report 842). The
// dangerous pre-fix behaviour was to pass the existence gate and then
// attempt to read content from a directory with no README.md — leading
// to an empty-body edit session that would create content on a node
// the user never Created.
func TestEdit_ShadowReservationRejected(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	tap := setupTapWithKeg(t, fx)

	shadowID := allocateShadowReservation(t, fx)

	stream := &toolkit.Stream{
		In:      io.NopCloser(bytes.NewReader([]byte("# Ghost\n"))),
		IsPiped: true,
	}
	err := tap.Edit(fx.Context(), tapper.EditOptions{
		NodeID: shadowID,
		Stream: stream,
	})
	require.Error(t, err, "Edit should reject shadow reservations")
	require.Contains(t, err.Error(), "not found")
}

// TestUploadFile_ShadowReservationRejected verifies that Tap.UploadFile
// refuses to attach a file to a shadow reservation (F2 of report 842).
// The pre-fix bug was the most dangerous of the five F2 sites: concurrent
// file upload against a bare directory would silently create attachments
// on a contentless node.
func TestUploadFile_ShadowReservationRejected(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	tap := setupTapWithKeg(t, fx)

	// Stage a source file in the sandbox so UploadFile has something to
	// read. The existence gate under test runs before this file is read,
	// so we need it staged but the content is immaterial.
	src := "/home/testuser/src.txt"
	require.NoError(t, fx.Runtime().WriteFile(src, []byte("payload"), 0o644))

	shadowID := allocateShadowReservation(t, fx)

	_, err := tap.UploadFile(fx.Context(), tapper.UploadFileOptions{
		NodeID:   shadowID,
		FilePath: src,
	})
	require.Error(t, err, "UploadFile should reject shadow reservations")
	require.Contains(t, err.Error(), "not found")
}

// TestUploadImage_ShadowReservationRejected is the image-attachment twin
// of TestUploadFile_ShadowReservationRejected (F2 of report 842).
func TestUploadImage_ShadowReservationRejected(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	tap := setupTapWithKeg(t, fx)

	src := "/home/testuser/pic.png"
	require.NoError(t, fx.Runtime().WriteFile(src, []byte("\x89PNG\r\n\x1a\n"), 0o644))

	shadowID := allocateShadowReservation(t, fx)

	_, err := tap.UploadImage(fx.Context(), tapper.UploadImageOptions{
		NodeID:   shadowID,
		FilePath: src,
	})
	require.Error(t, err, "UploadImage should reject shadow reservations")
	require.Contains(t, err.Error(), "not found")
}

// TestLock_ShadowReservationRejected verifies that Tap.Lock refuses to
// issue a cross-process lock token against a shadow reservation (F2 of
// report 842). The pre-fix bug allowed acquiring a lock against a node
// that did not actually exist — an authentication hole against a
// nonexistent target.
func TestLock_ShadowReservationRejected(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	tap := setupTapWithKeg(t, fx)

	shadowID := allocateShadowReservation(t, fx)

	_, err := tap.Lock(fx.Context(), tapper.LockOptions{NodeID: shadowID})
	require.Error(t, err, "Lock should reject shadow reservations")
	require.Contains(t, err.Error(), "not found")
}
