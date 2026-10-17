package tapper

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/internal/relay"
	"github.com/jlrickert/tapper/internal/testapi"
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

// stubRunners puts executable stand-ins for the named CLIs in a fresh
// directory and returns it, for use as PATH.
func stubRunners(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	}
	return dir
}

func builtinNames(cfgs []relay.ToolServerConfig) []string {
	out := make([]string, 0, len(cfgs))
	for _, c := range cfgs {
		out = append(out, c.Name)
	}
	return out
}

func TestRelayBuiltinServers(t *testing.T) {
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := NewTap(TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	orig := relayExecutable
	relayExecutable = func() (string, error) { return "/usr/local/bin/tap", nil }
	t.Cleanup(func() { relayExecutable = orig })

	bin := stubRunners(t, "claude", "codex", "opencode", "pi")
	require.NoError(t, sb.Runtime().Set("PATH", bin))

	all := &RelayRunners{
		Claude:   &RelayClaudeRunner{Run: true, Tools: true},
		Codex:    &RelayRunner{Run: true},
		Opencode: &RelayRunner{Run: true},
		Pi:       &RelayRunner{Run: true},
	}
	cfgs, err := tap.relayBuiltinServers(&RelayConfig{Runners: all})
	require.NoError(t, err)
	require.Equal(t, []string{"claude", "codex", "opencode", "pi", "claude-tools"}, builtinNames(cfgs))
	runner, claudeTools := cfgs[1], cfgs[4]
	require.Equal(t, "Codex", runner.Title)
	require.Equal(t, "/usr/local/bin/tap", runner.Command)
	require.Equal(t, []string{"runner", "serve", "--runner", "codex"}, runner.Args)
	require.Contains(t, runner.Env, "TAP_RUNNER_ROOTS=/home/testuser")
	require.Contains(t, runner.Env, "TAP_RUNNER_TIMEOUT=30m0s")
	require.Contains(t, runner.Env, "PATH="+bin, "runners inherit the relay's environment")
	require.Equal(t, 2, runner.MaxConcurrent)
	require.Equal(t, 31*time.Minute, runner.Timeout, "the relay waits a little past the runner's own timeout")
	require.Equal(t, []string{"runner", "serve", "--runner", "claude"}, cfgs[0].Args)
	require.Equal(t, filepath.Join(bin, "claude"), claudeTools.Command)
	require.Equal(t, []string{"mcp", "serve"}, claudeTools.Args)
	require.Equal(t, "/home/testuser", claudeTools.Dir)
	require.Equal(t, 10*time.Minute, claudeTools.Timeout)

	all.Roots, all.Timeout, all.MaxConcurrent = []string{"~/src", "/work"}, "45m", 3
	cfgs, err = tap.relayBuiltinServers(&RelayConfig{Runners: all})
	require.NoError(t, err)
	require.Contains(t, cfgs[0].Env, "TAP_RUNNER_ROOTS=/home/testuser/src"+string(filepath.ListSeparator)+"/work")
	require.Contains(t, cfgs[0].Env, "TAP_RUNNER_TIMEOUT=45m0s")
	require.Equal(t, 3, cfgs[0].MaxConcurrent)
	require.Equal(t, "/home/testuser/src", cfgs[4].Dir)

	cfg, err := ParseConfig([]byte("relay:\n  mcp:\n    codex: {enabled: false}\n    claude-tools: {enabled: false}\n" +
		"  runners:\n    claude: {run: true, tools: true}\n    codex: {run: true}\n"))
	require.NoError(t, err)
	cfgs, err = tap.relayBuiltinServers(cfg.Relay())
	require.NoError(t, err)
	require.Equal(t, []string{"claude"}, builtinNames(cfgs), "a disabled same-named server removes the built-in")
	servers, err := tap.relayToolServers(cfg.Relay())
	require.NoError(t, err)
	require.Len(t, servers, 1)
	require.Equal(t, "claude", servers[0].Name())

	_, err = tap.relayBuiltinServers(&RelayConfig{Runners: &RelayRunners{Timeout: "soon", Pi: &RelayRunner{Run: true}}})
	require.ErrorContains(t, err, "relay.runners.timeout")

	require.NoError(t, sb.Runtime().Set("PATH", stubRunners(t, "codex")))
	cfgs, err = tap.relayBuiltinServers(&RelayConfig{Runners: all})
	require.NoError(t, err)
	require.Equal(t, []string{"codex"}, builtinNames(cfgs), "a harness must be on PATH")

	require.NoError(t, sb.Runtime().Set("PATH", stubRunners(t)))
	cfgs, err = tap.relayBuiltinServers(&RelayConfig{Runners: all})
	require.NoError(t, err)
	require.Empty(t, cfgs, "no runner on PATH, no built-in")
}

func TestRelayRunnersConfig(t *testing.T) {
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := NewTap(TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	orig := relayExecutable
	relayExecutable = func() (string, error) { return "/usr/local/bin/tap", nil }
	t.Cleanup(func() { relayExecutable = orig })
	require.NoError(t, sb.Runtime().Set("PATH", stubRunners(t, "claude", "codex", "opencode", "pi")))

	builtins := func(t *testing.T, yaml string) []string {
		t.Helper()
		cfg, err := ParseConfig([]byte(yaml))
		require.NoError(t, err)
		cfgs, err := tap.relayBuiltinServers(cfg.Relay())
		require.NoError(t, err)
		return builtinNames(cfgs)
	}

	t.Run("all off by default", func(t *testing.T) {
		require.Empty(t, builtins(t, "relay: {name: laptop}\n"))
		require.Empty(t, builtins(t, "relay: {runners: {roots: [/work]}}\n"))
		require.Empty(t, builtins(t, "relay: {runners: {claude: {}, codex: {}, opencode: {}, pi: {}}}\n"))
	})
	t.Run("run enables only that harness", func(t *testing.T) {
		require.Equal(t, []string{"claude"}, builtins(t, "relay: {runners: {claude: {run: true}}}\n"))
		require.Equal(t, []string{"codex"}, builtins(t, "relay: {runners: {codex: {run: true}}}\n"))
		require.Equal(t, []string{"opencode"}, builtins(t, "relay: {runners: {opencode: {run: true}}}\n"))
		require.Equal(t, []string{"pi"}, builtins(t, "relay: {runners: {pi: {run: true}}}\n"))
	})
	t.Run("claude tools enables claude-tools", func(t *testing.T) {
		require.Equal(t, []string{"claude-tools"}, builtins(t, "relay: {runners: {claude: {tools: true}}}\n"))
	})
	t.Run("tools is claude only", func(t *testing.T) {
		for _, name := range []string{"codex", "opencode", "pi"} {
			cfg, err := ParseConfig([]byte("relay: {runners: {" + name + ": {run: true, tools: true}}}\n"))
			require.NoError(t, err, "the config still loads")
			_, err = tap.relayBuiltinServers(cfg.Relay())
			require.ErrorContains(t, err, "relay.runners."+name+".tools: not supported by this harness")
		}
	})
	t.Run("retired keys do nothing", func(t *testing.T) {
		// Unknown keys are ignored by the decoder (and flagged by the
		// schema), so the retired switches turn nothing on.
		require.Empty(t, builtins(t, "relay: {runners: {enabled: true, claudeTools: true, shareable: true}}\n"))
		cfg, err := ParseConfig([]byte("relay: {mcp: {web: {url: 'http://127.0.0.1:3000/mcp', shareable: true}}}\n"))
		require.NoError(t, err)
		require.Contains(t, cfg.Relay().MCP, "web")
	})
}

func TestRelayRunsWithOnlyBuiltins(t *testing.T) {
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	cfg := "hub: atlas\nhubs:\n  atlas: {url: https://hub.example, token: t}\n"
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(cfg), 0o644))
	tap, err := NewTap(TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().Set("PATH", stubRunners(t)))
	require.ErrorIs(t, tap.Relay(context.Background(), RelayOptions{}), ErrRelayNotConfigured,
		"no providers, no servers and no runners: nothing to relay")
}

