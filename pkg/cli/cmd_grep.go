package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewGrepCmd(deps *Deps) *cobra.Command {
	var opts tapper.GrepOptions
	var idOnly bool

	cmd := &cobra.Command{
		Use:   "grep PATTERN",
		Short: "match node content lines against a regex",
		Long: `Match node content lines in one KEG against a regex and print the
matching lines grouped by node, in node id order. Use it for exact
identifiers, symbols, and audits; use tap node search to find the nodes
about a topic.

With --format or --id-only, matching nodes are listed instead of their
matching lines.

` + formatHelp,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Query = args[0]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			if idOnly {
				opts.Format = tapper.IDOnlyFormat
			}

			nodes, err := deps.Tap.Grep(cmd.Context(), opts)
			if err != nil {
				return err
			}
			for _, node := range nodes {
				fmt.Fprintln(cmd.OutOrStdout(), node)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&idOnly, "id-only", false, "show only ids (shorthand for --format %i)")
	cmd.Flags().BoolVar(&opts.Reverse, "reverse", false, "list nodes in reverse order")
	cmd.Flags().IntVarP(&opts.Limit, "limit", "n", 0, "maximum number of results (0 for no limit)")
	cmd.Flags().IntVar(&opts.Offset, "offset", 0, "skip the first N results")
	cmd.Flags().StringVarP(&opts.Format, "format", "f", "", "output format")
	registerFormatCompletion(cmd)
	cmd.Flags().BoolVarP(&opts.IgnoreCase, "ignore-case", "i", false, "perform case-insensitive matching")
	cmd.Flags().IntVar(&opts.MaxLines, "max-lines", 0, "maximum matched lines per node (0 for unlimited)")

	return cmd
}
