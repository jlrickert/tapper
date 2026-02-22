package cli

import (
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func bindKegTargetFlags(cmd *cobra.Command, deps *Deps, opts *tapper.KegTargetOptions, kegHelp string) {
	profile := deps.Profile.withDefaults()

	if profile.AllowKegAliasFlags {
		cmd.Flags().StringVarP(&opts.Keg, "keg", "k", "", kegHelp)
		_ = cmd.RegisterFlagCompletionFunc("keg", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			kegs, _ := deps.Tap.ListKegs(true)
			return kegs, cobra.ShellCompDirectiveNoFileComp
		})
	}

	if !profile.ForceProjectResolution {
		cmd.Flags().BoolVar(&opts.Project, "project", false, "resolve against the project-local keg")
	} else {
		opts.Project = true
	}
	cmd.Flags().BoolVar(&opts.Cwd, "cwd", false, "with --project, use cwd instead of git root")
	cmd.Flags().StringVar(&opts.Path, "path", "", "explicit project path to resolve a local keg")
}

func applyKegTargetProfile(deps *Deps, opts *tapper.KegTargetOptions) {
	if deps.Profile.withDefaults().ForceProjectResolution {
		opts.Project = true
	}
}
