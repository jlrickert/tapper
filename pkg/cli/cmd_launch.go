package cli

// EXPERIMENTAL — see pkg/tapper/tap_launch.go. Undocumented on purpose; this
// command is a testing scaffold and will be redesigned when agents move to the
// hub.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// NewLaunchCmd builds the `tap launch` command. It resolves a Hub catalog model
// or a configured agent and starts the named harness under the configured root
// flight.
func NewLaunchCmd(deps *Deps) *cobra.Command {
	var opts tapper.LaunchOptions

	cmd := &cobra.Command{
		Use:   "launch HARNESS [-- ARGS...]",
		Short: "start an agent CLI with a Hub or configured model and flight (experimental)",
		Long: `Start opencode, Claude Code, Codex, or pi with a model and the current
Hub-backed flight as a connection-pinned root.

Hub models. --model names a model from your Hub catalog — the models your
connected relays offer (see 'tap relay'). The harness talks to Hub through a
loopback forwarder that lives as long as it does: the forwarder attaches your
Hub credential to each request, refreshing it as needed, so the credential
never reaches the harness and a long session outlives an expiring login.
opencode is the first harness with a Hub mode; its model picker lists your
whole catalog. With neither --model nor an agent configured, the launch uses
Hub mode and starts on your first catalog model:

  tap launch opencode --model laptop/ollama/qwen3:8b

Configured agents. An agent selects only a model:

  agents:
    opus:
      model: anthropic/claude-opus-4
    local:
      model: ollama/qwen3.6:35b

Models are provider-qualified so the launcher knows which protocol the harness
must speak. TAP_AGENT carries model selection and telemetry only.

--agent picks which entry to use. When it is omitted the top-level 'agent' key
is used instead, the same way 'flight' supplies the launch root:

  agent: opus

--model always wins over a configured agent key; --model with --agent is an
error.

The launch root follows normal flight precedence: explicit --flight,
TAP_FLIGHT, project flight, then the user baseline. It is resolved once, must
be Hub-backed, and is exported canonically as TAP_FLIGHT. Governed MCP calls
reload that root's live graph and may select an accessible transitive
descendant; they cannot switch roots.

A flight is optional. With none configured the harness starts under no-flight
identity authority — full access to every KEG the account can already reach,
which is what lets a fresh account launch an agent to create its first flight.
The launcher warns when it does this. Selecting a flight is how you narrow it.

Arguments after -- are passed through to the harness.

Experimental and unstable: expect this to change or disappear.`,
		Args:          cobra.MinimumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return tapper.LaunchHarnesses(), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Harness = args[0]
			opts.Args = args[1:]
			opts.Flight = deps.KegTargetOptions.Flight

			result, err := deps.Tap.Launch(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if !opts.DryRun {
				return nil
			}

			out := cmd.OutOrStdout()
			if result.Source == tapper.LaunchSourceHub {
				if _, err := fmt.Fprintf(out, "hub %s -> %s (via loopback forwarder)\n",
					result.Hub, result.Model); err != nil {
					return err
				}
			} else if _, err := fmt.Fprintf(out, "agent %s -> %s/%s\n",
				result.Agent, result.Provider, result.Model); err != nil {
				return err
			}
			if result.Flight != "" {
				if _, err := fmt.Fprintf(out, "flight: %s (connection-pinned root)\n", result.Flight); err != nil {
					return err
				}
			}
			auth := result.Auth
			if result.KeySource != "" {
				// The variable name, never the key itself.
				auth += " (from $" + result.KeySource + ")"
			}
			if _, err := fmt.Fprintf(out, "auth: %s\n", auth); err != nil {
				return err
			}
			for _, name := range result.StripEnv {
				if _, err := fmt.Fprintf(out, "unset: %s (inherited)\n", name); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(out, "Would run:"); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(out, "  "+strings.Join(result.Argv, " ")); err != nil {
				return err
			}
			if len(result.Env) == 0 {
				return nil
			}
			if _, err := fmt.Fprintln(out, "With environment:"); err != nil {
				return err
			}
			keys := make([]string, 0, len(result.Env))
			for k := range result.Env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if _, err := fmt.Fprintf(out, "  %s=%s\n", k, result.Env[k]); err != nil {
					return err
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&opts.Agent, "agent", "",
		"configured agent alias supplying the model (default: the config's agent key, or TAP_AGENT)")
	cmd.Flags().StringVar(&opts.Model, "model", "",
		"Hub catalog model id to launch with (e.g. laptop/ollama/qwen3:8b)")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print the resolved invocation without starting the harness")
	mustRegisterFlagCompletion(cmd, "agent", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return configAgentNames(deps), cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}

// configAgentNames returns the agent aliases known to local config
// (best-effort, offline), sorted for stable completion output.
func configAgentNames(deps *Deps) []string {
	tap, err := completionTap(deps)
	if err != nil {
		return nil
	}
	cfg, err := tap.ConfigService.Config()
	if err != nil || cfg == nil {
		return nil
	}
	var names []string
	for name := range cfg.Agents() {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
