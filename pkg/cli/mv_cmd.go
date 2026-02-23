package cli

import (
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewMoveCmd(deps *Deps) *cobra.Command {
	var opts tapper.MoveOptions

	cmd := &cobra.Command{
		Use:     "mv SRC_NODE_ID DST_NODE_ID",
		Short:   "move a node to a new id and rewrite inbound links",
		Aliases: []string{"move"},
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.SourceID = args[0]
			opts.DestID = args[1]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			return deps.Tap.Move(cmd.Context(), opts)
		},
	}

	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to move the node in")
	return cmd
}
