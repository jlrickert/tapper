package tapper_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestFlightService_ListHubFilterContactsOnlySelectedHub(t *testing.T) {
	t.Parallel()
	var firstCalls, secondCalls atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstCalls.Add(1)
		require.Equal(t, "/api/v1/flights", r.URL.Path)
		_ = json.NewEncoder(w).Encode([]tapper.HubFlight{{Namespace: "one", Slug: "focus"}})
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondCalls.Add(1)
		require.Equal(t, "/api/v1/flights", r.URL.Path)
		_ = json.NewEncoder(w).Encode([]tapper.HubFlight{{Namespace: "two", Slug: "review"}})
	}))
	defer second.Close()

	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	config := "hubs:\n" +
		"  first: {kind: remote, url: " + first.URL + ", token: tok}\n" +
		"  second: {kind: remote, url: " + second.URL + ", token: tok}\n"
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(config), 0o644))

	names, err := tap.ListFlights(fx.Context(), tapper.ListFlightsOptions{Hub: "first"})
	require.NoError(t, err)
	require.Equal(t, []string{"@one/+focus"}, names)
	require.EqualValues(t, 1, firstCalls.Load())
	require.Zero(t, secondCalls.Load(), "filtered discovery must not contact unrelated hubs")

	names, err = tap.ListFlights(fx.Context(), tapper.ListFlightsOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"@one/+focus", "@two/+review"}, names)
	require.EqualValues(t, 2, firstCalls.Load())
	require.EqualValues(t, 1, secondCalls.Load(), "unfiltered listing should retain all-hub behavior")
}

func TestFlightService_RemoteGraphUsesOneFreshBatchPerResolution(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/flights", r.URL.Path)
		require.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		generation := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if generation == 1 {
			_ = json.NewEncoder(w).Encode([]tapper.HubFlight{
				{Namespace: "team", Slug: "root", Title: "generation one", Subflights: []string{"+child", "+hidden"}},
				{Namespace: "team", Slug: "child", Subflights: []string{"+root", "+shared"}},
				{Namespace: "team", Slug: "shared"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode([]tapper.HubFlight{
			{Namespace: "team", Slug: "root", Title: "generation two", Subflights: []string{"+next"}},
			{Namespace: "team", Slug: "next"},
		})
	}))
	defer srv.Close()

	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	config := "hubs:\n  atlas: {kind: remote, url: " + srv.URL + ", token: tok}\n"
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(config), 0o644))
	root := &tapper.Flight{Name: "@team/+root", Namespace: "team", Slug: "root", Source: "atlas"}

	first, err := tap.FlightService.ResolveFlightGraph(t.Context(), root)
	require.NoError(t, err)
	require.Equal(t, int32(1), calls.Load())
	require.Equal(t, "generation one", first.Root.Title)
	require.Equal(t, []string{"@team/+child", "@team/+shared"}, first.AvailableRefs(), "cycles and shared descendants must terminate; absent branches are omitted")

	second, err := tap.FlightService.ResolveFlightGraph(t.Context(), root)
	require.NoError(t, err)
	require.Equal(t, int32(2), calls.Load(), "each resolution must issue exactly one batch request")
	require.Equal(t, "generation two", second.Root.Title)
	require.Equal(t, []string{"@team/+next"}, second.AvailableRefs(), "a later resolution must observe live graph changes")
}

func TestParseFlightRef(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		raw              string
		defaultNamespace string
		want             tapper.FlightRef
	}{
		{name: "slug", raw: "agent-work", want: tapper.FlightRef{Slug: "agent-work"}},
		{name: "plus_slug", raw: "+agent-work", want: tapper.FlightRef{Slug: "agent-work"}},
		{name: "default_namespace", raw: "+agent-work", defaultNamespace: "jlrickert", want: tapper.FlightRef{Namespace: "jlrickert", Slug: "agent-work"}},
		{name: "qualified", raw: "@foldwise/+agent-work", want: tapper.FlightRef{Namespace: "foldwise", Slug: "agent-work"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tapper.ParseFlightRef(tc.raw, tc.defaultNamespace)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestParseFlightRefRejectsUnusableNamespace pins that a malformed namespace
// fails here rather than travelling. The `+` sigil marks the slug, so
// "@+slug/..." transposes it into the namespace position — a namespace that can
// never exist. Left unvalidated it reaches the hub and returns as a bare 404,
// which reads as a missing flight and sends the author looking at permissions.
func TestParseFlightRefRejectsUnusableNamespace(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"@+agent-work/x", "@Foldwise/+agent-work", "@foldwise.dev/+agent-work"} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			_, err := tapper.ParseFlightRef(raw, "jlrickert")
			require.ErrorContains(t, err, "invalid flight reference")
			require.ErrorContains(t, err, "@namespace/+slug",
				"the refusal must show the form the author meant to write")
		})
	}
}

