package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewRemoveCmd(deps *Deps) *cobra.Command {
	var opts tapper.RemoveOptions

	cmd := &cobra.Command{
		Use:   "rm [NODE_ID...]",
		Short: "remove nodes from the keg",
		Long: `Remove one or more nodes and update the index.

Nodes can be specified as positional arguments or selected via --query.
Inbound links from other nodes are cleaned up.`,
		Aliases: []string{"remove"},
		ValidArgsFunction: nodeIDCompletionFunc(deps, 0),
		Args: func(cmd *cobra.Command, args []string) error {
			if opts.Query != "" {
				return nil
			}
			if len(args) == 0 {
				return fmt.Errorf("accepts at least 1 arg(s), received 0")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeIDs = args
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			return deps.Tap.Remove(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.Query, "query", "", `boolean expression (see "tap docs query-expressions" for syntax)`)

	return cmd
}
