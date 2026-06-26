package tapper_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestEditSchema_RejectsMissingSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tap, _, _ := newSchemaEditFixture(t, ctx)

	err := tap.EditSchema(ctx, tapper.EditSchemaOptions{
		Type:   "task",
		Stream: pipedSchemaStream("type: task\n"),
	})
	require.Error(t, err)
	require.ErrorIs(t, err, keg.ErrNotExist)
}

func TestEditSchema_NoopEditsLeaveContentUnchanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tap, k, fx := newSchemaEditFixture(t, ctx)

	original := []byte(`type: task
markdown:
  requireTitle: true
`)
	require.NoError(t, k.WriteSchema(ctx, "task", original))

	err := tap.EditSchema(ctx, tapper.EditSchemaOptions{
		Type:   "task",
		Stream: pipedSchemaStream(string(original)),
	})
	require.NoError(t, err)
	got, err := k.ReadSchema(ctx, "task")
	require.NoError(t, err)
	require.Equal(t, original, got)

	jail := fx.Runtime().GetJail()
	require.NotEmpty(t, jail)
	scriptPath := filepath.Join(jail, "schema-noop-editor.sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	require.NoError(t, fx.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	fx.Runtime().Unset("VISUAL")
	err = tap.EditSchema(ctx, tapper.EditSchemaOptions{Type: "task"})
	require.NoError(t, err)
	got, err = k.ReadSchema(ctx, "task")
	require.NoError(t, err)
	require.Equal(t, original, got)
}

func TestEditSchema_EditorStartsWithSchemaModeline(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tap, k, fx := newSchemaEditFixture(t, ctx)

	original := []byte(`type: task
markdown:
  requireTitle: true
`)
	require.NoError(t, k.WriteSchema(ctx, "task", original))

	jail := fx.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	capturePath := filepath.Join(jail, "captured-schema.yaml")
	scriptPath := filepath.Join(jail, "schema-capture-editor.sh")
	script := fmt.Sprintf("#!/bin/sh\ncp \"$1\" %q\n", capturePath)
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	require.NoError(t, fx.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	fx.Runtime().Unset("VISUAL")

	err = tap.EditSchema(ctx, tapper.EditSchemaOptions{Type: "task"})
	require.NoError(t, err)

	raw, err := os.ReadFile(capturePath)
	require.NoError(t, err)
	opened := string(raw)
	require.True(t, strings.HasPrefix(opened, "# yaml-language-server: $schema="+keg.KegSchemaDefinitionSchemaURL+"\n"))
	require.Contains(t, opened, "type: task")

	got, err := k.ReadSchema(ctx, "task")
	require.NoError(t, err)
	require.Equal(t, original, got, "capturing an unchanged editor file must not persist the modeline")
}

func TestEditSchema_InvalidInputDoesNotOverwrite(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "invalid_yaml",
			raw:  "type: [\n",
		},
		{
			name: "type_mismatch",
			raw:  "type: person\n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			tap, k, _ := newSchemaEditFixture(t, ctx)
			original := []byte("type: task\n")
			require.NoError(t, k.WriteSchema(ctx, "task", original))

			err := tap.EditSchema(ctx, tapper.EditSchemaOptions{
				Type:   "task",
				Stream: pipedSchemaStream(tt.raw),
			})
			require.Error(t, err)

			got, readErr := k.ReadSchema(ctx, "task")
			require.NoError(t, readErr)
			require.Equal(t, original, got)
		})
	}
}

func newSchemaEditFixture(t *testing.T, ctx context.Context) (*tapper.Tap, keg.Keg, *sandbox.Sandbox) {
	t.Helper()
	fx := NewSandbox(t)
	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	k := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, k.Init(ctx))
	tap.KegResolver = func(context.Context, tapper.KegTargetOptions, tapper.FlightRole) (keg.Keg, error) {
		return k, nil
	}
	return tap, k, fx
}

func pipedSchemaStream(raw string) *toolkit.Stream {
	return &toolkit.Stream{
		In:      strings.NewReader(raw),
		IsPiped: true,
	}
}
