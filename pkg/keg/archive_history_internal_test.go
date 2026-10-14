package keg

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

// snapshotWriteRecorder records the snapshot writes a repository receives.
type snapshotWriteRecorder struct {
	*testMemoryRepository
	writes []SnapshotWrite
}

func (r *snapshotWriteRecorder) AppendSnapshot(ctx context.Context, id NodeId, in SnapshotWrite) (Snapshot, error) {
	r.writes = append(r.writes, in)
	return r.testMemoryRepository.AppendSnapshot(ctx, id, in)
}

// A repository stores the content hash it is handed, so import must hand it
// one for every archived revision, exactly as taking a snapshot does.
func TestImportNodesHandsSnapshotContentHashToRepository(t *testing.T) {
	fx := sandbox.NewSandbox(t, nil)
	ctx := fx.Context()
	src := NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, src.Init(ctx))
	require.NoError(t, src.UpdateSettings(ctx, func(cfg *Settings) {
		if cfg.SchemaPolicy == nil {
			cfg.SchemaPolicy = &SchemaPolicy{}
		}
		cfg.SchemaPolicy.Strict = false
	}))
	node, err := src.Create(ctx, &CreateOptions{Body: []byte("# Node\n")})
	require.NoError(t, err)
	want, err := src.AppendSnapshot(ctx, node.ID, "first")
	require.NoError(t, err)
	require.NotEmpty(t, want.ContentHash)
	rc, err := src.ExportNodes(ctx, ExportNodesOptions{WithHistory: true})
	require.NoError(t, err)
	archive, err := io.ReadAll(rc)
	require.NoError(t, err)

	repo := &snapshotWriteRecorder{testMemoryRepository: newTestMemoryRepo(fx.Runtime())}
	dst := NewLocalKeg(repo, fx.Runtime())
	require.NoError(t, dst.Init(ctx))
	_, err = dst.ImportNodes(ctx, bytes.NewReader(archive), ImportNodesOptions{})
	require.NoError(t, err)
	require.Len(t, repo.writes, 1)
	require.Equal(t, want.ContentHash, repo.writes[0].Content.Hash)
}
