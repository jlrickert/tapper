package mcp_test

import (
	"testing"

	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func revisionTestFlight(slug string, capabilities []tapper.FlightCapability, kegName, instructions string) *tapper.Flight {
	return &tapper.Flight{
		Name: "@local/+" + slug, Namespace: "local", Slug: slug, Source: "test",
		FlightManifest: tapper.FlightManifest{
			Visibility:   tapper.FlightVisibilityPrivate,
			Capabilities: append([]tapper.FlightCapability(nil), capabilities...),
			Cover:        []tapper.FlightCover{{Namespace: "local", Keg: kegName, Role: tapper.FlightRoleEditor}},
			Instructions: instructions,
		},
	}
}

func copyRevisionTestFlight(in *tapper.Flight) *tapper.Flight {
	out := *in
	out.Capabilities = append([]tapper.FlightCapability(nil), in.Capabilities...)
	out.Cover = append([]tapper.FlightCover(nil), in.Cover...)
	out.Subflights = append([]string(nil), in.Subflights...)
	return &out
}

func TestFinalizeOrientationHashesOnlyRelevantAuthority(t *testing.T) {
	root := revisionTestFlight("root", nil, "personal", "root instructions")
	root.Subflights = []string{"@local/+child"}
	base := &mcp.Orientation{
		Root: root, Flight: root, Path: []string{root.Name}, Identity: `{"user_id":1}`,
		Kegs: []tapper.OrientationKeg{{
			Ref: "@local/personal", Role: "admin", Visibility: "private", FlightCap: "editor",
			Title: "Display title", Summary: "Display summary", Source: "atlas",
		}},
	}
	revision := func(in *mcp.Orientation) string {
		require.NoError(t, mcp.FinalizeOrientation(in))
		return in.Revision
	}
	want := revision(base)

	displayOnly := *base
	displayOnly.Revision = ""
	displayOnly.Kegs = append([]tapper.OrientationKeg(nil), base.Kegs...)
	displayOnly.Kegs[0].Title = "Renamed"
	displayOnly.Kegs[0].Summary = "Changed summary"
	displayOnly.Kegs[0].Source = "another-display-source"
	require.Equal(t, want, revision(&displayOnly))

	rootRelationOnly := *base
	rootRelationOnly.Revision = ""
	rootRelationOnly.Root = copyRevisionTestFlight(root)
	rootRelationOnly.Flight = rootRelationOnly.Root
	rootRelationOnly.Root.Subflights = nil
	require.Equal(t, want, revision(&rootRelationOnly), "root-active sessions ignore child-list edits")

	roleChanged := *base
	roleChanged.Revision = ""
	roleChanged.Kegs = append([]tapper.OrientationKeg(nil), base.Kegs...)
	roleChanged.Kegs[0].Role = "viewer"
	require.NotEqual(t, want, revision(&roleChanged))

	instructionsChanged := *base
	instructionsChanged.Revision = ""
	instructionsChanged.Root = copyRevisionTestFlight(root)
	instructionsChanged.Flight = instructionsChanged.Root
	instructionsChanged.Flight.Instructions = "changed authority instructions"
	require.NotEqual(t, want, revision(&instructionsChanged))
}

func TestFinalizeOrientationChildIgnoresItsOwnSubflights(t *testing.T) {
	root := revisionTestFlight("root", nil, "personal", "root")
	root.Subflights = []string{"@local/+child"}
	child := revisionTestFlight("child", []tapper.FlightCapability{tapper.FlightCapabilityManageKegs}, "other", "child")
	child.Subflights = []string{"@local/+grandchild"}
	base := &mcp.Orientation{Root: root, Flight: child, Path: []string{root.Name, child.Name}, Identity: "identity"}
	require.NoError(t, mcp.FinalizeOrientation(base))

	changed := *base
	changed.Revision = ""
	changed.Flight = copyRevisionTestFlight(child)
	changed.Flight.Subflights = []string{"@local/+different"}
	require.NoError(t, mcp.FinalizeOrientation(&changed))
	require.Equal(t, base.Revision, changed.Revision)

	changed.Revision = ""
	changed.Flight.Instructions = "changed"
	require.NoError(t, mcp.FinalizeOrientation(&changed))
	require.NotEqual(t, base.Revision, changed.Revision)

	changed = *base
	changed.Revision = ""
	changed.Path = []string{root.Name, "@local/+other-parent", child.Name}
	require.NoError(t, mcp.FinalizeOrientation(&changed))
	require.NotEqual(t, base.Revision, changed.Revision, "selected canonical path edges are revision material")
}

func TestCanonicalOrientationIdentityIsTransportNeutral(t *testing.T) {
	local, err := mcp.CanonicalOrientationIdentity(mcp.AuthIdentity{
		Hub: "atlas-alias", UserID: 42, Username: "ada", DisplayName: "Ada",
		DefaultNamespace: "ada", Namespaces: []string{"team", "ada"},
	})
	require.NoError(t, err)
	hosted, err := mcp.CanonicalOrientationIdentity(mcp.AuthIdentity{
		Hub: "https://hub.example", UserID: 42, Username: "ada", DisplayName: "Ada",
		DefaultNamespace: "ada", Namespaces: []string{"ada", "team"},
	})
	require.NoError(t, err)
	require.Equal(t, local, hosted)
}
