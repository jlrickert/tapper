package cli

import (
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewMoveCmd(deps *Deps) *cobra.Command {
	var opts tapper.MoveOptions

	cmd := &cobra.Command{
		Use:   "mv SRC_NODE_ID DST_NODE_ID",
		Short: "move a node to a new id and rewrite inbound links",
		Long: `Rename a node from SRC_NODE_ID to DST_NODE_ID.

All ../SRC references in other nodes are rewritten to ../DST. The
destination must not already exist. Node 0 cannot be moved.`,
		Aliases: []string{"move"},
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: nodeIDCompletionFunc(deps, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.SourceID = args[0]
			opts.DestID = args[1]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			return deps.Tap.Move(cmd.Context(), opts)
		},
	}
	return cmd
}
