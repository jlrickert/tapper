package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewLinksCmd(deps *Deps) *cobra.Command {
	var opts tapper.LinksOptions

	cmd := &cobra.Command{
		Use:   "links NODE_ID [NODE_ID...]",
		Short: "list outgoing links from one or more nodes",
		Long: `List nodes that the given NODE_IDs link to. When multiple IDs are
provided, results are merged and deduplicated.

Format placeholders: %i (node id), %d (date), %t (title), %% (literal %).
Default format: "%i %d %t".`,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: nodeIDCompletionFunc(deps, 0),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeIDs = args
			applyKegTargetProfile(deps, &opts.KegTargetOptions)

			nodes, err := deps.Tap.Links(cmd.Context(), opts)
			if err != nil {
				return err
			}
			for _, node := range nodes {
				fmt.Fprintln(cmd.OutOrStdout(), node)
			}
			return nil
		},
	}

	cmd.Flags().BoolVarP(&opts.IdOnly, "id-only", "", false, "show only ids")
	cmd.Flags().BoolVar(&opts.Reverse, "reverse", false, "list nodes in reverse order")
	cmd.Flags().IntVarP(&opts.Limit, "limit", "n", 0, "maximum number of results (0 for no limit)")
	cmd.Flags().IntVar(&opts.Offset, "offset", 0, "skip the first N results")
	cmd.Flags().StringVarP(&opts.Format, "format", "f", "", "output format")

	return cmd
}
