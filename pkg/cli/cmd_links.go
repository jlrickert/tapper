package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewLinksCmd(deps *Deps) *cobra.Command {
	var opts tapper.LinksOptions

	cmd := &cobra.Command{
		Use:   "links NODE_ID",
		Short: "list outgoing links from a node",
		Long: `List nodes that NODE_ID links to.

Format placeholders: %i (node id), %d (date), %t (title), %% (literal %).
Default format: "%i %d %t".`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: nodeIDCompletionFunc(deps, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
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
	cmd.Flags().StringVarP(&opts.Format, "format", "f", "", "output format")

	return cmd
}