// relayTokenWith seeds an expired, refreshable login for a hub whose token
// endpoint answers with status and body (and a rotated pair on 200), then
// resolves the relay's token for that hub.
func relayTokenWith(t *testing.T, status int, body string) (string, error) {
	t.Helper()
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"thub_fresh000000","token_type":"Bearer","expires_in":900,"refresh_token":"rt-next"}`))
	}))
	t.Cleanup(srv.Close)
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	rt := sb.Runtime()
	tap, err := NewTap(TapOptions{Runtime: rt})
	require.NoError(t, err)
	require.NoError(t, rt.AtomicWriteFile(tap.PathService.UserConfig(),
		[]byte(fmt.Sprintf("hub: work\nhubs:\n  work: {url: %s}\n", srv.URL)), 0o644))
	store, err := LoadAuthStore(context.Background(), rt, tap.PathService.AuthStorePath())
	require.NoError(t, err)
	store.Set(CanonicalHubURL(srv.URL), AuthEntry{
		AccessToken: "thub_expired0000", ExpiresAt: rt.Clock().Now().Add(-time.Minute),
		RefreshToken: "rt-old", ClientID: "tapper-cli", TokenEndpoint: srv.URL,
	})
	require.NoError(t, store.Save(context.Background(), rt, tap.PathService.AuthStorePath()))
	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	return tap.relayToken(cfg, srv.URL)
}

// A refresh that fails for a passing reason must not hand the relay the
// expired token: the hub would reject the dial, and the relay treats that
// rejection as final.
func TestRelayTokenRetriesWhenRefreshFails(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"503 server_error":    {http.StatusServiceUnavailable, `{"error":"server_error"}`},
		"400 invalid_request": {http.StatusBadRequest, `{"error":"invalid_request"}`},
		"400 not json":        {http.StatusBadRequest, `bad request`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			token, err := relayTokenWith(t, tc.status, tc.body)
			require.Error(t, err, "a transient refresh failure is an error the relay retries")
			require.Empty(t, token)
		})
	}
}

func TestRelayTokenRefreshesExpiredLogin(t *testing.T) {
	token, err := relayTokenWith(t, http.StatusOK, "")
	require.NoError(t, err)
	require.Equal(t, "thub_fresh000000", token)
}

// A refresh token the hub refused is a lost sign-in, not something to retry:
// the relay dials with what it has and the hub's 401 stops it.
func TestRelayTokenStopsRetryingWhenRefreshIsRejected(t *testing.T) {
	token, err := relayTokenWith(t, http.StatusBadRequest, `{"error":"invalid_grant"}`)
	require.NoError(t, err)
	require.Equal(t, "thub_expired0000", token)
}
