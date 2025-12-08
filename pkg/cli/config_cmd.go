package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewConfigCmd returns the `config` cobra command.
//
// Usage examples:
//
//	tap config
//	tap config --local
//	tap config edit
//	tap config edit --local
func NewConfigCmd() *cobra.Command {
	var opts tapper.ConfigOptions

	cmd := &cobra.Command{
		Use:   "config",
		Short: "display configuration",
		Long: `Display the merged configuration (user + local).

Use 'tap config edit' to modify the configuration.
Use '--local' flag to view only local project configuration.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			tap, err := tapper.NewTap(ctx)
			if err != nil {
				return err
			}

			output, err := tap.Config(ctx, opts)
			if err != nil {
				return err
			}

			fmt.Fprint(cmd.OutOrStdout(), output)
			return nil
		},
	}

	cmd.Flags().BoolVar(&opts.Local, "local", false, "display local project configuration")

	// Add the edit subcommand
	cmd.AddCommand(NewConfigEditCmd())

	return cmd
}
