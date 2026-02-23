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
		Short: "update indices for a keg",
		Long: `Update indices for a keg (nodes.tsv, tags, links, backlinks).

By default this runs incremental indexing using the keg config timestamp.
Use --rebuild to scan all nodes and regenerate the full dex.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			ctx := cmd.Context()
			output, err := deps.Tap.Index(ctx, opts)
			if err != nil {
				return err
			}

			fmt.Fprint(cmd.OutOrStdout(), output)
			return nil
		},
	}

	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to index")
	if deps.Profile.withDefaults().AllowKegAliasFlags {
		// Backward-compatible alias flag.
		cmd.Flags().StringVar(&opts.Keg, "alias", "", "alias of the keg to index (deprecated; use --keg)")
	}
	cmd.Flags().BoolVarP(&opts.Rebuild, "rebuild", "r", false, "rebuild all indices from scratch (nodes.tsv, tags, links, backlinks)")

	if deps.Profile.withDefaults().AllowKegAliasFlags {
		_ = cmd.RegisterFlagCompletionFunc("alias", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			kegs, _ := deps.Tap.ListKegs(true)
			return kegs, cobra.ShellCompDirectiveNoFileComp
		})
	}

	return cmd
}
