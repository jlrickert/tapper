package cli

import (
	"context"
	"strings"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
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

// nodeIDCompletionFunc returns a ValidArgsFunction that suggests node IDs from
// the resolved keg. maxArgs sets the maximum number of positional arguments
// after which no completions are offered (0 means unlimited).
func nodeIDCompletionFunc(deps *Deps, maxArgs int) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if (maxArgs > 0 && len(args) >= maxArgs) || deps.Tap == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var kegOpts tapper.KegTargetOptions
		applyKegTargetProfile(deps, &kegOpts)
		ids, err := deps.Tap.List(cmd.Context(), tapper.ListOptions{
			KegTargetOptions: kegOpts,
			IdOnly:           true,
		})
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return filterByPrefix(ids, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// listKegsFiltered returns keg aliases filtered by toComplete prefix.
func listKegsFiltered(deps *Deps, _ context.Context, toComplete string) []string {
	kegs, _ := deps.Tap.ListKegs(true)
	return filterByPrefix(kegs, toComplete)
}

// filterByPrefix returns items whose lowercase form starts with the lowercase
// prefix. Returns items unchanged when prefix is empty.
func filterByPrefix(items []string, prefix string) []string {
	if prefix == "" {
		return items
	}
	lower := strings.ToLower(prefix)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if strings.HasPrefix(strings.ToLower(item), lower) {
			out = append(out, item)
		}
	}
	return out
}
