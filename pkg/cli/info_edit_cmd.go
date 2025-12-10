package cli

import (
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewInfoEditCmd returns the `info edit` cobra subcommand.
//
// Usage examples:
//
//	Tap info edit
//	Tap info edit --alias myalias
func NewInfoEditCmd(deps *Deps) *cobra.Command {
	var opts tapper.InfoEditOptions

	cmd := &cobra.Command{
		Use:   "edit",
		Short: "edit keg metadata with default editor",
		Long: `Open the keg configuration file (keg.yaml) in your default editor for editing.

The editor is determined by the EDITOR environment variable, defaulting to 'vim'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			return deps.Tap.InfoEdit(ctx, opts)
		},
	}

	cmd.Flags().StringVar(&opts.Alias, "alias", "", "alias of the keg to edit info for")

	return cmd
}
