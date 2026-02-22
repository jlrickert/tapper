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
		Short: "list all notes",
		Long:  `list all notes. -f "%i %d %t" is the default`,

		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, args []string) error {
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
	cmd.Flags().StringVarP(&opts.Format, "format", "f", "", "output format")
	cmd.Flags().StringVarP(&opts.Keg, "keg", "k", "", "keg alias for which note to show")

	_ = cmd.RegisterFlagCompletionFunc("keg", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		kegs, _ := deps.Tap.ListKegs(true)
		return kegs, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}
