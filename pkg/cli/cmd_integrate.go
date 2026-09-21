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
		Long: `Extract the host-native Tapper plugins shipped inside the binary and
install the baseline tapper plugin for HOST, plus the tapper-guard safety
plugin where the host ships one. Claude and Codex are driven through their own
plugin CLI; opencode has none, so tap merges the MCP server into its
opencode.json and writes the skills itself. Re-running refreshes everything
already installed.

Repeat --plugin to add optional plugins such as tapper-dev. Plugin request
order is preserved and duplicate names are ignored.

tapper-guard carries the PreToolUse guard that denies direct tap and keg CLI
use and Tapper config mutation. --no-safety skips installing it; it does not
remove a guard the host already has. Disable or uninstall that one through the
host, for example: claude plugin disable tapper-guard@tapper-local. opencode
ships no hooks and no guard, so --no-safety does nothing there.

Scope defaults to user. Claude supports user, project, and local; opencode
supports user and project; Codex is user-only because its CLI keeps plugins in
~/.codex/config.toml and has no scope flag.

With --dry-run, print extraction paths and the exact host commands or file
writes without touching anything.`,
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
				if len(result.Commands) > 0 {
					if _, err := fmt.Fprintln(out, "Would run:"); err != nil {
						return err
					}
					for _, command := range result.Commands {
						if _, err := fmt.Fprintln(out, "  "+strings.Join(command, " ")); err != nil {
							return err
						}
					}
				}
				if len(result.Steps) > 0 {
					if _, err := fmt.Fprintln(out, "Would write:"); err != nil {
						return err
					}
					for _, step := range result.Steps {
						if _, err := fmt.Fprintln(out, "  "+step); err != nil {
							return err
						}
					}
				}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print target paths without writing any files")
	cmd.Flags().StringSliceVar(&opts.Plugins, "plugin", nil, "optional embedded plugin to install (repeatable)")
	cmd.Flags().BoolVar(&opts.NoSafety, "no-safety", false, "skip the tapper-guard safety plugin")
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
	// Scopes are per-host: Claude takes all three, opencode has no gitignored
	// tier, and Codex has no scope at all. Suggesting a value the host rejects
	// is worse than suggesting nothing, so the completion asks the host.
	mustRegisterFlagCompletion(cmd, "scope", func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return tapper.IntegrateScopes(args[0]), cobra.ShellCompDirectiveNoFileComp
	})

	return cmd
}
