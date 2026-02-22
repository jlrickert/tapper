package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewIndexCmd returns the `index` cobra command.
//
// Usage examples:
//
//	Tap index
//	Tap index --alias myalias
func NewIndexCmd(deps *Deps) *cobra.Command {
	var opts tapper.IndexOptions

	cmd := &cobra.Command{
		Use:   "index",
		Short: "rebuild indices for a keg",
		Long: `Rebuild all indices for a keg (nodes.tsv, tags, links, backlinks).

This command scans all nodes and regenerates the dex indices. Useful after
manually modifying files or to refresh stale indices.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			output, err := deps.Tap.Index(ctx, opts)
			if err != nil {
				return err
			}

			fmt.Fprint(cmd.OutOrStdout(), output)
			return nil
		},
	}

	cmd.Flags().StringVarP(&opts.KegAlias, "keg", "k", "", "alias of the keg to index")
	// Backward-compatible alias flag.
	cmd.Flags().StringVar(&opts.KegAlias, "alias", "", "alias of the keg to index (deprecated; use --keg)")
	cmd.Flags().BoolVarP(&opts.Rebuild, "rebuild", "r", false, "rebuild all indices (nodes.tsv, tags, links, backlinks)")

	_ = cmd.RegisterFlagCompletionFunc("keg", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		kegs, _ := deps.Tap.ListKegs(true)
		return kegs, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("alias", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		kegs, _ := deps.Tap.ListKegs(true)
		return kegs, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}
