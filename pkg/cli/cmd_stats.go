package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewStatsCmd returns the `stats` cobra command.
func NewStatsCmd(deps *Deps) *cobra.Command {
	var opts tapper.StatsOptions

	cmd := &cobra.Command{
		Use:   "stats NODE_ID",
		Short: "display node stats",
		Long: `Display programmatic stats (stats.json) for a node.

Stats include title, lead, content hash, timestamps (created, updated,
accessed), links, and access count.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: nodeIDCompletionFunc(deps, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)

			output, err := deps.Tap.Stats(cmd.Context(), opts)
			if err != nil {
				return err
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), output)
			return err
		},
	}

	return cmd
}
