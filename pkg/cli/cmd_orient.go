package cli

import (
	"fmt"
	"strconv"
	"strings"

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
		Short: "print the tapper orientation payload for a host and tier",
		Long: `Print a tapper orientation payload. Tier 0 is bounded (purpose
and rules); tier 1 adds linking and snapshot policy; tier 2 adds the
full canonical body and the rendered host artifact when --host is set.

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

	cmd.Flags().StringVar(&opts.Host, "host", "", "host identifier for host-specific payload (e.g. claude, codex)")
	cmd.Flags().IntVar(&opts.Tier, "tier", 0, "payload depth: 0 (bounded), 1 (linking + snapshot), 2 (full body + host)")

	registerHostCompletion(cmd, "host")
	mustRegisterFlagCompletion(cmd, "tier", tierCompletion)
	// --flight lives on the root persistent flag set (see cmd_root.go)
	// and is picked up automatically by applyKegTargetProfile; orient
	// does not register a command-local copy.

	return cmd
}

// registerHostCompletion wires shell completion for the named flag so
// `tap orient --host <TAB>` and `tap integrate <TAB>` both enumerate
// the hosts the binary knows about. The completion list comes from
// tapper.IntegrateHosts, which intersects the adapter registry with
// the orient-surface map.
func registerHostCompletion(cmd *cobra.Command, flagName string) {
	mustRegisterFlagCompletion(cmd, flagName, func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return filterByPrefix(tapper.IntegrateHosts(), toComplete), cobra.ShellCompDirectiveNoFileComp
	})
}

// tierCompletion suggests the valid --tier values ("0", "1", "2") for
// shell completion. Keeping the string list in sync with the
// tapper.OrientTierMin / OrientTierMax bounds means a future tier
// addition only needs to bump the constants.
func tierCompletion(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	var tiers []string
	for t := tapper.OrientTierMin; t <= tapper.OrientTierMax; t++ {
		s := strconv.Itoa(t)
		if toComplete == "" || strings.HasPrefix(s, toComplete) {
			tiers = append(tiers, s)
		}
	}
	return tiers, cobra.ShellCompDirectiveNoFileComp
}
