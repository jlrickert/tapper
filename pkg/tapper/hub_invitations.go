// Package tapper — hub invitation client calls.
//
// Org membership and keg grants are offered, not imposed: adding someone
// creates an invitation, and access starts only when its invitee accepts it.
// These talk to the hub's /api/v1/invitations endpoints over doHubJSON.
package tapper

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrInvitationVoid reports an invitation that can no longer be accepted
// because whoever sent it can no longer extend the access.
var ErrInvitationVoid = errors.New("invitation is no longer valid")

// AccessChange is the result of adding a member or grant. Invited means the
// hub created an invitation: nothing changes until the invitee accepts it.
// Mirrors the hub's 202 body; a 200/201 role change leaves Invited false.
type AccessChange struct {
	Status       string `json:"status,omitempty"`
	InvitationID int64  `json:"invitation_id,omitempty"`
	Username     string `json:"username,omitempty"`
	Role         string `json:"role,omitempty"`
}

// Invited reports whether the change became an invitation.
func (c AccessChange) Invited() bool { return c.Status == "invited" }

// HubInvitation is one pending invitation. Mirrors the hub's
// handler.invitationWire JSON body — keep the two in sync.
type HubInvitation struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`   // membership|grant
	Target    string    `json:"target"` // @ns or @ns/keg
	Namespace string    `json:"namespace"`
	Keg       string    `json:"keg,omitempty"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	InvitedBy string    `json:"invited_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`

	// Hub names the configured hub this row came from. Filled in locally.
	Hub string `json:"hub,omitempty"`
}

// ListInvitations returns the caller's pending invitations via
// GET /api/v1/invitations.
func ListInvitations(ctx context.Context, hubURL, token string) ([]HubInvitation, error) {
	var out []HubInvitation
	if err := doHubJSON(ctx, http.MethodGet, hubURL, token, "/api/v1/invitations", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AcceptInvitation accepts an invitation addressed to the caller.
func AcceptInvitation(ctx context.Context, hubURL, token string, id int64) (*HubInvitation, error) {
	var out HubInvitation
	if err := doHubJSON(ctx, http.MethodPost, hubURL, token, fmt.Sprintf("/api/v1/invitations/%d/accept", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeclineInvitation discards an invitation addressed to the caller.
func DeclineInvitation(ctx context.Context, hubURL, token string, id int64) (*HubInvitation, error) {
	var out HubInvitation
	if err := doHubJSON(ctx, http.MethodPost, hubURL, token, fmt.Sprintf("/api/v1/invitations/%d/decline", id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RevokeInvitation withdraws a pending invitation the caller could extend.
func RevokeInvitation(ctx context.Context, hubURL, token string, id int64) error {
	return doHubJSON(ctx, http.MethodDelete, hubURL, token, fmt.Sprintf("/api/v1/invitations/%d", id), nil, nil)
}
