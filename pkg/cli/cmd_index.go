package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewIndexCmd returns the `index` cobra command group.
//
// Usage examples:
//
//	tap index list
//	tap index get changes.md
//	tap index get -k ecw nodes.tsv
//	tap index rebuild
//	tap index rebuild --full
func NewIndexCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "index",
		Short: "manage keg indexes",
		Long:  `List, inspect, or rebuild index files for a keg.`,
	}

	cmd.AddCommand(
		newIndexListCmd(deps),
		newIndexGetCmd(deps),
		newIndexRebuildCmd(deps),
	)

	return cmd
}

// newIndexListCmd returns the `index list` subcommand.
func newIndexListCmd(deps *Deps) *cobra.Command {
	var opts tapper.IndexCatOptions

	cmd := &cobra.Command{
		Use:   "list",
		Short: "list available index files",
		Long:  `List all available index files for a keg (e.g. nodes.tsv, tags, links).`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			ctx := cmd.Context()

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

	return cmd
}

// newIndexGetCmd returns the `index get` subcommand.
func newIndexGetCmd(deps *Deps) *cobra.Command {
	var opts tapper.IndexCatOptions

	cmd := &cobra.Command{
		Use:   "get INDEX",
		Short: "dump a named index",
		Long: `Print the contents of a named index file.

INDEX is the index file name, e.g. "changes.md", "nodes.tsv", or "tags".`,
		Args: cobra.ExactArgs(1),
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

			opts.Name = args[0]
			output, err := deps.Tap.IndexCat(ctx, opts)
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), output)
			return nil
		},
	}

	return cmd
}

// newIndexRebuildCmd returns the `index rebuild` subcommand.
func newIndexRebuildCmd(deps *Deps) *cobra.Command {
	var opts tapper.IndexOptions

	cmd := &cobra.Command{
		Use:   "rebuild",
		Short: "rebuild indices for a keg",
		Long: `Rebuild indices for a keg (nodes.tsv, tags, links, backlinks, changes.md).

By default this runs incremental indexing using the keg config timestamp.
Use --full to scan all nodes and regenerate the full dex.`,
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
	cmd.Flags().BoolVarP(&opts.Rebuild, "full", "f", false, "full rebuild from scratch (scan all nodes and regenerate dex)")

	return cmd
}
