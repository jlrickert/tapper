package cli

import (
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewConfigEditCmd returns the `config edit` cobra subcommand.
//
// Usage examples:
//
//	Tap config edit
//	Tap config edit --project
func NewConfigEditCmd(deps *Deps) *cobra.Command {
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

			if deps.ConfigPath != "" {
				opts.ConfigPath = deps.ConfigPath
			}
			return deps.Tap.ConfigEdit(ctx, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Project, "local", false, "edit local project configuration")

	return cmd
}
