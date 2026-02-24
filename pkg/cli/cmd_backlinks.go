package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewBacklinksCmd(deps *Deps) *cobra.Command {
	var opts tapper.BacklinksOptions

	cmd := &cobra.Command{
		Use:   "backlinks NODE_ID",
		Short: "list nodes that link to a node",
		Long:  `List nodes that link to NODE_ID. -f "%i %d %t" is the default.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)

			nodes, err := deps.Tap.Backlinks(cmd.Context(), opts)
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
	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to read from")

	return cmd
}