func TestFlightRoleFor_CoverCapsWrites(t *testing.T) {
	t.Parallel()
	flight := &tapper.Flight{
		Name: "@foldwise/+review",
		FlightManifest: tapper.FlightManifest{
			Cover: []tapper.FlightCover{
				{Namespace: "foldwise", Keg: "docs", Role: tapper.FlightRoleViewer},
				{Namespace: "foldwise", Keg: "dev", Role: tapper.FlightRoleEditor},
				{Namespace: "foldwise", Keg: "admin", Role: tapper.FlightRoleAdmin},
			},
		},
	}
	role, ok := flight.RoleFor("", "foldwise", "docs")
	require.True(t, ok)
	require.Equal(t, tapper.FlightRoleViewer, role)
	require.True(t, role.AtLeast(tapper.FlightRoleViewer))
	require.False(t, role.AtLeast(tapper.FlightRoleEditor))

	role, ok = flight.RoleFor("", "foldwise", "dev")
	require.True(t, ok)
	require.Equal(t, tapper.FlightRoleEditor, role)
	require.True(t, role.AtLeast(tapper.FlightRoleEditor))
	require.False(t, role.AtLeast(tapper.FlightRoleAdmin))

	role, ok = flight.RoleFor("", "foldwise", "admin")
	require.True(t, ok)
	require.Equal(t, tapper.FlightRoleAdmin, role)
	require.True(t, role.AtLeast(tapper.FlightRoleViewer))
	require.True(t, role.AtLeast(tapper.FlightRoleEditor))
	require.True(t, role.AtLeast(tapper.FlightRoleAdmin))

	_, ok = flight.RoleFor("", "foldwise", "private")
	require.False(t, ok)
}

func TestParseFlightCoverSpecs_AdminAndUnknownRoles(t *testing.T) {
	t.Parallel()
	cover, err := tapper.ParseFlightCoverSpecs([]string{"@foldwise/dev=admin"})
	require.NoError(t, err)
	require.Equal(t, []tapper.FlightCover{{
		Namespace: "foldwise",
		Keg:       "dev",
		Role:      tapper.FlightRoleAdmin,
	}}, cover)

	_, err = tapper.ParseFlightCoverSpecs([]string{"@foldwise/dev=owner"})
	require.ErrorContains(t, err, `invalid flight cover role "owner"`)
}

func TestFlightRoleFor_EmptyCoverDeniesAll(t *testing.T) {
	t.Parallel()
	flight := &tapper.Flight{Name: "@foldwise/+empty"}
	_, ok := flight.RoleFor("docs", "foldwise", "docs")
	require.False(t, ok)
}

func TestFlightRoleFor_ViewerCapStableAcrossCalls(t *testing.T) {
	t.Parallel()
	flight := &tapper.Flight{
		Name: "@foldwise/+review",
		FlightManifest: tapper.FlightManifest{
			Cover: []tapper.FlightCover{
				{Namespace: "foldwise", Keg: "docs", Role: tapper.FlightRoleViewer},
			},
			// Legacy mirror naming the same keg, as older normalize passes
			// produced. Must not escalate the explicit viewer cap.
			AllowedKegs: []string{"@foldwise/docs"},
		},
	}
	for i := range 3 {
		role, ok := flight.RoleFor("", "foldwise", "docs")
		require.True(t, ok)
		require.Equal(t, tapper.FlightRoleViewer, role, "call %d", i)
	}
	require.Len(t, flight.Cover, 1, "RoleFor must not mutate the flight's cover")
}

