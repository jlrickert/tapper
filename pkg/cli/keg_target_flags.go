package cli

import (
	"context"
	"sort"
	"strings"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func applyKegTargetProfile(deps *Deps, opts *tapper.KegTargetOptions) {
	if opts.Keg == "" {
		opts.Keg = deps.KegTargetOptions.Keg
	}
	if opts.Namespace == "" {
		opts.Namespace = deps.KegTargetOptions.Namespace
	}
	if opts.Hub == "" {
		opts.Hub = deps.KegTargetOptions.Hub
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
	if opts.Flight == "" {
		opts.Flight = deps.KegTargetOptions.Flight
	}
	// Direct CLI commands use normal keg auth and keep Flight only as context
	// for surfaces such as orient. MCP receives deps.KegTargetOptions directly.
	opts.BypassFlightRestrictions = true
	profile := deps.Profile.withDefaults()
	if profile.ForceProjectResolution {
		opts.Project = true
	}
	// The full `tap` surface requires `tap bootstrap` before config-driven keg
	// resolution; the pruned `keg` binary (no config command) stays exempt and
	// resolves project-local kegs without setup.
	if profile.IncludeConfigCommand {
		opts.RequireBootstrap = true
	}
}

// globalKegTarget returns the keg selector (Keg/Namespace/Hub and profile
// defaults) resolved from the persistent global flags. Admin commands use it to
// target a keg through --keg/--namespace/--hub instead of a positional.
func globalKegTarget(deps *Deps) tapper.KegTargetOptions {
	var kt tapper.KegTargetOptions
	applyKegTargetProfile(deps, &kt)
	return kt
}

// namespaceFlagCompletionFunc completes --namespace from namespaces named in
// local config (offline, best-effort).
func namespaceFlagCompletionFunc(deps *Deps) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return filterByPrefix(configNamespaceNames(deps), toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// hubFlagCompletionFunc completes --hub from the hubs named in local config.
func hubFlagCompletionFunc(deps *Deps) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return filterByPrefix(configHubNames(deps), toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// configHubNames returns the hub names known to local config (best-effort,
// offline), sorted for stable completion output.
func configHubNames(deps *Deps) []string {
	tap, err := completionTap(deps)
	if err != nil {
		return nil
	}
	cfg, err := tap.ConfigService.Config()
	if err != nil || cfg == nil {
		return nil
	}
	var names []string
	for name := range cfg.Hubs() {
		if name = strings.TrimSpace(name); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
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

// nodeAndNameCompletionFunc returns a ValidArgsFunction that suggests node IDs
// for arg 0 and names from nameFn for arg 1. When withDest is true, arg 2
// offers filesystem path completion (for download commands); otherwise no
// completions after arg 1.
func nodeAndNameCompletionFunc(
	deps *Deps,
	nameFn func(ctx context.Context, nodeID string, kegOpts tapper.KegTargetOptions) ([]string, error),
	withDest bool,
) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if deps.Tap == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var kegOpts tapper.KegTargetOptions
		applyKegTargetProfile(deps, &kegOpts)

		switch len(args) {
		case 0:
			ids, err := deps.Tap.List(cmd.Context(), tapper.ListOptions{
				KegTargetOptions: kegOpts,
				IdOnly:           true,
			})
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return filterByPrefix(ids, toComplete), cobra.ShellCompDirectiveNoFileComp
		case 1:
			names, err := nameFn(cmd.Context(), args[0], kegOpts)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return filterByPrefix(names, toComplete), cobra.ShellCompDirectiveNoFileComp
		case 2:
			if withDest {
				return nil, cobra.ShellCompDirectiveDefault
			}
			return nil, cobra.ShellCompDirectiveNoFileComp
		default:
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}
}

// nodeIDWithLocalFileCompletionFunc returns a ValidArgsFunction that suggests
// node IDs for arg 0 and allows default filesystem completion for arg 1.
func nodeIDWithLocalFileCompletionFunc(deps *Deps) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if deps.Tap == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		switch len(args) {
		case 0:
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
		case 1:
			return nil, cobra.ShellCompDirectiveDefault
		default:
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}
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
