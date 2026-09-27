package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// printAccessChange reports the outcome of adding a member or grant: either an
// immediate role change or an invitation the user must still accept.
func printAccessChange(cmd *cobra.Command, change tapper.AccessChange, user, role string) {
	user = "@" + strings.TrimPrefix(strings.TrimSpace(user), "@")
	if change.Invited() {
		fmt.Fprintf(cmd.OutOrStdout(), "invited %s as %s (invitation %d); access starts when they accept\n", user, role, change.InvitationID)
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s is now %s\n", user, role)
}

// NewInvitationCmd manages pending invitations to org namespaces and kegs.
func NewInvitationCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "invitation",
		Aliases: []string{"invitations"},
		Short:   "list, accept, decline, or withdraw invitations to namespaces and kegs",
		Long: "Org membership and keg grants are offered, not imposed: access starts only when the invitee\n" +
			"accepts. `tap invitation list` shows invitations addressed to you on the selected hub (--hub).",
	}
	cmd.AddCommand(newInvitationListCmd(deps), newInvitationDecideCmd(deps, "accept"), newInvitationDecideCmd(deps, "decline"), newInvitationRevokeCmd(deps))
	return cmd
}

func newInvitationListCmd(deps *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "list invitations addressed to you",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rows, err := deps.Tap.InvitationList(cmd.Context(), tapper.InvitationOptions{Hub: globalKegTarget(deps).Hub})
			if err != nil {
				return err
			}
			for _, inv := range rows {
				fmt.Fprintf(cmd.OutOrStdout(), "%d\t%s\t%s\t%s\t@%s\t%s\n", inv.ID, inv.Kind, inv.Target, inv.Role, inv.InvitedBy, inv.ExpiresAt.Format("2006-01-02"))
			}
			return nil
		},
	}
}

func newInvitationDecideCmd(deps *Deps, action string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   action + " <id>",
		Short: action + " an invitation addressed to you",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseInvitationID(args[0])
			if err != nil {
				return err
			}
			opts := tapper.InvitationOptions{Hub: globalKegTarget(deps).Hub, ID: id}
			var inv *tapper.HubInvitation
			if action == "accept" {
				inv, err = deps.Tap.InvitationAccept(cmd.Context(), opts)
			} else {
				inv, err = deps.Tap.InvitationDecline(cmd.Context(), opts)
			}
			if err != nil {
				return err
			}
			if action == "accept" {
				fmt.Fprintf(cmd.OutOrStdout(), "joined %s as %s\n", inv.Target, inv.Role)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "declined the invitation to %s\n", inv.Target)
			}
			return nil
		},
	}
	cmd.ValidArgsFunction = invitationIDCompletion(deps)
	return cmd
}

func newInvitationRevokeCmd(deps *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <id>",
		Short: "withdraw a pending invitation you can manage",
		Long:  "Withdraw a pending invitation. Requires the namespace owner role (memberships) or keg admin role (grants).",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseInvitationID(args[0])
			if err != nil {
				return err
			}
			if err := deps.Tap.InvitationRevoke(cmd.Context(), tapper.InvitationOptions{Hub: globalKegTarget(deps).Hub, ID: id}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "withdrew invitation %d\n", id)
			return nil
		},
	}
}

func parseInvitationID(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid invitation id %q", raw)
	}
	return id, nil
}

// invitationIDCompletion completes the ids of invitations addressed to the
// caller, described by their target.
func invitationIDCompletion(deps *Deps) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 || deps.Tap == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		rows, err := deps.Tap.InvitationList(cmd.Context(), tapper.InvitationOptions{Hub: globalKegTarget(deps).Hub})
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var out []string
		for _, inv := range rows {
			id := strconv.FormatInt(inv.ID, 10)
			if strings.HasPrefix(id, toComplete) {
				out = append(out, id+"\t"+inv.Target+" as "+inv.Role)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
}
