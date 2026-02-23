package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewCatCmd returns the `cat` cobra command.
//
// Usage examples:
//
//	Tap cat 0
//	Tap cat 42
//	Tap cat 0 --keg myalias
func NewCatCmd(deps *Deps) *cobra.Command {
	var opts tapper.CatOptions

	cmd := &cobra.Command{
		Use:     "cat NODE_ID",
		Short:   "display a node's content with metadata as frontmatter",
		Aliases: []string{"show"},
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Set the node ID from the first argument
			opts.NodeID = args[0]
			opts.Stream = deps.Runtime.Stream()
			applyKegTargetProfile(deps, &opts.KegTargetOptions)

			output, err := deps.Tap.Cat(cmd.Context(), opts)
			if err != nil {
				return err
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), output)
			return err
		},
	}

	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to read from")
	cmd.Flags().BoolVar(&opts.ContentOnly, "content-only", false, "display node content only")
	cmd.Flags().BoolVar(&opts.StatsOnly, "stats-only", false, "display node stats only")
	cmd.Flags().BoolVar(&opts.MetaOnly, "meta-only", false, "display node metadata only")
	cmd.Flags().BoolVar(&opts.Edit, "edit", false, "edit node in a temporary file")

	return cmd
}
