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

// NewLaunchCmd builds the `tap launch` command. It resolves a configured agent
// to its model and flight and starts the named harness with that context.
func NewLaunchCmd(deps *Deps) *cobra.Command {
	var opts tapper.LaunchOptions

	cmd := &cobra.Command{
		Use:   "launch HARNESS [-- ARGS...]",
		Short: "start an agent CLI with a configured model and flight (experimental)",
		Long: `Start Claude Code, Codex, or pi with the model and flight named by a
configured agent.

An agent is an alias for a (model, flight) pair:

  agents:
    opus:
      model: anthropic/claude-opus-4
      flight: +dev
    local:
      model: ollama/qwen3.6:35b
      flight: "@me/+scratch"

Models are provider-qualified so the launcher knows which protocol the harness
must speak. The agent name is exported as TAP_AGENT, so a tap mcp session
started inside the harness resolves the agent's flight for itself. Editing the
agent's flight and calling orient again therefore moves a running session,
which exporting the resolved flight would not.

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

			result, err := deps.Tap.Launch(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if !opts.DryRun {
				return nil
			}

			out := cmd.OutOrStdout()
			if _, err := fmt.Fprintf(out, "agent %s -> %s/%s\n",
				result.Agent, result.Provider, result.Model); err != nil {
				return err
			}
			if result.Flight != "" {
				// "resolves to" rather than "is": the child re-resolves this
				// from TAP_AGENT on every orient, so it can change under a
				// running session.
				if _, err := fmt.Fprintf(out, "flight: %s (resolves to, via agent)\n", result.Flight); err != nil {
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

	cmd.Flags().StringVar(&opts.Agent, "agent", "", "configured agent alias supplying the model and flight")
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
