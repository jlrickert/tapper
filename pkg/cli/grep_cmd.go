package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewGrepCmd(deps *Deps) *cobra.Command {
	var opts tapper.GrepOptions

	cmd := &cobra.Command{
		Use:   "grep QUERY",
		Short: "search node content by query",
		Long:  "Search node content with a regex and print matching lines grouped by node.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Query = args[0]
			applyKegTargetProfile(deps, &opts.KegTargetOptions)

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

	cmd.Flags().BoolVarP(&opts.IdOnly, "id-only", "", false, "show only ids")
	cmd.Flags().BoolVar(&opts.Reverse, "reverse", false, "list nodes in reverse order")
	cmd.Flags().StringVarP(&opts.Format, "format", "f", "", "output format")
	cmd.Flags().BoolVarP(&opts.IgnoreCase, "ignore-case", "i", false, "perform case-insensitive matching")
	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to read from")

	return cmd
}
