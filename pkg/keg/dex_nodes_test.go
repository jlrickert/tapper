package keg_test

import (
	"context"
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestParseNodeIndex_FiveColumnFormat(t *testing.T) {
	t.Parallel()
	data := []byte("42\t2025-01-02T15:04:05Z\t2024-06-01T10:00:00Z\t2025-01-03T08:00:00Z\tMy Title\n" +
		"0\t2024-12-01T12:00:00Z\t2024-01-01T00:00:00Z\t2024-12-02T09:00:00Z\tZero Node\n")

	idx, err := keg.ParseNodeIndex(context.Background(), data)
	require.NoError(t, err)

	entries := idx.List(context.Background())
	require.Len(t, entries, 2)

	require.Equal(t, "42", entries[0].ID)
	require.Equal(t, "My Title", entries[0].Title)
	require.Equal(t, time.Date(2025, 1, 2, 15, 4, 5, 0, time.UTC), entries[0].Updated)
	require.Equal(t, time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC), entries[0].Created)
	require.Equal(t, time.Date(2025, 1, 3, 8, 0, 0, 0, time.UTC), entries[0].Accessed)

	require.Equal(t, "0", entries[1].ID)
	require.Equal(t, "Zero Node", entries[1].Title)
	require.Equal(t, time.Date(2024, 12, 1, 12, 0, 0, 0, time.UTC), entries[1].Updated)
	require.Equal(t, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), entries[1].Created)
	require.Equal(t, time.Date(2024, 12, 2, 9, 0, 0, 0, time.UTC), entries[1].Accessed)
}

func TestParseNodeIndex_ThreeColumnLegacy(t *testing.T) {
	t.Parallel()
	data := []byte("42\t2025-01-02T15:04:05Z\tMy Title\n" +
		"0\t2024-12-01T12:00:00Z\tZero Node\n")

	idx, err := keg.ParseNodeIndex(context.Background(), data)
	require.NoError(t, err)

	entries := idx.List(context.Background())
	require.Len(t, entries, 2)

	require.Equal(t, "42", entries[0].ID)
	require.Equal(t, "My Title", entries[0].Title)
	require.Equal(t, time.Date(2025, 1, 2, 15, 4, 5, 0, time.UTC), entries[0].Updated)
	require.True(t, entries[0].Created.IsZero(), "created should be zero for legacy format")
	require.True(t, entries[0].Accessed.IsZero(), "accessed should be zero for legacy format")
}

func TestNodeIndex_DataEmitsFiveColumns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	updated := time.Date(2025, 1, 2, 15, 4, 5, 0, time.UTC)
	created := time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC)
	accessed := time.Date(2025, 1, 3, 8, 0, 0, 0, time.UTC)

	stats := keg.NewStats(created)
	stats.SetTitle("Test Title")
	stats.SetUpdated(updated)
	stats.SetAccessed(accessed)

	nd := &keg.NodeData{
		ID:    keg.NodeId{ID: 42},
		Stats: stats,
	}

	idx, err := keg.ParseNodeIndex(ctx, []byte{})
	require.NoError(t, err)
	err = idx.Add(ctx, nd)
	require.NoError(t, err)

	data, err := idx.Data(ctx)
	require.NoError(t, err)

	expected := "42\t2025-01-02T15:04:05Z\t2024-06-01T10:00:00Z\t2025-01-03T08:00:00Z\tTest Title\n"
	require.Equal(t, expected, string(data))
}

func TestNodeIndex_DataRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	input := "42\t2025-01-02T15:04:05Z\t2024-06-01T10:00:00Z\t2025-01-03T08:00:00Z\tMy Title\n" +
		"100\t2025-02-01T00:00:00Z\t2025-01-01T00:00:00Z\t2025-02-02T00:00:00Z\tAnother\n"

	idx, err := keg.ParseNodeIndex(ctx, []byte(input))
	require.NoError(t, err)

	data, err := idx.Data(ctx)
	require.NoError(t, err)
	require.Equal(t, input, string(data))
}

func TestNodeIndex_DataZeroTimestampsOmitted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Legacy 3-col parse: created/accessed will be zero
	input := "42\t2025-01-02T15:04:05Z\tMy Title\n"
	idx, err := keg.ParseNodeIndex(ctx, []byte(input))
	require.NoError(t, err)

	data, err := idx.Data(ctx)
	require.NoError(t, err)

	// Should emit 5-col with empty created/accessed columns
	expected := "42\t2025-01-02T15:04:05Z\t\t\tMy Title\n"
	require.Equal(t, expected, string(data))
}
