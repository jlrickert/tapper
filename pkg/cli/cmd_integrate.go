package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// NewIntegrateCmd builds the `tap integrate` command. It installs the
// embedded native marketplace and installs through the requested host CLI.
func NewIntegrateCmd(deps *Deps) *cobra.Command {
	var opts tapper.IntegrateOptions

	cmd := &cobra.Command{
		Use:   "integrate HOST",
		Short: "install native Tapper plugins for HOST from an embedded local marketplace",
		Long: `Extract the host-native Tapper marketplace shipped inside the binary,
register it with Codex or Claude, and install the baseline tapper plugin.
Repeat --plugin to add optional plugins such as tapper-dev. Plugin request
order is preserved and duplicate names are ignored. Scope defaults to user;
Claude also supports project and local. With --dry-run, print extraction paths
and exact host commands without writing files or invoking the host.`,
		Args: cobra.ExactArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return tapper.IntegrateHosts(), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			opts.Host = args[0]
			result, err := deps.Tap.Integrate(cmd.Context(), opts)
			if err != nil {
				return err
			}
			prefix := "Extracted:"
			if opts.DryRun {
				prefix = "Would extract:"
			}
			out := cmd.OutOrStdout()
			if _, err := fmt.Fprintln(out, prefix); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(out, "  "+result.Root); err != nil {
				return err
			}
			for _, p := range result.Paths {
				if _, err := fmt.Fprintln(out, "  "+p); err != nil {
					return err
				}
			}
			if opts.DryRun {
				if _, err := fmt.Fprintln(out, "Would run:"); err != nil {
					return err
				}
				for _, command := range result.Commands {
					if _, err := fmt.Fprintln(out, "  "+strings.Join(command, " ")); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print target paths without writing any files")
	cmd.Flags().StringSliceVar(&opts.Plugins, "plugin", nil, "optional embedded plugin to install (repeatable)")
	cmd.Flags().StringVar(&opts.Scope, "scope", "user", "host install scope: user, project, or local")
	mustRegisterFlagCompletion(cmd, "plugin", func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		plugins, err := tapper.IntegratePlugins(args[0])
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return plugins, cobra.ShellCompDirectiveNoFileComp
	})
	mustRegisterFlagCompletion(cmd, "scope", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"user", "project", "local"}, cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}
