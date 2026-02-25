package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewReindexCmd returns the `reindex` cobra command.
func NewReindexCmd(deps *Deps) *cobra.Command {
	var opts tapper.IndexOptions

	cmd := &cobra.Command{
		Use:   "reindex",
		Short: "rebuild indices for a keg",
		Long: `Rebuild indices for a keg (nodes.tsv, tags, links, backlinks, changes.md).

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

	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to reindex")
	if deps.Profile.withDefaults().AllowKegAliasFlags {
		cmd.Flags().StringVar(&opts.Keg, "alias", "", "alias of the keg to reindex (deprecated; use --keg)")
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
