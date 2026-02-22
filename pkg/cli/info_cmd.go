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
//	Tap info
//	Tap info --alias myalias
//	Tap info edit
//	Tap info edit --alias myalias
func NewInfoCmd(deps *Deps) *cobra.Command {
	var opts tapper.InfoOptions

	cmd := &cobra.Command{
		Use:   "info",
		Short: "display keg metadata",
		Long: `Display the keg configuration (keg.yaml).

Shows metadata about the keg including title, creator, state, and other
configuration properties. Use 'Tap info edit' to modify the configuration.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			ctx := cmd.Context()
			output, err := deps.Tap.Info(ctx, opts)
			if err != nil {
				return err
			}

			fmt.Fprint(cmd.OutOrStdout(), output)
			return nil
		},
	}

	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to display info for")

	// Add the edit subcommand
	cmd.AddCommand(NewInfoEditCmd(deps))

	return cmd
}
