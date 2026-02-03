package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewPwdCmd(deps *Deps) *cobra.Command {
	var opts tapper.DirOptions

	cmd := &cobra.Command{
		Use: "dir",
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := deps.Tap.Dir(cmd.Context(), opts)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), dir)
			return err
		},
	}

	cmd.Flags().StringVarP(&opts.Keg, "keg", "k", "", "alias of the keg to read from")

	_ = cmd.RegisterFlagCompletionFunc("keg", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		kegs, _ := deps.Tap.ListKegs(cmd.Context(), true)
		return kegs, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}
