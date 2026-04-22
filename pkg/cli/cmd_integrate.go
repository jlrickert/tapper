package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// NewIntegrateCmd builds the `tap integrate` command. It installs the
// embedded rendered tree for the requested host into that host's
// standard filesystem location. --dry-run lets callers inspect the
// target paths without touching the filesystem.
func NewIntegrateCmd(deps *Deps) *cobra.Command {
	var opts tapper.IntegrateOptions

	cmd := &cobra.Command{
		Use:   "integrate HOST",
		Short: "install the rendered tapper integration for HOST into its install path",
		Long: `Install the tapper integration tree shipped inside the binary
for the specified host (claude, codex). Files are copied to the host's
standard install path under $HOME; pass --target to override.

With --dry-run, nothing is written — the command prints the target
paths it would create so the caller can review them first.`,
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
			targets, err := deps.Tap.Integrate(cmd.Context(), opts)
			if err != nil {
				return err
			}
			prefix := "Wrote:"
			if opts.DryRun {
				prefix = "Would write:"
			}
			out := cmd.OutOrStdout()
			if _, err := fmt.Fprintln(out, prefix); err != nil {
				return err
			}
			for _, p := range targets {
				if _, err := fmt.Fprintln(out, "  "+p); err != nil {
					return err
				}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print target paths without writing any files")
	cmd.Flags().StringVar(&opts.Target, "target", "", "override the default install directory")

	// --target is a directory path; ShellCompDirectiveFilterDirs asks
	// the shell to only offer directories, not files.
	mustRegisterFlagCompletion(cmd, "target", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveFilterDirs
	})

	return cmd
}
