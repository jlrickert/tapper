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
	cmd := &cobra.Command{
		Use:   "list",
		Short: "list the namespaces you belong to",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// --hub is the global keg-resolution flag (default: the resolved default hub).
			opts := tapper.NamespaceListOptions{Hub: globalKegTarget(deps).Hub}
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
	return cmd
}

func newNamespaceMembersCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "members",
		Short: "list the members of a namespace",
		Long:  "List the members of the namespace selected by --namespace/--hub (default: the resolved namespace).",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			kt := globalKegTarget(deps)
			members, err := deps.Tap.NamespaceMembers(cmd.Context(), tapper.NamespaceMembersOptions{Namespace: kt.Namespace, Hub: kt.Hub})
			if err != nil {
				return err
			}
			for _, m := range members {
				fmt.Fprintf(cmd.OutOrStdout(), "@%s\t%s\n", m.Username, m.Role)
			}
			return nil
		},
	}
	return cmd
}

func newNamespaceAddMemberCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add-member <@user> <role>",
		Short: "add a member to a namespace (owner|admin|member)",
		Long:  "Add a member to the namespace selected by --namespace/--hub (default: the resolved namespace).",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			kt := globalKegTarget(deps)
			return deps.Tap.NamespaceAddMember(cmd.Context(), tapper.NamespaceAddMemberOptions{
				Namespace: kt.Namespace,
				Hub:       kt.Hub,
				User:      args[0],
				Role:      args[1],
			})
		},
	}
	cmd.ValidArgsFunction = namespaceMemberRoleArgCompletion(deps, 1)
	return cmd
}

func newNamespaceSetRoleCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set-role <@user> <role>",
		Short: "change a member's role (owner|admin|member)",
		Long:  "Change a member's role in the namespace selected by --namespace/--hub (default: the resolved namespace).",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			kt := globalKegTarget(deps)
			return deps.Tap.NamespaceSetRole(cmd.Context(), tapper.NamespaceSetRoleOptions{
				Namespace: kt.Namespace,
				Hub:       kt.Hub,
				User:      args[0],
				Role:      args[1],
			})
		},
	}
	cmd.ValidArgsFunction = namespaceMemberRoleArgCompletion(deps, 1)
	return cmd
}

func newNamespaceRemoveMemberCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove-member <@user>",
		Short: "remove a member from a namespace",
		Long:  "Remove a member from the namespace selected by --namespace/--hub (default: the resolved namespace).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kt := globalKegTarget(deps)
			return deps.Tap.NamespaceRemoveMember(cmd.Context(), tapper.NamespaceRemoveMemberOptions{
				Namespace: kt.Namespace,
				Hub:       kt.Hub,
				User:      args[0],
			})
		},
	}
	return cmd
}

func newNamespaceCreateCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "open the hub UI to create an org namespace",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			kt := globalKegTarget(deps)
			ns, err := deps.Tap.NamespaceCreate(cmd.Context(), tapper.NamespaceCreateOptions{Name: args[0], Hub: kt.Hub})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Create @%s in the hub UI:\n%s\n", ns.Name, ns.URL)
			return nil
		},
	}
	return cmd
}

// namespaceMemberRoleArgCompletion completes the role enum at roleIdx; the user
// argument and everything else get nothing (the namespace is a flag now).
func namespaceMemberRoleArgCompletion(_ *Deps, roleIdx int) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == roleIdx {
			return filterByPrefix(namespaceMemberRoleValues, toComplete), cobra.ShellCompDirectiveNoFileComp
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
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
	cfg, err := tap.ConfigService.Config()
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