// Legacy allowedKegs entries keep their historical editor default, but an
// explicit =viewer suffix is honored.
func TestFlightRoleFor_LegacyAllowedKegsRoles(t *testing.T) {
	t.Parallel()
	flight := &tapper.Flight{
		Name: "@foldwise/+legacy",
		FlightManifest: tapper.FlightManifest{
			AllowedKegs: []string{"@foldwise/dev", "@foldwise/docs=viewer"},
		},
	}
	role, ok := flight.RoleFor("", "foldwise", "dev")
	require.True(t, ok)
	require.Equal(t, tapper.FlightRoleEditor, role)

	role, ok = flight.RoleFor("", "foldwise", "docs")
	require.True(t, ok)
	require.Equal(t, tapper.FlightRoleViewer, role)
}

func TestFlattenFlightGraph_BreadthFirstDedupAndSelection(t *testing.T) {
	t.Parallel()
	flight := func(name string, children ...string) *tapper.Flight {
		ref, err := tapper.ParseFlightRef(name, "")
		require.NoError(t, err)
		return &tapper.Flight{
			Name: name, Namespace: ref.Namespace, Slug: ref.Slug, Source: "atlas",
			FlightManifest: tapper.FlightManifest{
				Visibility: tapper.FlightVisibilityPrivate,
				Subflights: children,
			},
		}
	}
	root := flight("@team/+root", "+right", "@team/+left")
	flights := map[string]*tapper.Flight{
		"@team/+left":   flight("@team/+left", "+shared", "@other/+grand"),
		"@team/+right":  flight("@team/+right", "+shared"),
		"@team/+shared": flight("@team/+shared"),
		"@other/+grand": flight("@other/+grand"),
	}
	fetched := map[string]int{}
	graph, err := tapper.FlattenFlightGraph(t.Context(), root, func(_ context.Context, ref string) (*tapper.Flight, error) {
		fetched[ref]++
		return flights[ref], nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"@team/+right", "@team/+left", "@team/+shared", "@other/+grand"}, graph.AvailableRefs())
	require.Equal(t, 1, fetched["@team/+shared"], "shared descendant must be fetched once")
	active, path, err := graph.Select("+shared")
	require.NoError(t, err)
	require.Equal(t, "@team/+shared", active.Name)
	require.Equal(t, []string{"@team/+root", "@team/+right", "@team/+shared"}, path)
	active, path, err = graph.Select("@team/+root")
	require.NoError(t, err)
	require.Same(t, root, active)
	require.Equal(t, []string{"@team/+root"}, path)
}

