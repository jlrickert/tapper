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
//	tap index
//	tap index changes.md
//	tap index -k ecw nodes.tsv
func NewIndexCmd(deps *Deps) *cobra.Command {
	var opts tapper.IndexCatOptions

	cmd := &cobra.Command{
		Use:   "index [NAME]",
		Short: "list available indexes or dump a named index",
		Long: `List all available index files for a keg.

When NAME is provided (e.g. "changes.md", "nodes.tsv", "tags"), print the
contents of that index file.`,
		Args: cobra.MaximumNArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 || deps.Tap == nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			ctx := cmd.Context()
			indexes, err := deps.Tap.ListIndexes(ctx, opts)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return indexes, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			ctx := cmd.Context()

			if len(args) == 1 {
				opts.Name = args[0]
				output, err := deps.Tap.IndexCat(ctx, opts)
				if err != nil {
					return err
				}
				fmt.Fprint(cmd.OutOrStdout(), output)
				return nil
			}

			indexes, err := deps.Tap.ListIndexes(ctx, opts)
			if err != nil {
				return err
			}
			for _, idx := range indexes {
				fmt.Fprintln(cmd.OutOrStdout(), idx)
			}
			return nil
		},
	}

	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to read from")
	if deps.Profile.withDefaults().AllowKegAliasFlags {
		cmd.Flags().StringVar(&opts.Keg, "alias", "", "alias of the keg (deprecated; use --keg)")
		_ = cmd.RegisterFlagCompletionFunc("alias", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			kegs, _ := deps.Tap.ListKegs(true)
			return kegs, cobra.ShellCompDirectiveNoFileComp
		})
	}

	return cmd
}
