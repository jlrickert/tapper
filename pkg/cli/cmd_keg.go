package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// kegGrantRoleValues are the roles accepted by `tap keg grant`.
var kegGrantRoleValues = []string{"viewer", "editor", "admin"}

var kegVisibilityValues = []string{"public", "private"}

// NewKegCmd returns the `keg` command group: hub-side keg administration
// (listing, creating, ACL grants, visibility) plus the keg's own settings.
//
//	tap keg list
//	tap keg create @ns/blog
//	tap keg grant @ns/blog @alice editor
//	tap keg grants @ns/blog
//	tap keg revoke @ns/blog @alice
//	tap keg visibility @ns/blog public
//	tap keg rename @ns/blog docs
//	tap keg settings
func NewKegCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keg",
		Short: "administer kegs on a hub",
		Long: `Administer kegs on a hub: list and create kegs, manage per-keg
access grants and roles, set visibility, rename aliases, and edit a keg's own
settings.`,
	}
	cmd.AddCommand(
		newKegListCmd(deps),
		newKegGrantsCmd(deps),
		newKegGrantCmd(deps),
		newKegRevokeCmd(deps),
		newKegVisibilityCmd(deps),
		newKegRenameCmd(deps),
		newKegSettingsCmd(deps),
	)
	createCmd := newKegCreateCmd(deps)
	if deps.Profile.withDefaults().AllowKegAliasFlags {
		filterRepoTargetFlagsInHelp(createCmd)
	}
	cmd.AddCommand(createCmd)
	return cmd
}

func newKegRenameCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rename <old> <new>",
		Short: "rename a keg alias within its namespace",
		Long: `Rename a hub-backed keg alias within the same namespace.

The old selector may be bare or @namespace/old. The new alias must be bare;
cross-namespace moves and redirects for old URLs are not created.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			kt := globalKegTarget(deps)
			return deps.Tap.KegRename(cmd.Context(), tapper.KegRenameOptions{
				Old:       args[0],
				New:       args[1],
				Namespace: kt.Namespace,
				Hub:       kt.Hub,
			})
		},
	}
	cmd.ValidArgsFunction = kegRenameArgCompletionFunc(deps)
	return cmd
}

func kegRenameArgCompletionFunc(deps *Deps) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return kegFlagCompletions(cmd.Context(), deps, toComplete), cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

func newKegListCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "list kegs on a hub as @namespace/keg",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// --hub is the global keg-resolution flag (default: every configured hub).
			opts := tapper.HubListOptions{Hub: globalKegTarget(deps).Hub}
			kegs, err := deps.Tap.HubListKegs(cmd.Context(), opts)
			if err != nil {
				return err
			}
			for _, k := range kegs {
				fmt.Fprintln(cmd.OutOrStdout(), k)
			}
			return nil
		},
	}
	return cmd
}

func newKegGrantsCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "grants",
		Short: "list the access grants on a keg",
		Long:  "List the access grants on the keg selected by --keg/--namespace/--hub (default: the resolved keg).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			kt := globalKegTarget(deps)
			grants, err := deps.Tap.KegGrants(cmd.Context(), tapper.KegGrantsOptions{Keg: kt.Keg, Namespace: kt.Namespace, Hub: kt.Hub})
			if err != nil {
				return err
			}
			for _, g := range grants {
				fmt.Fprintf(cmd.OutOrStdout(), "@%s\t%s\n", g.Username, g.Role)
			}
			return nil
		},
	}
	return cmd
}

func newKegGrantCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "grant <@user> <role>",
		Short: "grant a user a role on a keg (viewer|editor|admin)",
		Long:  "Grant a user a role on the keg selected by --keg/--namespace/--hub (default: the resolved keg).",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			kt := globalKegTarget(deps)
			return deps.Tap.KegGrant(cmd.Context(), tapper.KegGrantOptions{
				Keg:       kt.Keg,
				Namespace: kt.Namespace,
				Hub:       kt.Hub,
				User:      args[0],
				Role:      args[1],
			})
		},
	}
	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 1 {
			return filterByPrefix(kegGrantRoleValues, toComplete), cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return cmd
}

func newKegRevokeCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke <@user>",
		Short: "revoke a user's grant on a keg",
		Long:  "Revoke a user's grant on the keg selected by --keg/--namespace/--hub (default: the resolved keg).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kt := globalKegTarget(deps)
			return deps.Tap.KegRevoke(cmd.Context(), tapper.KegRevokeOptions{Keg: kt.Keg, Namespace: kt.Namespace, Hub: kt.Hub, User: args[0]})
		},
	}
	return cmd
}

func newKegVisibilityCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "visibility <public|private>",
		Short: "set a keg's visibility",
		Long:  "Set the visibility of the keg selected by --keg/--namespace/--hub (default: the resolved keg).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kt := globalKegTarget(deps)
			return deps.Tap.KegVisibility(cmd.Context(), tapper.KegVisibilityOptions{Keg: kt.Keg, Namespace: kt.Namespace, Hub: kt.Hub, Visibility: args[0]})
		},
	}
	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return filterByPrefix(kegVisibilityValues, toComplete), cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return cmd
}

// newKegSettingsCmd returns `tap keg settings` (formerly `tap settings`): the
// keg's own configuration (title, creator, links, schema policy, …).
func newKegSettingsCmd(deps *Deps) *cobra.Command {
	var opts tapper.KegSettingsOptions

	cmd := &cobra.Command{
		Use:   "settings",
		Short: "display keg settings",
		Long: `Display the keg settings (keg file contents).

Shows metadata about the keg including title, creator, links, schema policy, and
other configuration properties. Use 'tap keg settings edit' to modify the keg
configuration.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)

			output, err := deps.Tap.KegSettings(cmd.Context(), opts)
			if err != nil {
				return err
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), output)
			return err
		},
	}
	cmd.AddCommand(newKegSettingsEditCmd(deps))
	return cmd
}

func newKegSettingsEditCmd(deps *Deps) *cobra.Command {
	var opts tapper.KegSettingsEditOptions

	cmd := &cobra.Command{
		Use:   "edit",
		Short: "edit keg settings with default editor",
		Long: `Open the keg settings in your default editor for editing.

If stdin is piped with non-empty YAML, the piped content is validated and
written directly instead of opening an editor.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			hash, err := deps.Tap.KegSettingsHash(cmd.Context(), opts.KegTargetOptions)
			if err != nil {
				return err
			}
			opts.ExpectedHash = hash
			opts.Stream = deps.Runtime.Stream()
			return deps.Tap.KegSettingsEdit(cmd.Context(), opts)
		},
	}
	return cmd
}
