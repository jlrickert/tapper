package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewRepoRmCmd(deps *Deps) *cobra.Command {
	var opts tapper.RemoveRepoOptions

	cmd := &cobra.Command{
		Use:     "rm ALIAS",
		Short:   "remove a keg alias from the user config",
		Aliases: []string{"remove"},
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Alias = args[0]
			if err := deps.Tap.RemoveRepo(cmd.Context(), opts); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "removed keg %q\n", opts.Alias)
			return err
		},
	}

	cmd.Flags().BoolVar(&opts.Force, "force", false,
		"allow removal of the defaultKeg or fallbackKeg alias")

	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 || deps.Tap == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		kegs, _ := deps.Tap.ListKegs(true)
		return kegs, cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}