func TestFlattenFlightGraph_RejectsMalformedGraphs(t *testing.T) {
	t.Parallel()
	base := func(name, source string, children ...string) *tapper.Flight {
		ref, err := tapper.ParseFlightRef(name, "")
		require.NoError(t, err)
		return &tapper.Flight{Name: name, Namespace: ref.Namespace, Slug: ref.Slug, Source: source,
			FlightManifest: tapper.FlightManifest{Visibility: tapper.FlightVisibilityPrivate, Subflights: children}}
	}
	for _, tc := range []struct {
		name    string
		root    *tapper.Flight
		flights map[string]*tapper.Flight
		wantErr string
	}{
		{
			name: "cross source",
			root: base("@team/+root", "atlas", "@team/+child"),
			flights: map[string]*tapper.Flight{
				"@team/+child": base("@team/+child", "other"),
			},
			wantErr: "outside root source",
		},
		// A cycle is deliberately absent here: it is no longer malformed. See
		// TestFlattenFlightGraph_ToleratesCycles.
		{
			name: "canonical direct duplicate",
			root: base("@team/+root", "atlas", "+child", "@team/+child"),
			flights: map[string]*tapper.Flight{
				"@team/+child": base("@team/+child", "atlas"),
			},
			wantErr: "duplicate canonical",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tapper.FlattenFlightGraph(t.Context(), tc.root, func(_ context.Context, ref string) (*tapper.Flight, error) {
				return tc.flights[ref], nil
			})
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestFlattenFlightGraph_ExcludesInaccessibleBranchAndKeepsIndependentAuthority(t *testing.T) {
	root := &tapper.Flight{Name: "@team/+root", Namespace: "team", Slug: "root", Source: "atlas",
		FlightManifest: tapper.FlightManifest{Subflights: []string{"+hidden", "+manager"}}}
	manager := &tapper.Flight{Name: "@team/+manager", Namespace: "team", Slug: "manager", Source: "atlas",
		FlightManifest: tapper.FlightManifest{Capabilities: []tapper.FlightCapability{tapper.FlightCapabilityManageKegs}}}
	graph, err := tapper.FlattenFlightGraph(t.Context(), root, func(_ context.Context, ref string) (*tapper.Flight, error) {
		if ref == "@team/+hidden" {
			return nil, keg.ErrForbidden
		}
		return manager, nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"@team/+manager"}, graph.AvailableRefs())
	_, _, err = graph.Select("+hidden")
	require.ErrorIs(t, err, tapper.ErrFlightSubflightNotAllowed)
	selected, _, err := graph.Select("+manager")
	require.NoError(t, err)
	require.True(t, selected.HasCapability(tapper.FlightCapabilityManageKegs))
}

func TestFlattenFlightGraph_EnforcesDirectAndUniqueBounds(t *testing.T) {
	direct := &tapper.Flight{Name: "@team/+direct", Namespace: "team", Slug: "direct", Source: "atlas"}
	for i := 0; i <= tapper.MaxFlightSubflights; i++ {
		direct.Subflights = append(direct.Subflights, fmt.Sprintf("@team/+d%d", i))
	}
	_, err := tapper.FlattenFlightGraph(t.Context(), direct, func(_ context.Context, ref string) (*tapper.Flight, error) {
		return &tapper.Flight{Name: ref, Namespace: "team", Source: "atlas"}, nil
	})
	require.ErrorContains(t, err, "maximum direct subflight")

	// A long chain is no longer rejected: depth is not a bound. Only the
	// unique-descendant cap limits how far a traversal will go.
	chain := map[string]*tapper.Flight{}
	root := &tapper.Flight{Name: "@team/+n0", Namespace: "team", Slug: "n0", Source: "atlas"}
	previous := root
	for i := 1; i <= 32; i++ {
		name := fmt.Sprintf("@team/+n%d", i)
		previous.Subflights = []string{name}
		next := &tapper.Flight{Name: name, Namespace: "team", Slug: fmt.Sprintf("n%d", i), Source: "atlas"}
		chain[name] = next
		previous = next
	}
	deep, err := tapper.FlattenFlightGraph(t.Context(), root, func(_ context.Context, ref string) (*tapper.Flight, error) {
		return chain[ref], nil
	})
	require.NoError(t, err)
	require.Len(t, deep.Available, 32)

	wide := &tapper.Flight{Name: "@team/+wide", Namespace: "team", Slug: "wide", Source: "atlas"}
	flights := map[string]*tapper.Flight{}
	for i := 0; i < 64; i++ {
		childName := fmt.Sprintf("@team/+c%d", i)
		wide.Subflights = append(wide.Subflights, childName)
		child := &tapper.Flight{Name: childName, Namespace: "team", Slug: fmt.Sprintf("c%d", i), Source: "atlas"}
		flights[childName] = child
		for j := 0; j < 5; j++ {
			grandName := fmt.Sprintf("@team/+c%d-g%d", i, j)
			child.Subflights = append(child.Subflights, grandName)
			flights[grandName] = &tapper.Flight{Name: grandName, Namespace: "team", Slug: fmt.Sprintf("c%d-g%d", i, j), Source: "atlas"}
		}
	}
	_, err = tapper.FlattenFlightGraph(t.Context(), wide, func(_ context.Context, ref string) (*tapper.Flight, error) {
		return flights[ref], nil
	})
	require.ErrorContains(t, err, "maximum unique descendant")
}

func TestFlattenFlightGraph_KeepsShortestPathToSharedDescendant(t *testing.T) {
	build := func(longDepth int) (*tapper.Flight, map[string]*tapper.Flight) {
		root := &tapper.Flight{Name: "@team/+root", Namespace: "team", Slug: "root", Source: "atlas"}
		shared := &tapper.Flight{Name: "@team/+shared", Namespace: "team", Slug: "shared", Source: "atlas"}
		root.Subflights = []string{shared.Name, "@team/+long-1"}
		flights := map[string]*tapper.Flight{shared.Name: shared}
		previous := root
		for i := 1; i < longDepth; i++ {
			name := fmt.Sprintf("@team/+long-%d", i)
			current := &tapper.Flight{Name: name, Namespace: "team", Slug: fmt.Sprintf("long-%d", i), Source: "atlas"}
			flights[name] = current
			if previous != root {
				previous.Subflights = []string{name}
			}
			previous = current
		}
		previous.Subflights = []string{shared.Name}
		return root, flights
	}

	// A descendant reachable both directly and down a long chain keeps the
	// deterministic shortest selection path, however long the other route is.
	for _, longDepth := range []int{4, 12} {
		root, flights := build(longDepth)
		graph, err := tapper.FlattenFlightGraph(t.Context(), root, func(_ context.Context, ref string) (*tapper.Flight, error) {
			return flights[ref], nil
		})
		require.NoError(t, err)
		sharedRef := "@team/+shared"
		_, path, err := graph.Select(sharedRef)
		require.NoError(t, err)
		require.Equal(t, []string{root.Name, sharedRef}, path)
	}
}

// TestFlattenFlightGraph_ToleratesCycles is the property the removal of the
// cycle and depth passes rests on: a subflight entry is a list item, not an
// assertion about graph shape, so a cyclic manifest must flatten to a finite,
// usable graph rather than erroring or looping. Authority is never inherited
// from an ancestor, so mutual reference grants nothing either.
func TestFlattenFlightGraph_ToleratesCycles(t *testing.T) {
	a := &tapper.Flight{Name: "@team/+a", Namespace: "team", Slug: "a", Source: "atlas", FlightManifest: tapper.FlightManifest{Subflights: []string{"@team/+b"}}}
	b := &tapper.Flight{Name: "@team/+b", Namespace: "team", Slug: "b", Source: "atlas", FlightManifest: tapper.FlightManifest{Subflights: []string{"@team/+a"}}}
	flights := map[string]*tapper.Flight{a.Name: a, b.Name: b}

	graph, err := tapper.FlattenFlightGraph(t.Context(), a, func(_ context.Context, ref string) (*tapper.Flight, error) {
		return flights[ref], nil
	})
	require.NoError(t, err, "a cycle must flatten, not fail")
	require.Equal(t, []string{"@team/+b"}, graph.AvailableRefs())

	selected, path, err := graph.Select("@team/+b")
	require.NoError(t, err)
	require.Equal(t, b.Name, selected.Name)
	require.Equal(t, []string{a.Name, b.Name}, path)

	// Selecting the root back through the cycle resolves to the root itself
	// rather than re-entering it as its own descendant.
	rootAgain, rootPath, err := graph.Select(a.Name)
	require.NoError(t, err)
	require.Equal(t, a.Name, rootAgain.Name)
	require.Equal(t, []string{a.Name}, rootPath)

	// A three-flight cycle is equally finite.
	x := &tapper.Flight{Name: "@team/+x", Namespace: "team", Slug: "x", Source: "atlas", FlightManifest: tapper.FlightManifest{Subflights: []string{"@team/+y"}}}
	y := &tapper.Flight{Name: "@team/+y", Namespace: "team", Slug: "y", Source: "atlas", FlightManifest: tapper.FlightManifest{Subflights: []string{"@team/+z"}}}
	z := &tapper.Flight{Name: "@team/+z", Namespace: "team", Slug: "z", Source: "atlas", FlightManifest: tapper.FlightManifest{Subflights: []string{"@team/+x"}}}
	ring := map[string]*tapper.Flight{x.Name: x, y.Name: y, z.Name: z}
	ringGraph, err := tapper.FlattenFlightGraph(t.Context(), x, func(_ context.Context, ref string) (*tapper.Flight, error) {
		return ring[ref], nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{"@team/+y", "@team/+z"}, ringGraph.AvailableRefs())
}
