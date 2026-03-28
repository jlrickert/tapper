package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewListCmd(deps *Deps) *cobra.Command {
	opts := tapper.ListOptions{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "list all indexed nodes",
		Long: `List indexed nodes for the resolved keg.

Format placeholders:
  %i  node id
  %d  updated date
  %c  created date
  %a  accessed date
  %t  title
  %%  literal percent

Default format: "%i\t%d\t%t".

Use --query to filter by boolean tag/attribute expressions.
Use --limit (-n) to cap output (0 for no limit).
Use --offset to skip the first N results (for pagination).
Use --sort to order by "id", "updated", "created", or "accessed".`,

		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			nodes, err := deps.Tap.List(cmd.Context(), opts)
			if err != nil {
				return err
			}
			for _, node := range nodes {
				fmt.Fprintln(cmd.OutOrStdout(), node)
			}
			if len(nodes) == 0 {
				return fmt.Errorf("no nodes found")
			}

			return err
		},
	}

	cmd.Flags().BoolVarP(&opts.IdOnly, "id-only", "", false, "show only ids")
	cmd.Flags().BoolVar(&opts.Reverse, "reverse", false, "list nodes in reverse order")
	cmd.Flags().IntVarP(&opts.Limit, "limit", "n", 0, "maximum number of results (0 for no limit)")
	cmd.Flags().IntVar(&opts.Offset, "offset", 0, "skip the first N results")
	cmd.Flags().StringVarP(&opts.Format, "format", "f", "", "output format")
	cmd.Flags().StringVar(&opts.Query, "query", "", `boolean expression (see "tap docs query-expressions" for syntax)`)
	cmd.Flags().StringVar((*string)(&opts.Sort), "sort", "", `sort order: "id", "updated", "created", or "accessed"`)
	mustRegisterFlagCompletion(cmd, "sort", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"id", "updated", "created", "accessed"}, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}
