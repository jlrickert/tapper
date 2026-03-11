package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewVersionCmd(deps *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", deps.Profile.Use, Version)
			return err
		},
	}
}
