package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewVersionCmd(deps *Deps) *cobra.Command {
	var showLicense bool

	cmd := &cobra.Command{
		Use:   "version",
		Short: "print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if showLicense {
				_, err := fmt.Fprint(cmd.OutOrStdout(), LicenseText)
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", deps.Profile.Use, Version)
			return err
		},
	}

	cmd.Flags().BoolVar(&showLicense, "license", false, "print the full license text")

	return cmd
}
