package cli

import (
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewConfigEditCmd returns the `config edit` cobra subcommand.
//
// Usage examples:
//
//	tap config edit
//	tap config edit --local
func NewConfigEditCmd() *cobra.Command {
	var opts tapper.ConfigEditOptions

	cmd := &cobra.Command{
		Use:   "edit",
		Short: "edit configuration with default editor",
		Long: `Open the configuration file in your default editor for editing.

By default, edits the user configuration. Use '--local' to edit the
project-specific local configuration instead.

The editor is determined by the EDITOR environment variable, defaulting to 'vim'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			tap, err := tapper.NewTap(ctx)
			if err != nil {
				return err
			}

			return tap.ConfigEdit(ctx, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Local, "local", false, "edit local project configuration")

	return cmd
}
