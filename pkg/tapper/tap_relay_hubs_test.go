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

func TestRelayModelMetadataAndVariantsParse(t *testing.T) {
	cfg, err := ParseConfig([]byte(`relay:
  providers:
    ollama:
      kind: ollama
      models:
        metadata:
          "gpt-oss*": {reasoning: effort}
          llama3:8b: {contextWindow: 8192, maxContextWindow: 131072}
        variants:
          qwen3.6:35b-256k: {from: "qwen3.6:35b", contextWindow: 262144, reasoning: toggle}
`))
	require.NoError(t, err)
	models := cfg.Relay().Providers["ollama"].Models
	require.Equal(t, RelayModelMeta{Reasoning: "effort"}, models.Metadata["gpt-oss*"])
	require.Equal(t, RelayModelMeta{ContextWindow: 8192, MaxContextWindow: 131072}, models.Metadata["llama3:8b"])
	require.Equal(t, RelayVariant{From: "qwen3.6:35b", RelayModelMeta: RelayModelMeta{ContextWindow: 262144, Reasoning: "toggle"}}, models.Variants["qwen3.6:35b-256k"])

	variants := relayVariants(models.Variants)
	require.Equal(t, 262144, variants["qwen3.6:35b-256k"].ContextWindow)
	require.Equal(t, "qwen3.6:35b", variants["qwen3.6:35b-256k"].From)
}

func TestRelayToolServersFromConfig(t *testing.T) {
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := NewTap(TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	cfg, err := ParseConfig([]byte(`relay:
  mcp:
    everything:
      command: npx
      args: ["-y", "@modelcontextprotocol/server-everything"]
      envFrom: [GITHUB_TOKEN]
      timeout: 30s
    web:
      url: http://127.0.0.1:3000/mcp
      headersFromEnv: {Authorization: MCP_AUTH}
      tools: {deny: ["delete_*"]}
    off:
      enabled: false
      command: nope
`))
	require.NoError(t, err)
	rc := cfg.Relay()
	require.Equal(t, []string{"delete_*"}, rc.MCP["web"].Tools.Deny)

	_, err = tap.relayToolServers(rc)
	require.ErrorContains(t, err, "MCP_AUTH", "a named header variable must be set")

	require.NoError(t, sb.Runtime().Set("MCP_AUTH", "Bearer x"))
	servers, err := tap.relayToolServers(rc)
	require.NoError(t, err)
	require.Len(t, servers, 2, "a disabled server is left out")
	require.Equal(t, "everything", servers[0].Name())
	require.Equal(t, "web", servers[1].Name())

	bad := &RelayConfig{MCP: map[string]RelayMCPServer{"s": {Command: "x", Timeout: "soon"}}}
	_, err = tap.relayToolServers(bad)
	require.ErrorContains(t, err, "timeout")

	both := &RelayConfig{MCP: map[string]RelayMCPServer{"s": {Command: "x", URL: "http://h"}}}
	_, err = tap.relayToolServers(both)
	require.ErrorContains(t, err, "both command and url")
}
