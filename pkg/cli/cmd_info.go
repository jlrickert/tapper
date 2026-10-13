package cli

import (
	"encoding/json"
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// NewInfoCmd returns the `info` cobra command.
//
// Usage examples:
//
//	tap keg info
//	tap keg info --keg myalias
func NewInfoCmd(deps *Deps) *cobra.Command {
	var opts tapper.InfoOptions

	cmd := &cobra.Command{
		Use:   "info",
		Short: "display concise information about the active keg",
		Long: `Display diagnostic information about the resolved keg.

Includes the canonical reference, active flight, summary, node count, and
file/image capabilities. Pass --debug for path and backend diagnostics.`,
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

	cmd.Flags().BoolVar(&opts.JSON, "json", false, "render diagnostics as JSON instead of YAML")
	cmd.Flags().BoolVar(&opts.Debug, "debug", false, "include working-directory and backend resolution diagnostics")

	return cmd
}

// NewContextInfoCmd returns the top-level `info` command: what the CLI is
// pointed at. `tap keg info` describes one KEG instead.
//
// Usage examples:
//
//	tap info
//	tap info --json
//	tap info --flight @me/+work
func NewContextInfoCmd(deps *Deps) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "info",
		Short: "show the hub, default keg, flight, and the flight's kegs",
		Long: `Show the CLI's current context: the hub and the user signed in to it, the
default keg, the active flight, and the kegs that flight covers with your role
in each. Each default notes the config tier that set it. A hub that cannot be
reached is reported as a warning; the local defaults still print.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var kt tapper.KegTargetOptions
			applyKegTargetProfile(deps, &kt)
			info, err := deps.Tap.ContextInfo(cmd.Context(), kt)
			if err != nil {
				return err
			}
			var out []byte
			if asJSON {
				out, err = json.MarshalIndent(info, "", "  ")
				out = append(out, '\n')
			} else {
				out, err = yaml.Marshal(info)
			}
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(out)
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "render as JSON instead of YAML")
	return cmd
}
