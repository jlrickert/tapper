package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewInfoCmd returns the `info` cobra command.
//
// Usage examples:
//
//	tap info
//	tap info --alias myalias
//	tap info edit
//	tap info edit --alias myalias
func NewInfoCmd() *cobra.Command {
	var opts tapper.InfoOptions

	cmd := &cobra.Command{
		Use:   "info",
		Short: "display keg metadata",
		Long: `Display the keg configuration (keg.yaml).

Shows metadata about the keg including title, creator, state, and other
configuration properties. Use 'tap info edit' to modify the configuration.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			tap, err := tapper.NewTap(ctx)
			if err != nil {
				return err
			}

			output, err := tap.Info(ctx, opts)
			if err != nil {
				return err
			}

			fmt.Fprint(cmd.OutOrStdout(), output)
			return nil
		},
	}

	cmd.Flags().StringVar(&opts.Alias, "alias", "", "alias of the keg to display info for")

	// Add the edit subcommand
	cmd.AddCommand(NewInfoEditCmd())

	return cmd
}
