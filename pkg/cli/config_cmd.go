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
//	tap config --edit
//	tap config --edit --keg myalias
func NewConfigCmd(deps *Deps) *cobra.Command {
	var opts tapper.InfoOptions
	var edit bool

	cmd := &cobra.Command{
		Use:   "config",
		Short: "display keg configuration",
		Long: `Display the keg configuration (keg file contents).

Shows metadata about the keg including title, creator, entities, tags, and
other configuration properties. Use '--edit' to modify the keg
configuration.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			ctx := cmd.Context()

			if edit {
				return deps.Tap.InfoEdit(ctx, tapper.InfoEditOptions{
					KegTargetOptions: opts.KegTargetOptions,
					Stream:           deps.Runtime.Stream(),
				})
			}

			output, err := deps.Tap.Info(ctx, opts)
			if err != nil {
				return err
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), output)
			return err
		},
	}

	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to display configuration for")
	cmd.Flags().BoolVar(&edit, "edit", false, "edit keg configuration with default editor")

	return cmd
}
