package cli

import (
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewRemoveCmd(deps *Deps) *cobra.Command {
	var opts tapper.RemoveOptions

	cmd := &cobra.Command{
		Use:     "rm NODE_ID",
		Short:   "remove a node",
		Aliases: []string{"remove"},
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeIDs = args
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			return deps.Tap.Remove(cmd.Context(), opts)
		},
	}

	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to remove the node from")
	return cmd
}
