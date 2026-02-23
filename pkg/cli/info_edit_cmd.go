package cli

import (
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewInfoEditCmd returns the `config edit` cobra subcommand.
//
// Usage examples:
//
//	tap config edit
//	tap config edit --keg myalias
func NewInfoEditCmd(deps *Deps) *cobra.Command {
	var opts tapper.InfoEditOptions

	cmd := &cobra.Command{
		Use:   "edit",
		Short: "edit keg metadata with default editor",
		Long: `Open the keg configuration file (keg.yaml) in your default editor for editing.

The editor is determined by the EDITOR environment variable, defaulting to 'vim'.
If stdin is piped, that content is used as the initial editable draft.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			opts.Stream = deps.Runtime.Stream()
			ctx := cmd.Context()
			return deps.Tap.InfoEdit(ctx, opts)
		},
	}

	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to edit configuration for")

	return cmd
}
