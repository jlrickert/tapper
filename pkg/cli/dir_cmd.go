package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewPwdCmd(deps *Deps) *cobra.Command {
	var opts tapper.DirOptions

	cmd := &cobra.Command{
		Use:   "dir [NODE_ID]",
		Short: "print keg directory or node directory path",
		Long: `Print a filesystem path for the resolved keg.

With no NODE_ID, prints the keg root directory.
With NODE_ID, prints the node directory (<keg>/<node_id>) for local file-backed kegs.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.NodeID = args[0]
			}
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			dir, err := deps.Tap.Dir(cmd.Context(), opts)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), dir)
			return err
		},
	}

	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to read from")
	return cmd
}
