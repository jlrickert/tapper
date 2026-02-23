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
//	tap cat 0
//	tap cat 0 1 2
//	tap cat --tag "fire and not archived"
//	tap cat 0 --keg myalias
func NewCatCmd(deps *Deps) *cobra.Command {
	var opts tapper.CatOptions

	cmd := &cobra.Command{
		Use:     "cat [NODE_ID...]",
		Short:   "display node(s) content with metadata as frontmatter",
		Aliases: []string{"show"},
		Args: func(cmd *cobra.Command, args []string) error {
			if opts.Tag != "" {
				return nil
			}
			if len(args) == 0 {
				return fmt.Errorf("accepts at least 1 arg(s), received 0")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeIDs = args
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
	cmd.Flags().StringVar(&opts.Tag, "tag", "", `tag expression to select nodes (e.g., "fire", "fire and not archived")`)

	return cmd
}
