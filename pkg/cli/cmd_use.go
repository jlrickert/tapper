package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewUseCmd returns the `use` command: record which keg (and flight) a project
// resolves, or a user-wide fallback keg.
//
// Usage examples:
//
//	tap use @work/dev                  # project keg (defaultKeg)
//	tap use @work/dev --flight @work/+plan
//	tap use @me/notes --user           # user-wide fallback (fallbackKeg)
//	tap use --flight @work/+plan       # set/replace just the project flight
//	tap use --clear                    # unset the scope's slot(s)
//	tap use                            # show the resolved keg + flight + fallback
func NewUseCmd(deps *Deps) *cobra.Command {
	var opts tapper.UseOptions

	cmd := &cobra.Command{
		Use:   "use [@namespace/keg]",
		Short: "set the project keg + flight, or the user fallback keg",
		Long: `Record which keg (and flight) the current project resolves, or a
user-wide fallback keg.

Scope picks the keg slot:
  - project (default) writes defaultKeg to .tapper/config.yaml
  - --user writes the user-wide fallbackKeg to ~/.config/tapper/config.yaml

Flight is project-scoped. With no arguments, prints the resolved keg, flight,
and fallback and the config scope that set each.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			// With no positional and no setter flags, show the resolved context.
			if len(args) == 0 && opts.Flight == "" && !opts.Clear {
				var kt tapper.KegTargetOptions
				applyKegTargetProfile(deps, &kt)
				out, err := deps.Tap.UseStatus(ctx, kt)
				if err != nil {
					return err
				}
				_, err = fmt.Fprint(cmd.OutOrStdout(), out)
				return err
			}

			if len(args) == 1 {
				opts.Keg = args[0]
			}
			opts.ConfigPath = deps.ConfigPath
			return deps.Tap.Use(ctx, opts)
		},
	}

	cmd.Flags().StringVar(&opts.Flight, "flight", "", "flight to record for the project (@namespace/+slug)")
	cmd.Flags().BoolVar(&opts.User, "user", false, "write the user-wide fallback keg instead of the project keg")
	cmd.Flags().BoolVar(&opts.Clear, "clear", false, "unset the scope's keg slot (and project flight)")
	mustRegisterFlagCompletion(cmd, "flight", flightFlagCompletionFunc(deps))
	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return kegFlagCompletions(cmd.Context(), deps, toComplete), cobra.ShellCompDirectiveNoFileComp
	}

	return cmd
}
