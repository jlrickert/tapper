package tapper

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/keg"
)

// TestKegResolverSeam pins the hub seam: when Tap.KegResolver is set, both the
// read path (resolveKeg → viewer) and the write path (resolveKegForRole →
// editor) delegate to it with the calling operation's role and propagate its
// result, bypassing config-driven resolution entirely. tapper-hub relies on
// this to scope a single /mcp connector to the authenticated user's catalog.
func TestKegResolverSeam(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var gotRoles []FlightRole
	var gotKegs []string
	sentinel := errors.New("resolver invoked")

	tap := &Tap{
		KegResolver: func(_ context.Context, opts KegTargetOptions, role FlightRole) (keg.Keg, error) {
			gotRoles = append(gotRoles, role)
			gotKegs = append(gotKegs, opts.Keg)
			return nil, sentinel
		},
	}

	// Read path: resolveKeg defaults to viewer.
	_, err := tap.resolveKeg(ctx, KegTargetOptions{Keg: "@alice/notes"})
	require.ErrorIs(t, err, sentinel)

	// Write path: resolveKegForRole carries editor through to the resolver.
	_, err = tap.resolveKegForRole(ctx, KegTargetOptions{Keg: "@alice/notes"}, FlightRoleEditor)
	require.ErrorIs(t, err, sentinel)

	require.Equal(t, []FlightRole{FlightRoleViewer, FlightRoleEditor}, gotRoles)
	require.Equal(t, []string{"@alice/notes", "@alice/notes"}, gotKegs)
}

// TestKegResolverSeam_ReturnsKeg confirms the resolver's keg.Keg flows straight
// back to the caller untouched (no flight gating wraps it).
func TestKegResolverSeam_ReturnsKeg(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	want := &keg.LocalKeg{}
	tap := &Tap{
		KegResolver: func(context.Context, KegTargetOptions, FlightRole) (keg.Keg, error) {
			return want, nil
		},
	}

	got, err := tap.resolveKeg(ctx, KegTargetOptions{Keg: "@alice/notes"})
	require.NoError(t, err)
	require.Same(t, want, got)
}
