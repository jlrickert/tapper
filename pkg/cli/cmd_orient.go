package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// NewOrientCmd builds the `tap orient` command. It is the CLI mirror of
// the MCP orient tool: the same payload bytes reach a shell user as
// reach an MCP-speaking agent, because both surfaces call Tap.Orient.
func NewOrientCmd(deps *Deps) *cobra.Command {
	var opts tapper.OrientOptions

	cmd := &cobra.Command{
		Use:   "orient",
		Short: "print the tapper orientation payload",
		Long: `Print the tapper KEG system orientation payload, including
the active KEG, reachable KEGs, active flight context, KEG-level
instructions, and canonical guidance.

This command shares its payload builder with the mcp__tapper__orient
tool so the bytes printed here match the bytes an agent would receive
over MCP.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			payload, err := deps.Tap.Orient(cmd.Context(), opts)
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), payload)
			return err
		},
	}

	// --flight lives on the root persistent flag set (see cmd_root.go)
	// and is picked up automatically by applyKegTargetProfile; orient
	// does not register a command-local copy.

	return cmd
}

// registerHostCompletion wires shell completion for the named flag so
// host-selecting integration commands enumerate the hosts the binary knows
// about. The completion list comes from tapper.IntegrateHosts.
func registerHostCompletion(cmd *cobra.Command, flagName string) {
	mustRegisterFlagCompletion(cmd, flagName, func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return filterByPrefix(tapper.IntegrateHosts(), toComplete), cobra.ShellCompDirectiveNoFileComp
	})
}
