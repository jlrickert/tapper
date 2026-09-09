package keg

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestNodeStatsPortableIdentityRoundTrip(t *testing.T) {
	const raw = `{"uuid":"cbb0e98c-29e8-4d73-8c85-0f60cd82c033","creator":"someone","creator_assumed":true,"access_count":42,"hash":"unchanged"}`
	stats, err := ParseStats(context.Background(), []byte(raw))
	require.NoError(t, err)
	require.Equal(t, "someone", stats.Creator())
	stats.SetUpdated(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	data, err := stats.ToJSON()
	require.NoError(t, err)
	require.NotContains(t, string(data), "creator_assumed")
	got, err := ParseStats(context.Background(), data)
	require.NoError(t, err)
	require.Equal(t, stats.UUID(), got.UUID())
	require.Equal(t, stats.Creator(), got.Creator())
	require.Equal(t, 42, got.AccessCount())
	require.Equal(t, "unchanged", got.Hash())
	legacy, err := ParseStats(context.Background(), []byte(`{"title":"Legacy"}`))
	require.NoError(t, err)
	require.Empty(t, legacy.UUID())
	require.Empty(t, legacy.Creator())
}
