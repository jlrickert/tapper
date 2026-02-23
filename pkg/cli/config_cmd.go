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
//	tap config --keg myalias
//	tap config edit
//	tap config edit --keg myalias
func NewConfigCmd(deps *Deps) *cobra.Command {
	var opts tapper.InfoOptions

	cmd := &cobra.Command{
		Use:   "config",
		Short: "display keg configuration",
		Long: `Display the keg configuration (keg file contents).

Shows metadata about the keg including title, creator, entities, tags, and
other configuration properties. Use 'tap config edit' to modify the keg
configuration.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			ctx := cmd.Context()
			output, err := deps.Tap.Info(ctx, opts)
			if err != nil {
				return err
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), output)
			return err
		},
	}

	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to display configuration for")

	// Add the edit subcommand for keg config editing.
	cmd.AddCommand(NewInfoEditCmd(deps))

	return cmd
}
