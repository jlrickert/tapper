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

			output, err := deps.Tap.Cat(cmd.Context(), opts)
			if err != nil {
				return err
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), output)
			return err
		},
	}

	cmd.Flags().StringVarP(&opts.Keg, "keg", "k", "", "alias of the keg to read from")

	_ = cmd.RegisterFlagCompletionFunc("keg", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		kegs, _ := deps.Tap.ListKegs(true)
		return kegs, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}
