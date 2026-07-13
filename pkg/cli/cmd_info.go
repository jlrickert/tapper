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
		Short: "display concise information about the active keg",
		Long: `Display diagnostic information about the resolved keg.

Includes the canonical reference, active flight, summary, node count, and
file/image capabilities. Pass --debug for path and backend diagnostics.`,
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

	cmd.Flags().BoolVar(&opts.JSON, "json", false, "render diagnostics as JSON instead of YAML")
	cmd.Flags().BoolVar(&opts.Debug, "debug", false, "include working-directory and backend resolution diagnostics")

	return cmd
}
