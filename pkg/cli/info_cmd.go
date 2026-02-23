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
//	tap info
//	tap info --keg myalias
func NewInfoCmd(deps *Deps) *cobra.Command {
	var opts tapper.KegInfoOptions

	cmd := &cobra.Command{
		Use:   "info",
		Short: "display keg diagnostics",
		Long: `Display diagnostic information about the resolved keg.

Includes working directory, resolved target details, node counts, and
asset summary data useful for troubleshooting.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			ctx := cmd.Context()
			output, err := deps.Tap.KegInfo(ctx, opts)
			if err != nil {
				return err
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), output)
			return err
		},
	}

	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to inspect")

	return cmd
}
