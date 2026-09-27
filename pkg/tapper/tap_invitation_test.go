package tapper_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/tapper"
)

func TestInvitationListAcceptDeclineRevoke(t *testing.T) {
	t.Parallel()
	var calls []string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/invitations":
			_ = json.NewEncoder(w).Encode([]tapper.HubInvitation{{ID: 3, Kind: "grant", Target: "@acme/notes", Role: "editor"}})
		case "POST /api/v1/invitations/3/accept", "POST /api/v1/invitations/3/decline":
			_ = json.NewEncoder(w).Encode(tapper.HubInvitation{ID: 3, Kind: "grant", Target: "@acme/notes", Role: "editor"})
		case "POST /api/v1/invitations/4/accept":
			w.WriteHeader(http.StatusGone)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "stale"})
		case "DELETE /api/v1/invitations/3":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	tap, fx, _ := newRemoteHubTap(t, h)
	ctx := fx.Context()

	rows, err := tap.InvitationList(ctx, tapper.InvitationOptions{})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "@acme/notes", rows[0].Target)
	require.NotEmpty(t, rows[0].Hub)

	inv, err := tap.InvitationAccept(ctx, tapper.InvitationOptions{ID: 3})
	require.NoError(t, err)
	require.Equal(t, "editor", inv.Role)
	_, err = tap.InvitationDecline(ctx, tapper.InvitationOptions{ID: 3})
	require.NoError(t, err)
	require.NoError(t, tap.InvitationRevoke(ctx, tapper.InvitationOptions{ID: 3}))

	_, err = tap.InvitationAccept(ctx, tapper.InvitationOptions{ID: 4})
	require.ErrorIs(t, err, tapper.ErrInvitationVoid)
	_, err = tap.InvitationAccept(ctx, tapper.InvitationOptions{})
	require.Error(t, err, "an id is required")
	require.Len(t, calls, 5)
}
