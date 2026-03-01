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
//	tap config edit --keg myalias
func NewConfigEditCmd(deps *Deps) *cobra.Command {
	var opts tapper.KegConfigEditOptions

	cmd := &cobra.Command{
		Use:   "edit",
		Short: "edit keg configuration with default editor",
		Long: `Open the keg configuration in your default editor for editing.

If stdin is piped with non-empty YAML, the piped content is validated and
written directly instead of opening an editor.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			opts.Stream = deps.Runtime.Stream()
			return deps.Tap.KegConfigEdit(cmd.Context(), opts)
		},
	}

	return cmd
}
