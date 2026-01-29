package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func NewRepoCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repo",
		Short: "manage keg repositories",
	}

	cmd.AddCommand(NewRepoKegListCmd(deps))

	return cmd
}

func NewRepoKegListCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "list all available kegs",
		Aliases: []string{"ls"},
		RunE: func(cmd *cobra.Command, args []string) error {
			kegs, err := deps.Tap.ListKegs(cmd.Context(), true)
			if err != nil {
				return err
			}
			if len(kegs) == 0 {
				return fmt.Errorf("no kegs found")
			}
			output := strings.Join(kegs, " ")
			_, err = fmt.Fprint(cmd.OutOrStdout(), output)
			return err
		},
	}
	return cmd
}
