package cli

// EXPERIMENTAL — see pkg/tapper/tap_launch.go.

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// NewLaunchCmd builds the `tap launch` command. It starts the named harness on
// a model from the caller's Hub catalog under the configured root flight.
func NewLaunchCmd(deps *Deps) *cobra.Command {
	var opts tapper.LaunchOptions

	cmd := &cobra.Command{
		Use:   "launch HARNESS [-- ARGS...]",
		Short: "start an agent CLI on a Hub model under the current flight (experimental)",
		Long: `Start Claude Code, Codex, opencode, or pi on a model from your Hub catalog,
with the current Hub-backed flight as a connection-pinned root.

--model names a catalog model: the models your connected relays offer and
those shared with you (see 'tap relay'). Without it the launch starts on the
first model in your catalog.

  tap launch claude --model laptop/ollama/qwen3:8b
  tap launch codex

Each harness talks to Hub in the protocol it was built for: Claude Code the
Anthropic Messages API, Codex the OpenAI Responses API, opencode and pi OpenAI
chat completions. It reaches Hub through a loopback forwarder that lives as
long as it does. The forwarder attaches your Hub credential to each request,
refreshing it as needed, so the credential never reaches the harness and a
long session outlives an expiring login. opencode and pi list your whole
catalog in their model pickers.

The child gets TAP_HARNESS and TAP_MODEL, which Tapper reports as the
session's identity in telemetry and orientation. They select nothing.

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

Experimental and unstable: expect this to change.`,
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
			return printLaunchPlan(cmd.OutOrStdout(), result)
		},
	}

	cmd.Flags().StringVar(&opts.Model, "model", "",
		"Hub catalog model id to launch with (default: the first in your catalog)")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print the resolved invocation without starting the harness")

	return cmd
}

// printLaunchPlan writes a dry run's report. Placeholders stand in for the
// forwarder's address and key, which exist only once the harness starts.
func printLaunchPlan(out io.Writer, result *tapper.LaunchResult) error {
	var b strings.Builder
	fmt.Fprintf(&b, "hub %s -> %s (via loopback forwarder)\n", result.Hub, result.Model)
	if result.Flight != "" {
		fmt.Fprintf(&b, "flight: %s (connection-pinned root)\n", result.Flight)
	}
	for _, name := range result.StripEnv {
		fmt.Fprintf(&b, "unset: %s (inherited)\n", name)
	}
	b.WriteString("Would run:\n  " + strings.Join(result.Argv, " ") + "\n")
	if len(result.Env) > 0 {
		b.WriteString("With environment:\n")
		keys := make([]string, 0, len(result.Env))
		for k := range result.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "  %s=%s\n", k, result.Env[k])
		}
	}
	names := make([]string, 0, len(result.Files))
	for name := range result.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "Writing <launch dir>/%s:\n", name)
		for _, line := range strings.Split(strings.TrimRight(result.Files[name], "\n"), "\n") {
			b.WriteString("  " + line + "\n")
		}
	}
	_, err := io.WriteString(out, b.String())
	return err
}
