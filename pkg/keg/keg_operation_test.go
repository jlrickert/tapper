package keg_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func exerciseOperationBoundary(t *testing.T, repo keg.Repository) {
	t.Helper()
	ctx := context.Background()

	var calls int
	require.NoError(t, repo.WithKegWrite(ctx, func(writeCtx context.Context) error {
		calls++
		if err := repo.WithKegRead(writeCtx, func(context.Context) error { calls++; return nil }); err != nil {
			return err
		}
		return repo.WithKegWrite(writeCtx, func(context.Context) error { calls++; return nil })
	}))
	require.Equal(t, 3, calls)

	require.NoError(t, repo.WithKegRead(ctx, func(readCtx context.Context) error {
		require.NoError(t, repo.WithKegRead(readCtx, func(context.Context) error { return nil }))
		err := repo.WithKegWrite(readCtx, func(context.Context) error { return nil })
		require.ErrorIs(t, err, keg.ErrKegLockUpgrade)
		return nil
	}))

	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- repo.WithKegWrite(ctx, func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	waitCtx, cancel := context.WithCancel(ctx)
	cancel()
	err := repo.WithKegRead(waitCtx, func(context.Context) error { return nil })
	require.ErrorIs(t, err, keg.ErrLockTimeout)
	require.ErrorIs(t, err, context.Canceled)
	close(release)
	require.NoError(t, <-done)
}

func TestMemoryRepoKegOperationBoundary(t *testing.T) {
	fx := NewSandbox(t)
	exerciseOperationBoundary(t, keg.NewMemoryRepo(fx.Runtime()))
}

func TestFsRepoKegOperationBoundaryAndStaleOwnerCleanup(t *testing.T) {
	fx := NewSandbox(t)
	repo := keg.NewFsRepo("repo", fx.Runtime())
	exerciseOperationBoundary(t, repo)

	lockPath := filepath.Join("repo", keg.KegOperationLock)
	require.NoError(t, fx.Runtime().Mkdir(lockPath, 0o700, true))
	require.NoError(t, fx.Runtime().WriteFile(filepath.Join(lockPath, "owner.json"), []byte(`{"pid":2147483647,"hostname":"stale","started_at":"2000-01-01T00:00:00Z","uid":"test"}`), 0o600))
	require.NoError(t, repo.WithKegRead(fx.Context(), func(context.Context) error { return nil }))
	_, err := fx.Runtime().Stat(lockPath, false)
	require.Error(t, err)
}

type dexPhaseRepo struct {
	keg.Repository
	armed    atomic.Bool
	active   atomic.Int32
	max      atomic.Int32
	attempts chan struct{}
	entered  chan struct{}
	release  chan struct{}
	blockOne atomic.Bool
}

func newDexPhaseRepo(base keg.Repository) *dexPhaseRepo {
	return &dexPhaseRepo{
		Repository: base,
		attempts:   make(chan struct{}, 4),
		entered:    make(chan struct{}, 4),
		release:    make(chan struct{}),
	}
}

func (r *dexPhaseRepo) WithKegWrite(ctx context.Context, fn func(context.Context) error) error {
	if r.armed.Load() {
		r.attempts <- struct{}{}
	}
	return r.Repository.WithKegWrite(ctx, fn)
}

func (r *dexPhaseRepo) WriteIndex(ctx context.Context, name string, data []byte) error {
	if r.armed.Load() && name == "nodes.tsv" {
		active := r.active.Add(1)
		for old := r.max.Load(); active > old && !r.max.CompareAndSwap(old, active); old = r.max.Load() {
		}
		r.entered <- struct{}{}
		if r.blockOne.CompareAndSwap(true, false) {
			<-r.release
		}
		defer r.active.Add(-1)
	}
	return r.Repository.WriteIndex(ctx, name, data)
}

func TestDifferentNodeWritersSerializeCompleteDexPersistence(t *testing.T) {
	fx := NewSandbox(t)
	base := keg.NewMemoryRepo(fx.Runtime())
	repo := newDexPhaseRepo(base)
	first := keg.NewLocalKeg(repo, fx.Runtime())
	second := keg.NewLocalKeg(repo, fx.Runtime())
	require.NoError(t, first.Init(fx.Context()))
	one, err := first.Create(fx.Context(), &keg.CreateOptions{Body: []byte("# One\n\nold one\n")})
	require.NoError(t, err)
	two, err := first.Create(fx.Context(), &keg.CreateOptions{Body: []byte("# Two\n\nold two\n")})
	require.NoError(t, err)

	repo.blockOne.Store(true)
	repo.armed.Store(true)
	errs := make(chan error, 2)
	go func() { errs <- first.SetContent(fx.Context(), one.ID, []byte("# One updated\n\nnew one\n")) }()
	<-repo.attempts
	<-repo.entered
	go func() { errs <- second.SetContent(fx.Context(), two.ID, []byte("# Two updated\n\nnew two\n")) }()
	<-repo.attempts
	require.Equal(t, int32(1), repo.active.Load(), "second writer entered the dex persistence phase")
	close(repo.release)
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	require.Equal(t, int32(1), repo.max.Load())
	repo.armed.Store(false)

	before, err := first.DexArtifacts(fx.Context())
	require.NoError(t, err)
	require.NoError(t, first.Index(fx.Context(), keg.IndexOptions{NoUpdate: true}))
	after, err := first.DexArtifacts(fx.Context())
	require.NoError(t, err)
	require.Equal(t, before.Indexes, after.Indexes, "persisted indexes differ from a clean rebuild")
}

func TestConcurrentMutationsMatchCleanDexRebuild(t *testing.T) {
	fx := NewSandbox(t)
	repo := keg.NewMemoryRepo(fx.Runtime())
	first := keg.NewLocalKeg(repo, fx.Runtime())
	second := keg.NewLocalKeg(repo, fx.Runtime())
	require.NoError(t, first.Init(fx.Context()))
	removeTarget, err := first.Create(fx.Context(), &keg.CreateOptions{Body: []byte("# Remove target\n\nold\n")})
	require.NoError(t, err)
	updateTarget, err := first.Create(fx.Context(), &keg.CreateOptions{Body: []byte("# Update target\n\nold\n")})
	require.NoError(t, err)

	start := make(chan struct{})
	errs := make(chan error, 4)
	created := make(chan keg.NodeId, 1)
	go func() {
		<-start
		result, err := first.Create(fx.Context(), &keg.CreateOptions{Body: []byte("# Concurrent create\n\nnew\n")})
		if err == nil {
			created <- result.ID
		}
		errs <- err
	}()
	go func() {
		<-start
		errs <- second.SetContent(fx.Context(), updateTarget.ID, []byte("# Updated concurrently\n\nnew\n"))
	}()
	go func() {
		<-start
		_, err := first.Remove(fx.Context(), removeTarget.ID)
		errs <- err
	}()
	go func() {
		<-start
		errs <- second.Index(fx.Context(), keg.IndexOptions{NoUpdate: true})
	}()
	close(start)
	for range 4 {
		require.NoError(t, <-errs)
	}
	createdID := <-created

	updated, err := first.ReadNode(fx.Context(), updateTarget.ID)
	require.NoError(t, err)
	require.Contains(t, string(updated.Content), "Updated concurrently")
	exists, err := first.NodeExists(fx.Context(), removeTarget.ID)
	require.NoError(t, err)
	require.False(t, exists)
	exists, err = first.NodeExists(fx.Context(), createdID)
	require.NoError(t, err)
	require.True(t, exists)

	before, err := first.DexArtifacts(fx.Context())
	require.NoError(t, err)
	require.NoError(t, first.Index(fx.Context(), keg.IndexOptions{NoUpdate: true}))
	after, err := first.DexArtifacts(fx.Context())
	require.NoError(t, err)
	require.Equal(t, before.Indexes, after.Indexes, "concurrent mutations left indexes different from a clean rebuild")
}

type snapshotPhaseRepo struct {
	keg.Repository
	armed       atomic.Bool
	readEntered chan struct{}
	releaseRead chan struct{}
	writeStart  chan struct{}
	readOnce    sync.Once
}

func (r *snapshotPhaseRepo) WithKegWrite(ctx context.Context, fn func(context.Context) error) error {
	if r.armed.Load() {
		select {
		case r.writeStart <- struct{}{}:
		default:
		}
	}
	return r.Repository.WithKegWrite(ctx, fn)
}

func (r *snapshotPhaseRepo) ReadContent(ctx context.Context, id keg.NodeId) ([]byte, error) {
	data, err := r.Repository.ReadContent(ctx, id)
	if err == nil && r.armed.Load() {
		r.readOnce.Do(func() {
			close(r.readEntered)
			<-r.releaseRead
		})
	}
	return data, err
}

func TestAggregateReadCannotMixNodeGenerations(t *testing.T) {
	fx := NewSandbox(t)
	base := keg.NewMemoryRepo(fx.Runtime())
	repo := &snapshotPhaseRepo{
		Repository:  base,
		readEntered: make(chan struct{}),
		releaseRead: make(chan struct{}),
		writeStart:  make(chan struct{}, 1),
	}
	reader := keg.NewLocalKeg(repo, fx.Runtime())
	writer := keg.NewLocalKeg(repo, fx.Runtime())
	require.NoError(t, reader.Init(fx.Context()))
	created, err := reader.Create(fx.Context(), &keg.CreateOptions{Body: []byte("# Old\n\nold body\n")})
	require.NoError(t, err)
	before, err := reader.ReadNode(fx.Context(), created.ID)
	require.NoError(t, err)

	repo.armed.Store(true)
	readResult := make(chan *keg.NodeView, 1)
	readErr := make(chan error, 1)
	go func() {
		view, err := reader.ReadNode(fx.Context(), created.ID)
		readResult <- view
		readErr <- err
	}()
	<-repo.readEntered
	writeErr := make(chan error, 1)
	go func() { writeErr <- writer.SetContent(fx.Context(), created.ID, []byte("# New\n\nnew body\n")) }()
	<-repo.writeStart
	close(repo.releaseRead)
	require.NoError(t, <-readErr)
	view := <-readResult
	require.Equal(t, before.Content, view.Content)
	require.Equal(t, before.Meta, view.Meta)
	require.Equal(t, before.Stats.Hash(), view.Stats.Hash())
	require.NoError(t, <-writeErr)
	repo.armed.Store(false)
	after, err := reader.ReadNode(fx.Context(), created.ID)
	require.NoError(t, err)
	require.Contains(t, string(after.Content), "# New")
	require.NotEqual(t, before.Stats.Hash(), after.Stats.Hash(), fmt.Sprintf("hash remained %q", before.Stats.Hash()))
}

func TestOperationBoundaryRejectsNilCallback(t *testing.T) {
	fx := NewSandbox(t)
	repo := keg.NewMemoryRepo(fx.Runtime())
	require.Error(t, repo.WithKegRead(context.Background(), nil))
	require.Error(t, repo.WithKegWrite(context.Background(), nil))
	require.False(t, errors.Is(repo.WithKegWrite(context.Background(), nil), keg.ErrKegLockUpgrade))
}
