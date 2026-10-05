package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jlrickert/tapper/pkg/relaycontract"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// NewRelayCmd builds the `tap relay` command, which contributes this
// machine's model providers to Hub's catalog and serves the inference Hub
// routes to them.
func NewRelayCmd(deps *Deps) *cobra.Command {
	var opts tapper.RelayOptions

	cmd := &cobra.Command{
		Use:   "relay",
		Short: "offer this machine's models and tools to Hub (experimental)",
		Long: `Connect to Hub and offer the models of your configured providers to your
account's model catalog, and the tools of your configured MCP servers and of
this machine's coding agents to Hub. The relay dials out to Hub and stays connected, so no inbound port is
needed. Provider credentials and MCP server settings stay on this machine.

Providers and MCP servers are configured in your user config only; project
config cannot set them:

  relay:
    enabled: true           # optional; false turns the relay off
    name: workstation       # optional; defaults to the hostname
    hubs: [work, personal]  # optional; serve these configured hubs at once
    providers:
      ollama:
        kind: ollama        # baseUrl defaults to http://127.0.0.1:11434/v1
        maxConcurrent: 2    # optional; this provider's in-flight limit (default 4)
        models:
          allow: ["qwen3*"]
      openrouter:
        kind: openrouter
        auth: apiKey
        apiKeyEnv: OPENROUTER_API_KEY   # the variable's name, never the key
        maxConcurrent: 16
    mcp:
      everything:           # a stdio server the relay starts
        command: npx
        args: ["-y", "@modelcontextprotocol/server-everything"]
        envFrom: [GITHUB_TOKEN]   # passed through; others are not
        timeout: 2m         # optional; longest one call may run
      notes:                # a streamable HTTP server already running
        url: http://127.0.0.1:3000/mcp
        headersFromEnv: {Authorization: NOTES_MCP_AUTH}
        tools:
          deny: ["delete_*"]
    runners:                # optional; the built-in servers below, all off
      claude: {run: true, tools: true}  # "claude" and "claude-tools"
      codex: {run: true}    # "codex"; likewise opencode and pi
      roots: [~/src]        # where delegated tasks may work (default ~)
      timeout: 30m          # longest one delegated task may run
      maxConcurrent: 2      # delegated tasks running at once, per server

Without relay.hubs the relay serves the one hub 'tap auth login' would use;
--hub narrows it to that hub even when relay.hubs is set. With several hubs,
every hub sees every provider. Each provider's maxConcurrent (default 4) is
its limit across all of them: a hub that asks while that provider is full
hears "overloaded", and the relay's other providers are unaffected.

MCP servers are forwarded only to hubs that support relayed tools; others get
the models alone. Hub can call a tool only by the server and tool names the
relay listed; a stdio server gets PATH, HOME, locale settings and the variables
in envFrom, not the relay's whole environment. A server that exits is
restarted.

Relayed tools reach Hub's chat, Hub's /mcp/relay endpoint, and every 'tap mcp'
(the Claude Code plugin's server included). Without an agent a caller gets all
of their own relayed tools plus those shared with them; with an agent
(TAP_AGENT for 'tap mcp') they are gated by that agent's relay:tools.
TAP_RELAY_TOOLS=off keeps a 'tap mcp' from offering them.

Built-in servers are off until relay.runners turns them on, and each is
offered only when its program is on PATH. "claude", "codex", "opencode" and
"pi" (relay.runners.NAME.run) each run 'tap runner serve --runner NAME', one
tool (claude_code_run, codex_run, opencode_run, pi_run) that runs a task in a
directory under relay.runners.roots. "claude-tools"
(relay.runners.claude.tools) runs 'claude mcp serve'. Each agent keeps its own
permission settings. Who may call them is decided on Hub, where each server is
enabled and shared on its own. A relay.mcp entry with the same name replaces a
built-in, or removes it with enabled: false.

The relay authenticates to each hub with that hub's token from 'tap auth login'.
It runs in the foreground until interrupted, reconnecting if a connection drops.
Disconnecting it from a hub's Account → Relay page stops it for that hub for
good; the other hubs keep being served. Run it again to reconnect.

Experimental and unstable: expect this to change.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts.Hub = deps.KegTargetOptions.Hub
			opts.Version = Version
			out := cmd.ErrOrStderr()
			opts.OnRegistered = func(hubURL string, reg relaycontract.Registered) {
				fmt.Fprintf(out, "relay connected to %s (protocol %d) with %d model(s)\n", hubURL, reg.Protocol, len(reg.Models))
				for _, m := range reg.Models {
					fmt.Fprintf(out, "  %s\n", m.CatalogID)
				}
			}
			return deps.Tap.Relay(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.Name, "name", "", "relay name shown in Hub (overrides relay.name)")
	return cmd
}
