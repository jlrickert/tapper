package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// namespaceMemberRoleValues are the roles accepted by `tap namespace add-member`
// and `set-role`.
var namespaceMemberRoleValues = []string{"owner", "admin", "member"}

// NewNamespaceCmd returns the `namespace` command group: namespace discovery,
// membership/role management, and org-namespace creation.
//
//	tap namespace list
//	tap namespace members @acme
//	tap namespace add-member @acme @alice admin
//	tap namespace set-role @acme @alice member
//	tap namespace remove-member @acme @alice
//	tap namespace create acme
func NewNamespaceCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "namespace",
		Short: "administer namespaces and their members",
		Long: `Administer namespaces on a hub: list the namespaces you belong to,
manage org-namespace membership and roles, and create org namespaces.`,
		Aliases: []string{"ns"},
	}
	cmd.AddCommand(
		newNamespaceListCmd(deps),
		newNamespaceMembersCmd(deps),
		newNamespaceAddMemberCmd(deps),
		newNamespaceSetRoleCmd(deps),
		newNamespaceRemoveMemberCmd(deps),
		newNamespaceCreateCmd(deps),
	)
	return cmd
}

func newNamespaceListCmd(deps *Deps) *cobra.Command {
	var opts tapper.NamespaceListOptions
	cmd := &cobra.Command{
		Use:   "list",
		Short: "list the namespaces you belong to",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			nss, err := deps.Tap.NamespaceList(cmd.Context(), opts)
			if err != nil {
				return err
			}
			for _, ns := range nss {
				fmt.Fprintf(cmd.OutOrStdout(), "@%s\t%s\t%s\n", ns.Name, ns.Kind, ns.Role)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Hub, "hub", "", "hub to query (default: the resolved default hub)")
	return cmd
}

func newNamespaceMembersCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "members <namespace>",
		Short: "list the members of a namespace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			members, err := deps.Tap.NamespaceMembers(cmd.Context(), tapper.NamespaceMembersOptions{Namespace: args[0]})
			if err != nil {
				return err
			}
			for _, m := range members {
				fmt.Fprintf(cmd.OutOrStdout(), "@%s\t%s\n", m.Username, m.Role)
			}
			return nil
		},
	}
	cmd.ValidArgsFunction = namespaceArgCompletion(deps)
	return cmd
}

func newNamespaceAddMemberCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-member <namespace> <@user> <role>",
		Short: "add a member to a namespace (owner|admin|member)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return deps.Tap.NamespaceAddMember(cmd.Context(), tapper.NamespaceAddMemberOptions{
				Namespace: args[0],
				User:      args[1],
				Role:      args[2],
			})
		},
	}
	cmd.ValidArgsFunction = namespaceMemberRoleArgCompletion(deps, 2)
	return cmd
}

func newNamespaceSetRoleCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-role <namespace> <@user> <role>",
		Short: "change a member's role (owner|admin|member)",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return deps.Tap.NamespaceSetRole(cmd.Context(), tapper.NamespaceSetRoleOptions{
				Namespace: args[0],
				User:      args[1],
				Role:      args[2],
			})
		},
	}
	cmd.ValidArgsFunction = namespaceMemberRoleArgCompletion(deps, 2)
	return cmd
}

func newNamespaceRemoveMemberCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove-member <namespace> <@user>",
		Short: "remove a member from a namespace",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return deps.Tap.NamespaceRemoveMember(cmd.Context(), tapper.NamespaceRemoveMemberOptions{
				Namespace: args[0],
				User:      args[1],
			})
		},
	}
	cmd.ValidArgsFunction = namespaceArgCompletion(deps)
	return cmd
}

func newNamespaceCreateCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "create an org namespace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ns, err := deps.Tap.NamespaceCreate(cmd.Context(), tapper.NamespaceCreateOptions{Name: args[0]})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "@%s\n", ns.Name)
			return nil
		},
	}
	return cmd
}

// namespaceArgCompletion completes the first positional argument with the
// namespaces named in local config (best-effort, offline). Later args get
// nothing.
func namespaceArgCompletion(deps *Deps) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return filterByPrefix(configNamespaceNames(deps), toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// namespaceMemberRoleArgCompletion completes the namespace at arg 0 and the role
// enum at roleIdx; everything else gets nothing.
func namespaceMemberRoleArgCompletion(deps *Deps, roleIdx int) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		switch len(args) {
		case 0:
			return filterByPrefix(configNamespaceNames(deps), toComplete), cobra.ShellCompDirectiveNoFileComp
		case roleIdx:
			return filterByPrefix(namespaceMemberRoleValues, toComplete), cobra.ShellCompDirectiveNoFileComp
		default:
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
	}
}

// configNamespaceNames returns the namespace names known to local config (the
// namespaces map plus default/fallback), each rendered both bare and with the
// "@" sigil so completion works whether or not the user typed the prefix.
func configNamespaceNames(deps *Deps) []string {
	tap, err := completionTap(deps)
	if err != nil {
		return nil
	}
	cfg, err := tap.ConfigService.Config(true)
	if err != nil || cfg == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var names []string
	add := func(n string) {
		n = strings.TrimSpace(n)
		if n == "" {
			return
		}
		if _, ok := seen[n]; ok {
			return
		}
		seen[n] = struct{}{}
		names = append(names, n)
	}
	for n := range cfg.Namespaces() {
		add(n)
	}
	add(cfg.DefaultNamespace())
	add(cfg.FallbackNamespace())

	out := make([]string, 0, len(names)*2)
	for _, n := range names {
		out = append(out, n, "@"+n)
	}
	sort.Strings(out)
	return out
}
