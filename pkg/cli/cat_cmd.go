package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/app"
	"github.com/spf13/cobra"
)

// NewCatCmd returns the `cat` cobra command.
//
// Usage examples:
//
//	tap cat 0
//	tap cat 42
//	tap cat 0 --alias myalias
func NewCatCmd() *cobra.Command {
	var opts app.CatOptions

	cmd := &cobra.Command{
		Use:     "cat NODE_ID",
		Short:   "display a node's content with metadata as frontmatter",
		Aliases: []string{"show"},
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Set the node ID from the first argument
			opts.NodeID = args[0]

			ctx := cmd.Context()
			r, err := app.NewRunnerFromWd(ctx)
			if err != nil {
				return err
			}

			output, err := r.Cat(ctx, opts)
			if err != nil {
				return err
			}

			fmt.Fprint(cmd.OutOrStdout(), output)
			return nil
		},
	}

	cmd.Flags().StringVar(&opts.Alias, "alias", "", "alias of the keg to read from")

	return cmd
}
