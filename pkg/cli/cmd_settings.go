package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewSettingsCmd returns the `settings` cobra command.
//
// Usage examples:
//
//	tap settings
//	tap settings --keg myalias
//	tap settings edit
//	tap settings edit --keg myalias
func NewSettingsCmd(deps *Deps) *cobra.Command {
	var opts tapper.InfoOptions

	cmd := &cobra.Command{
		Use:   "settings",
		Short: "display keg configuration",
		Long: `Display the keg configuration (keg file contents).

Shows metadata about the keg including title, creator, entities, tags, and
other configuration properties. Use 'tap settings edit' to modify the keg
configuration.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)

			output, err := deps.Tap.Info(cmd.Context(), opts)
			if err != nil {
				return err
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), output)
			return err
		},
	}
	cmd.AddCommand(NewSettingsEditCmd(deps))

	return cmd
}
