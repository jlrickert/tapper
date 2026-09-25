package tapper

import (
	"context"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestRelayHubURLs(t *testing.T) {
	cfg, err := ParseConfig([]byte(`hub: work
hubs:
  work: {url: work.example.com}
  personal: {url: "https://personal.example.com/"}
  alias: {url: work.example.com}
relay:
  hubs: [work, personal, alias]
  providers:
    ollama: {kind: ollama}
`))
	require.NoError(t, err)
	rc := cfg.Relay()

	got, err := relayHubURLs(cfg, rc, "")
	require.NoError(t, err)
	require.Equal(t, []string{"https://work.example.com", "https://personal.example.com"}, got,
		"every listed hub, each URL once")

	got, err = relayHubURLs(cfg, rc, "personal")
	require.NoError(t, err)
	require.Equal(t, []string{"https://personal.example.com"}, got, "--hub narrows to one hub")

	got, err = relayHubURLs(cfg, &RelayConfig{}, "")
	require.NoError(t, err)
	require.Equal(t, []string{"https://work.example.com"}, got, "no relay.hubs keeps the selected hub")

	_, err = relayHubURLs(cfg, &RelayConfig{Hubs: []string{"work", "missing"}}, "")
	require.ErrorContains(t, err, `"missing"`)
}

func TestRelayConfigEnabledAndProviderLimitParse(t *testing.T) {
	cfg, err := ParseConfig([]byte("relay:\n  enabled: false\n  providers:\n    ollama: {kind: ollama, maxConcurrent: 3}\n"))
	require.NoError(t, err)
	require.False(t, cfg.Relay().IsEnabled())
	require.Equal(t, 3, cfg.Relay().Providers["ollama"].MaxConcurrent)

	cfg, err = ParseConfig([]byte("relay:\n  name: laptop\n"))
	require.NoError(t, err)
	require.True(t, cfg.Relay().IsEnabled(), "omitted enabled means on")
	require.True(t, (*RelayConfig)(nil).IsEnabled())
}

func TestRelayRefusesToStartWhenDisabled(t *testing.T) {
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	cfg := "hub: atlas\nhubs:\n  atlas: {url: https://hub.example, token: t}\n" +
		"relay:\n  enabled: false\n  providers:\n    ollama: {kind: ollama}\n"
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(cfg), 0o644))
	tap, err := NewTap(TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	require.ErrorIs(t, tap.Relay(context.Background(), RelayOptions{}), ErrRelayDisabled)
}
