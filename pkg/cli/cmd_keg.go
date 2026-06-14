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
//	tap keg settings
func NewKegCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "keg",
		Short: "administer kegs on a hub",
		Long: `Administer kegs on a hub: list and create kegs, manage per-keg
access grants and roles, set visibility, and edit a keg's own settings.`,
	}
	cmd.AddCommand(
		newKegListCmd(deps),
		newKegGrantsCmd(deps),
		newKegGrantCmd(deps),
		newKegRevokeCmd(deps),
		newKegVisibilityCmd(deps),
		newKegSettingsCmd(deps),
	)
	// `keg create` carries the keg-creation surface, gated to the same profile
	// that historically exposed `tap init` (the pruned `keg` binary omits it).
	if deps.Profile.IncludeRepoCommand {
		createCmd := newKegCreateCmd(deps)
		// create re-binds --keg/--project/--path/--cwd locally with create-time
		// semantics; strip the inherited keg-target persistent flags from its
		// "Global Flags" help so users don't see two entries for each name.
		if deps.Profile.withDefaults().AllowKegAliasFlags {
			filterRepoTargetFlagsInHelp(createCmd)
		}
		cmd.AddCommand(createCmd)
	}
	return cmd
}

func newKegListCmd(deps *Deps) *cobra.Command {
	var opts tapper.HubListOptions
	cmd := &cobra.Command{
		Use:   "list",
		Short: "list kegs on a hub as @namespace/keg",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
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
	cmd.Flags().StringVar(&opts.Hub, "hub", "", "hub to list (default: every configured hub)")
	return cmd
}

func newKegGrantsCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "grants <keg>",
		Short: "list the access grants on a keg",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			grants, err := deps.Tap.KegGrants(cmd.Context(), tapper.KegGrantsOptions{Keg: args[0]})
			if err != nil {
				return err
			}
			for _, g := range grants {
				fmt.Fprintf(cmd.OutOrStdout(), "@%s\t%s\n", g.Username, g.Role)
			}
			return nil
		},
	}
	cmd.ValidArgsFunction = kegRefArgCompletion(deps)
	return cmd
}

func newKegGrantCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "grant <keg> <@user> <role>",
		Short: "grant a user a role on a keg (viewer|editor|admin)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return deps.Tap.KegGrant(cmd.Context(), tapper.KegGrantOptions{
				Keg:  args[0],
				User: args[1],
				Role: args[2],
			})
		},
	}
	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		switch len(args) {
		case 0:
			return kegFlagCompletions(cmd.Context(), deps, toComplete), cobra.ShellCompDirectiveNoFileComp
		case 2:
			return filterByPrefix(kegGrantRoleValues, toComplete), cobra.ShellCompDirectiveNoFileComp
		default:
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}
	return cmd
}

func newKegRevokeCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke <keg> <@user>",
		Short: "revoke a user's grant on a keg",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return deps.Tap.KegRevoke(cmd.Context(), tapper.KegRevokeOptions{Keg: args[0], User: args[1]})
		},
	}
	cmd.ValidArgsFunction = kegRefArgCompletion(deps)
	return cmd
}

func newKegVisibilityCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "visibility <keg> <public|private>",
		Short: "set a keg's visibility",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return deps.Tap.KegVisibility(cmd.Context(), tapper.KegVisibilityOptions{Keg: args[0], Visibility: args[1]})
		},
	}
	cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		switch len(args) {
		case 0:
			return kegFlagCompletions(cmd.Context(), deps, toComplete), cobra.ShellCompDirectiveNoFileComp
		case 1:
			return filterByPrefix(kegVisibilityValues, toComplete), cobra.ShellCompDirectiveNoFileComp
		default:
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}
	return cmd
}

// newKegSettingsCmd returns `tap keg settings` (formerly `tap settings`): the
// keg's own configuration (title, creator, entities, tags, …).
func newKegSettingsCmd(deps *Deps) *cobra.Command {
	var opts tapper.InfoOptions

	cmd := &cobra.Command{
		Use:   "settings",
		Short: "display keg configuration",
		Long: `Display the keg configuration (keg file contents).

Shows metadata about the keg including title, creator, entities, tags, and
other configuration properties. Use 'tap keg settings edit' to modify the keg
configuration.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)

			output, err := deps.Tap.Info(cmd.Context(), opts)
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
	var opts tapper.KegConfigEditOptions

	cmd := &cobra.Command{
		Use:   "edit",
		Short: "edit keg configuration with default editor",
		Long: `Open the keg configuration in your default editor for editing.

If stdin is piped with non-empty YAML, the piped content is validated and
written directly instead of opening an editor.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			opts.Stream = deps.Runtime.Stream()
			return deps.Tap.KegConfigEdit(cmd.Context(), opts)
		},
	}
	return cmd
}

// kegRefArgCompletion completes the first positional argument as a keg
// reference (@namespace/keg or a bare name) and offers nothing for later args.
func kegRefArgCompletion(deps *Deps) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return kegFlagCompletions(cmd.Context(), deps, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}
