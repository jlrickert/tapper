package tapper

import (
	"context"
	"fmt"
)

// InvitationOptions selects a hub (empty: the selected hub) and, for
// decisions, one invitation.
type InvitationOptions struct {
	Hub string
	ID  int64
}

// InvitationList returns the caller's pending invitations on a hub.
func (t *Tap) InvitationList(ctx context.Context, opts InvitationOptions) ([]HubInvitation, error) {
	name, entry, err := t.ConfigService.SelectedHub(opts.Hub)
	if err != nil {
		return nil, err
	}
	hubURL, token, err := remoteHubEndpoint(t, name, entry)
	if err != nil {
		return nil, err
	}
	rows, err := ListInvitations(ctx, hubURL, token)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Hub = name
	}
	return rows, nil
}

// InvitationAccept accepts one of the caller's invitations.
func (t *Tap) InvitationAccept(ctx context.Context, opts InvitationOptions) (*HubInvitation, error) {
	hubURL, token, err := t.invitationHub(opts)
	if err != nil {
		return nil, err
	}
	return AcceptInvitation(ctx, hubURL, token, opts.ID)
}

// InvitationDecline declines one of the caller's invitations.
func (t *Tap) InvitationDecline(ctx context.Context, opts InvitationOptions) (*HubInvitation, error) {
	hubURL, token, err := t.invitationHub(opts)
	if err != nil {
		return nil, err
	}
	return DeclineInvitation(ctx, hubURL, token, opts.ID)
}

// InvitationRevoke withdraws a pending invitation the caller sent or could
// send (a namespace owner, or a keg admin).
func (t *Tap) InvitationRevoke(ctx context.Context, opts InvitationOptions) error {
	hubURL, token, err := t.invitationHub(opts)
	if err != nil {
		return err
	}
	return RevokeInvitation(ctx, hubURL, token, opts.ID)
}

func (t *Tap) invitationHub(opts InvitationOptions) (hubURL, token string, err error) {
	if opts.ID <= 0 {
		return "", "", fmt.Errorf("an invitation id is required")
	}
	name, entry, err := t.ConfigService.SelectedHub(opts.Hub)
	if err != nil {
		return "", "", err
	}
	return remoteHubEndpoint(t, name, entry)
}
