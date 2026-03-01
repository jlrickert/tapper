package cli

import (
	"github.com/jlrickert/tapper/pkg/tapper"
)

func applyKegTargetProfile(deps *Deps, opts *tapper.KegTargetOptions) {
	if opts.Keg == "" {
		opts.Keg = deps.KegTargetOptions.Keg
	}
	if !opts.Project {
		opts.Project = deps.KegTargetOptions.Project
	}
	if opts.Path == "" {
		opts.Path = deps.KegTargetOptions.Path
	}
	if !opts.Cwd {
		opts.Cwd = deps.KegTargetOptions.Cwd
	}
	if deps.Profile.withDefaults().ForceProjectResolution {
		opts.Project = true
	}
}
