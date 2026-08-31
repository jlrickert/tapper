package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type perCallFlightBackend struct {
	mu         sync.Mutex
	root       string
	flights    map[string]*tapper.Flight
	kegs       []string
	created    []string
	resolves   int
	resolveErr error
}

type refreshFlightBackend struct {
	*perCallFlightBackend
	refreshMu sync.Mutex
	loadMode  string
	loadRoot  string
	loadErr   error
	loads     int
	loadStart chan<- struct{}
	loadWait  <-chan struct{}
}

func newRefreshFlightBackend(mode string) *refreshFlightBackend {
	base := newPerCallFlightBackend()
	return &refreshFlightBackend{
		perCallFlightBackend: base,
		loadMode:             mode,
		loadRoot:             base.root,
	}
}

func (p *refreshFlightBackend) Load(ctx context.Context) (*mcp.Orientation, error) {
	p.refreshMu.Lock()
	p.loads++
	mode, rootRef, loadErr := p.loadMode, p.loadRoot, p.loadErr
	loadStart, loadWait := p.loadStart, p.loadWait
	p.loadStart, p.loadWait = nil, nil
	p.refreshMu.Unlock()
	if loadStart != nil {
		close(loadStart)
	}
	if loadWait != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-loadWait:
		}
	}
	if loadErr != nil {
		return nil, loadErr
	}
	switch mode {
	case "active":
		return p.Resolve(ctx, rootRef, "")
	case "no-flight":
		payload, err := tapper.BuildOrientationPayload(nil, "No flight; full access.", "", nil, nil, &tapper.OrientationAuthority{FullAccess: true})
		if err != nil {
			return nil, err
		}
		return &mcp.Orientation{FullAccess: true, Payload: payload, ReconnectInstructions: "start a new session"}, nil
	default:
		payload, err := tapper.BuildOrientationPayload(nil, "", "", nil, nil, nil)
		if err != nil {
			return nil, err
		}
		return &mcp.Orientation{Payload: payload}, nil
	}
}

func (p *refreshFlightBackend) loadCount() int {
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	return p.loads
}

func (p *refreshFlightBackend) setLoad(mode, root string, err error) {
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	p.loadMode, p.loadErr = mode, err
	if root != "" {
		p.loadRoot = root
	}
}

func (p *refreshFlightBackend) blockNextLoad(start chan<- struct{}, wait <-chan struct{}) {
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	p.loadStart, p.loadWait = start, wait
}

func newPerCallFlightBackend() *perCallFlightBackend {
	flight := func(slug string, cover []string, capabilities ...tapper.FlightCapability) *tapper.Flight {
		f := &tapper.Flight{
			Name: "@team/+" + slug, Namespace: "team", Slug: slug, Source: "test",
			FlightManifest: tapper.FlightManifest{
				Title: slug, Visibility: tapper.FlightVisibilityPrivate,
				Capabilities: append([]tapper.FlightCapability(nil), capabilities...),
				Instructions: "instructions for " + slug,
			},
		}
		for _, alias := range cover {
			f.Cover = append(f.Cover, tapper.FlightCover{Namespace: "team", Keg: alias, Role: tapper.FlightRoleEditor})
		}
		return f
	}
	root := flight("root", []string{"root-keg"})
	child := flight("child", []string{"child-keg"}, tapper.FlightCapabilityManageKegs)
	sibling := flight("sibling", []string{"sibling-keg"})
	grandchild := flight("grandchild", []string{"grandchild-keg"})
	root.Subflights = []string{"+child", "+sibling"}
	child.Subflights = []string{"+grandchild"}
	return &perCallFlightBackend{
		root: root.Name,
		flights: map[string]*tapper.Flight{
			root.Name: root, child.Name: child, sibling.Name: sibling, grandchild.Name: grandchild,
		},
		kegs: []string{"@team/root-keg", "@team/child-keg", "@team/sibling-keg", "@team/grandchild-keg", "@team/new-keg"},
	}
}

func clonePerCallFlight(in *tapper.Flight) *tapper.Flight {
	if in == nil {
		return nil
	}
	out := *in
	out.Capabilities = append([]tapper.FlightCapability(nil), in.Capabilities...)
	out.Cover = append([]tapper.FlightCover(nil), in.Cover...)
	out.Subflights = append([]string(nil), in.Subflights...)
	return &out
}

func (p *perCallFlightBackend) Load(ctx context.Context) (*mcp.Orientation, error) {
	return p.Resolve(ctx, p.root, "")
}

func (p *perCallFlightBackend) Resolve(ctx context.Context, rootRef, selected string) (*mcp.Orientation, error) {
	p.mu.Lock()
	p.resolves++
	root := clonePerCallFlight(p.flights[rootRef])
	flights := make(map[string]*tapper.Flight, len(p.flights))
	for ref, flight := range p.flights {
		flights[ref] = clonePerCallFlight(flight)
	}
	kegs := append([]string(nil), p.kegs...)
	resolveErr := p.resolveErr
	p.mu.Unlock()
	if resolveErr != nil {
		return nil, fmt.Errorf("%w: %v", mcp.ErrOrientationUnavailable, resolveErr)
	}
	if strings.TrimSpace(rootRef) == "" && strings.TrimSpace(selected) == "" {
		authorizedKegs := make([]tapper.OrientationKeg, 0, len(kegs))
		for _, ref := range kegs {
			namespaceAlias := strings.TrimPrefix(ref, "@")
			namespace, alias, _ := strings.Cut(namespaceAlias, "/")
			authorizedKegs = append(authorizedKegs, tapper.OrientationKeg{
				Ref: ref, Namespace: namespace, Alias: alias, Role: "admin", Source: "test",
			})
		}
		orientation := &mcp.Orientation{
			Identity: "test-user-1", Kegs: authorizedKegs, AggregateKegs: authorizedKegs, FullAccess: true,
		}
		if err := mcp.FinalizeOrientation(orientation); err != nil {
			return nil, err
		}
		payload, err := tapper.BuildOrientationPayload(nil, "No flight; full access.", "", authorizedKegs, nil,
			&tapper.OrientationAuthority{FullAccess: true, Revision: orientation.Revision})
		if err != nil {
			return nil, err
		}
		orientation.Payload = payload
		return orientation, nil
	}
	if root == nil {
		return nil, fmt.Errorf("%w: pinned root %s is unavailable", mcp.ErrOrientationRootUnavailable, rootRef)
	}
	graph, err := tapper.FlattenFlightGraph(ctx, root, func(_ context.Context, ref string) (*tapper.Flight, error) {
		flight := flights[ref]
		if flight == nil {
			return nil, keg.ErrForbidden
		}
		return flight, nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", mcp.ErrOrientationUnavailable, err)
	}
	selectedFlight, path, err := graph.Select(selected)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", mcp.ErrOrientationDenied, err)
	}
	authorizedKegs := make([]tapper.OrientationKeg, 0)
	for _, ref := range kegs {
		namespaceAlias := strings.TrimPrefix(ref, "@")
		namespace, alias, _ := strings.Cut(namespaceAlias, "/")
		authorizedKegs = append(authorizedKegs, tapper.OrientationKeg{
			Ref: ref, Namespace: namespace, Alias: alias, Role: "admin", Source: "test",
		})
	}
	orientationKegs := tapper.ProjectOrientationKegs(selectedFlight, authorizedKegs)
	graphFlights := []*tapper.Flight{graph.Root}
	graphFlights = append(graphFlights, graph.Available...)
	orientation := &mcp.Orientation{
		Root: root, Flight: selectedFlight, Path: path, AvailableFlights: append([]string{root.Name}, graph.AvailableRefs()...),
		Identity: "test-user-1", Kegs: orientationKegs,
		AggregateKegs: mcp.AggregateOrientationKegs(graphFlights, authorizedKegs),
	}
	if err := mcp.FinalizeOrientation(orientation); err != nil {
		return nil, err
	}
	discovery := orientationKegs
	if strings.TrimSpace(selected) == "" {
		discovery = orientation.AggregateKegs
	}
	payload, err := tapper.BuildOrientationPayload(selectedFlight, "", "", discovery, nil, &tapper.OrientationAuthority{
		Root: root, Active: selectedFlight, Path: path, AvailableFlights: orientation.AvailableFlights,
		Revision: orientation.Revision,
	})
	if err != nil {
		return nil, err
	}
	orientation.Payload = payload
	return orientation, nil
}

func (p *perCallFlightBackend) Render(ctx context.Context, flight *tapper.Flight) (*mcp.Orientation, error) {
	if flight == nil {
		return nil, errors.New("flight is required")
	}
	return p.Resolve(ctx, flight.Name, "")
}

func (p *perCallFlightBackend) ListFlights(context.Context) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	refs := make([]string, 0, len(p.flights))
	for ref := range p.flights {
		refs = append(refs, ref)
	}
	return refs, nil
}

func (p *perCallFlightBackend) GetFlight(_ context.Context, ref string) (*tapper.Flight, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	parsed, err := tapper.ParseFlightRef(ref, "team")
	if err != nil {
		return nil, err
	}
	flight := p.flights[parsed.Canonical()]
	if flight == nil {
		return nil, keg.ErrNotExist
	}
	return clonePerCallFlight(flight), nil
}

func (p *perCallFlightBackend) CreateFlight(_ context.Context, opts tapper.CreateFlightOptions) (*tapper.Flight, error) {
	ref, err := tapper.ParseFlightRef(opts.Ref, "team")
	if err != nil {
		return nil, err
	}
	flight := &tapper.Flight{Name: ref.Canonical(), Namespace: ref.Namespace, Slug: ref.Slug, Source: "test",
		FlightManifest: tapper.FlightManifest{Title: opts.Title, Visibility: opts.Visibility, Capabilities: opts.Capabilities, Cover: opts.Cover, Subflights: opts.Subflights, Instructions: opts.Instructions}}
	p.mu.Lock()
	p.flights[flight.Name] = flight
	p.mu.Unlock()
	return clonePerCallFlight(flight), nil
}

func (p *perCallFlightBackend) UpdateFlight(_ context.Context, opts tapper.UpdateFlightOptions) (*tapper.Flight, error) {
	return p.GetFlight(context.Background(), opts.Ref)
}

func (p *perCallFlightBackend) DeleteFlight(_ context.Context, opts tapper.DeleteFlightOptions) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	ref, err := tapper.ParseFlightRef(opts.Ref, "team")
	if err != nil {
		return err
	}
	delete(p.flights, ref.Canonical())
	return nil
}

func (p *perCallFlightBackend) ListKegs(context.Context) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.kegs...), nil
}

func (p *perCallFlightBackend) CreateKeg(_ context.Context, opts tapper.CreateKegOptions) (string, error) {
	namespace := opts.Namespace
	if namespace == "" {
		namespace = "team"
	}
	ref := "@" + namespace + "/" + opts.Keg
	p.mu.Lock()
	p.created = append(p.created, ref)
	p.mu.Unlock()
	return ref, nil
}

func (p *perCallFlightBackend) SearchKegs(_ context.Context, query string) (mcp.KegSearchResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	rows := make([]tapper.OrientationKeg, 0, len(p.kegs))
	for _, ref := range p.kegs {
		namespaceAlias := strings.TrimPrefix(ref, "@")
		namespace, alias, _ := strings.Cut(namespaceAlias, "/")
		rows = append(rows, tapper.OrientationKeg{
			Ref: ref, Namespace: namespace, Alias: alias, Role: "admin", Source: "test", Visibility: "private",
			Title: alias, Summary: "summary for " + alias,
		})
	}
	return mcp.KegSearchResult{Kegs: mcp.SearchIdentityKegs(rows, query)}, nil
}

func (p *perCallFlightBackend) Identities(context.Context) ([]mcp.AuthIdentity, error) {
	return []mcp.AuthIdentity{{Hub: "test", UserID: 1, Username: "tester", DefaultNamespace: "team", Namespaces: []string{"team"}}}, nil
}

func newPerCallFlightSession(t *testing.T, backend *perCallFlightBackend) (*sdkmcp.ClientSession, context.Context) {
	t.Helper()
	ctx := context.Background()
	sandbox := newTestSandbox(t)
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sandbox.Runtime()})
	require.NoError(t, err)
	server := mcp.NewServer(tap, "test", mcp.KegDefaults{}, mcp.ServerOptions{
		OrientationProvider: backend, FlightProvider: backend, KegProvider: backend,
		KegSearchProvider: backend, IdentityProvider: backend,
	})
	return connectFlightSession(t, ctx, server, nil), ctx
}

func newRefreshFlightSession(t *testing.T, backend *refreshFlightBackend, opts *sdkmcp.ClientOptions) (*sdkmcp.ClientSession, context.Context) {
	t.Helper()
	ctx := context.Background()
	sandbox := newTestSandbox(t)
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sandbox.Runtime()})
	require.NoError(t, err)
	server := mcp.NewServer(tap, "test", mcp.KegDefaults{}, mcp.ServerOptions{
		OrientationProvider: backend, FlightProvider: backend, KegProvider: backend,
		KegSearchProvider: backend, IdentityProvider: backend,
	})
	return connectFlightSession(t, ctx, server, opts), ctx
}

func callCatKeg(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession, kegRef string) *sdkmcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "cat", Arguments: map[string]any{
		"keg": kegRef, "node_ids": []string{"0"}, "content_only": true,
	}})
	require.NoError(t, err)
	return result
}

func TestMCP_PerCallRecursiveFlightSelectionIsConcurrentAndIsolated(t *testing.T) {
	backend := newPerCallFlightBackend()
	session, ctx := newPerCallFlightSession(t, backend)
	cases := []struct {
		name   string
		flight string
		want   string
	}{
		{name: "root omitted", want: strings.Join([]string{
			"@team/child-keg\teditor\t@team/+child",
			"@team/grandchild-keg\teditor\t@team/+grandchild",
			"@team/root-keg\teditor\t@team/+root",
			"@team/sibling-keg\teditor\t@team/+sibling",
		}, "\n")},
		{name: "root explicit", flight: "@team/+root", want: "@team/root-keg\teditor\t@team/+root"},
		{name: "child", flight: "+child", want: "@team/child-keg\teditor\t@team/+child"},
		{name: "sibling", flight: "+sibling", want: "@team/sibling-keg\teditor\t@team/+sibling"},
		{name: "grandchild", flight: "+grandchild", want: "@team/grandchild-keg\teditor\t@team/+grandchild"},
	}
	var wg sync.WaitGroup
	for round := 0; round < 5; round++ {
		for _, tc := range cases {
			tc := tc
			wg.Add(1)
			go func() {
				defer wg.Done()
				arguments := map[string]any{}
				if tc.flight != "" {
					arguments["flight"] = tc.flight
				}
				result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: arguments})
				require.NoError(t, err)
				require.False(t, result.IsError, extractText(t, result))
				require.Equal(t, tc.want, strings.TrimSpace(extractText(t, result)), tc.name)
			}()
		}
	}
	wg.Wait()

	oriented, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "orient", Arguments: map[string]any{"flight": "+grandchild"}})
	require.NoError(t, err)
	require.False(t, oriented.IsError, extractText(t, oriented))
	require.Contains(t, extractText(t, oriented), "Selected flight:")
	require.Contains(t, extractText(t, oriented), "@team/+grandchild")
	require.Contains(t, extractText(t, oriented), "@team/+root")
}

func TestMCP_KegListDefaultsToLiveGraphAndExplicitFlightIsExact(t *testing.T) {
	backend := newPerCallFlightBackend()
	session, ctx := newPerCallFlightSession(t, backend)

	backend.mu.Lock()
	beforeInvalid := backend.resolves
	backend.mu.Unlock()
	invalid, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{"all": true}})
	require.NoError(t, err)
	require.True(t, invalid.IsError)
	require.Contains(t, extractText(t, invalid), "unexpected additional properties")
	backend.mu.Lock()
	require.Equal(t, beforeInvalid, backend.resolves, "invalid selection must fail before live discovery")
	backend.mu.Unlock()

	all, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, all.IsError, extractText(t, all))
	require.Equal(t, strings.Join([]string{
		"@team/child-keg\teditor\t@team/+child",
		"@team/grandchild-keg\teditor\t@team/+grandchild",
		"@team/root-keg\teditor\t@team/+root",
		"@team/sibling-keg\teditor\t@team/+sibling",
	}, "\n"), extractText(t, all))
	require.NotContains(t, extractText(t, all), "new-keg", "identity access outside the root graph must not leak")

	var structured struct {
		Kegs []struct {
			Ref     string   `json:"ref"`
			Role    string   `json:"role"`
			Flights []string `json:"flights"`
		} `json:"kegs"`
	}
	raw, err := json.Marshal(all.StructuredContent)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &structured))
	require.Len(t, structured.Kegs, 4)
	require.Equal(t, "@team/child-keg", structured.Kegs[0].Ref)
	require.Equal(t, "editor", structured.Kegs[0].Role)
	require.Equal(t, []string{"@team/+child"}, structured.Kegs[0].Flights)

	root, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{"flight": "@team/+root"}})
	require.NoError(t, err)
	require.Equal(t, "@team/root-keg\teditor\t@team/+root", extractText(t, root))
	child, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{"flight": "+child"}})
	require.NoError(t, err)
	require.Equal(t, "@team/child-keg\teditor\t@team/+child", extractText(t, child))
}

func TestAggregateOrientationKegsUsesHighestEffectiveRole(t *testing.T) {
	authorized := []tapper.OrientationKeg{
		{Ref: "@team/shared", Namespace: "team", Alias: "shared", Role: "editor"},
		{Ref: "@team/view-only", Namespace: "team", Alias: "view-only", Role: "viewer"},
	}
	root := &tapper.Flight{Name: "@team/+root", FlightManifest: tapper.FlightManifest{Cover: []tapper.FlightCover{
		{Namespace: "team", Keg: "shared", Role: tapper.FlightRoleViewer},
		{Namespace: "team", Keg: "view-only", Role: tapper.FlightRoleAdmin},
	}}}
	child := &tapper.Flight{Name: "@team/+child", FlightManifest: tapper.FlightManifest{Cover: []tapper.FlightCover{
		{Namespace: "team", Keg: "shared", Role: tapper.FlightRoleAdmin},
	}}}

	rows := mcp.AggregateOrientationKegs([]*tapper.Flight{root, child}, authorized)
	require.Len(t, rows, 2)
	require.Equal(t, "@team/shared", rows[0].Ref)
	require.Equal(t, "editor", mcp.EffectiveOrientationRole(rows[0]), "highest declared cap must still be intersected with identity role")
	require.Equal(t, []string{child.Name, root.Name}, rows[0].Flights, "every granting flight must remain visible")
	require.Equal(t, "@team/view-only", rows[1].Ref)
	require.Equal(t, "viewer", mcp.EffectiveOrientationRole(rows[1]))
	require.Equal(t, []string{root.Name}, rows[1].Flights)
}

func TestAggregateOrientationKegsMergesEqualWinningFlightProvenance(t *testing.T) {
	authorized := []tapper.OrientationKeg{{
		Ref: "@team/shared", Namespace: "team", Alias: "shared", Role: "editor",
	}}
	root := &tapper.Flight{Name: "@team/+root", FlightManifest: tapper.FlightManifest{Cover: []tapper.FlightCover{{
		Namespace: "team", Keg: "shared", Role: tapper.FlightRoleEditor,
	}}}}
	child := &tapper.Flight{Name: "@team/+child", FlightManifest: tapper.FlightManifest{Cover: []tapper.FlightCover{{
		Namespace: "team", Keg: "shared", Role: tapper.FlightRoleAdmin,
	}}}}

	rows := mcp.AggregateOrientationKegs([]*tapper.Flight{root, child, child}, authorized)
	require.Len(t, rows, 1)
	require.Equal(t, "editor", mcp.EffectiveOrientationRole(rows[0]))
	require.Equal(t, []string{"@team/+child", "@team/+root"}, rows[0].Flights)
}

func TestMCP_KegSearchIsIdentityScopedLiteralBoundedAndUngoverned(t *testing.T) {
	backend := newPerCallFlightBackend()
	session, ctx := newPerCallFlightSession(t, backend)

	found, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_search", Arguments: map[string]any{"query": "NEW-KEG"}})
	require.NoError(t, err)
	require.False(t, found.IsError, extractText(t, found))
	require.Equal(t, "@team/new-keg\tadmin\tnew-keg\tsummary for new-keg\tprivate\ttest", extractText(t, found))
	require.NotContains(t, extractText(t, found), "flight")
	raw, err := json.Marshal(found.StructuredContent)
	require.NoError(t, err)
	var structured mcp.KegSearchResult
	require.NoError(t, json.Unmarshal(raw, &structured))
	require.Equal(t, []mcp.KegSearchRow{{
		Ref: "@team/new-keg", Role: "admin", Title: "new-keg",
		Summary: "summary for new-keg", Visibility: "private", Source: "test",
	}}, structured.Kegs)

	empty, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_search", Arguments: map[string]any{"query": "   "}})
	require.NoError(t, err)
	require.True(t, empty.IsError)
	require.Contains(t, extractText(t, empty), "query must not be empty")

	rows := make([]tapper.OrientationKeg, 0, 60)
	for i := 59; i >= 0; i-- {
		alias := fmt.Sprintf("match-%02d", i)
		rows = append(rows, tapper.OrientationKeg{
			Ref: "@team/" + alias, Namespace: "team", Alias: alias,
			Title: "A match", Summary: "literal metadata", Role: "viewer",
		})
	}
	bounded := mcp.SearchIdentityKegs(rows, "MATCH")
	require.Len(t, bounded, 50)
	require.Equal(t, "@team/match-00", bounded[0].Ref)
	require.Equal(t, "@team/match-49", bounded[49].Ref)
}

func TestMCP_PerCallSelectionAdoptsGraphAuthorityAndCapabilityChanges(t *testing.T) {
	backend := newPerCallFlightBackend()
	session, ctx := newPerCallFlightSession(t, backend)

	denied, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_create", Arguments: map[string]any{"keg": "denied"}})
	require.NoError(t, err)
	require.True(t, denied.IsError)
	require.Equal(t, "ORIENTATION_DENIED", denied.StructuredContent.(map[string]any)["code"])

	created, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_create", Arguments: map[string]any{"flight": "+child", "keg": "allowed"}})
	require.NoError(t, err)
	require.False(t, created.IsError, extractText(t, created))
	backend.mu.Lock()
	require.Equal(t, []string{"@team/allowed"}, backend.created)
	backend.flights["@team/+grandchild"].Cover = []tapper.FlightCover{{Namespace: "team", Keg: "new-keg", Role: tapper.FlightRoleViewer}}
	backend.mu.Unlock()

	changed, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{"flight": "+grandchild"}})
	require.NoError(t, err)
	require.False(t, changed.IsError, extractText(t, changed))
	require.Equal(t, "@team/new-keg\tviewer\t@team/+grandchild", strings.TrimSpace(extractText(t, changed)))

	backend.mu.Lock()
	backend.flights["@team/+root"].Subflights = []string{"+sibling"}
	backend.mu.Unlock()
	delisted, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{"flight": "+grandchild"}})
	require.NoError(t, err)
	require.True(t, delisted.IsError)
	structured := delisted.StructuredContent.(map[string]any)
	require.Equal(t, "ORIENTATION_DENIED", structured["code"])
	require.Equal(t, false, structured["operationPerformed"])
	require.Equal(t, false, structured["reorientRequired"])
}

func TestMCP_PerCallDiscoveryAdoptsGraphAdditionDeletionAndTransientRecovery(t *testing.T) {
	backend := newPerCallFlightBackend()
	session, ctx := newPerCallFlightSession(t, backend)

	backend.mu.Lock()
	backend.flights["@team/+added"] = &tapper.Flight{
		Name: "@team/+added", Namespace: "team", Slug: "added", Source: "test",
		FlightManifest: tapper.FlightManifest{
			Title: "added", Visibility: tapper.FlightVisibilityPrivate,
			Cover: []tapper.FlightCover{{Namespace: "team", Keg: "new-keg", Role: tapper.FlightRoleAdmin}},
		},
	}
	backend.flights[backend.root].Subflights = append(backend.flights[backend.root].Subflights, "+added")
	backend.mu.Unlock()

	added, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, added.IsError, extractText(t, added))
	require.Contains(t, extractText(t, added), "@team/new-keg\tadmin\t@team/+added", "new descendant must be adopted without orient")

	backend.mu.Lock()
	delete(backend.flights, "@team/+child")
	backend.mu.Unlock()
	deleted, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, deleted.IsError, extractText(t, deleted))
	require.NotContains(t, extractText(t, deleted), "child-keg")
	require.NotContains(t, extractText(t, deleted), "grandchild-keg", "a deleted descendant removes its transitive branch")
	require.Contains(t, extractText(t, deleted), "@team/new-keg\tadmin\t@team/+added")
	denied, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{"flight": "+child"}})
	require.NoError(t, err)
	require.True(t, denied.IsError)
	require.Equal(t, "ORIENTATION_DENIED", denied.StructuredContent.(map[string]any)["code"])

	backend.mu.Lock()
	backend.resolveErr = errors.New("temporary Hub failure")
	backend.mu.Unlock()
	transient, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.True(t, transient.IsError)
	require.Equal(t, "ORIENTATION_UNAVAILABLE", transient.StructuredContent.(map[string]any)["code"])
	backend.mu.Lock()
	backend.resolveErr = nil
	backend.mu.Unlock()
	recovered, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, recovered.IsError, extractText(t, recovered))
	require.Contains(t, extractText(t, recovered), "@team/root-keg\teditor\t@team/+root")

	backend.mu.Lock()
	delete(backend.flights, backend.root)
	backend.mu.Unlock()
	lost, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.True(t, lost.IsError)
	require.Equal(t, "ORIENTATION_ROOT_UNAVAILABLE", lost.StructuredContent.(map[string]any)["code"])
}

func TestMCP_AuthorityBearingSchemasExposeOptionalFlightAndRejectKegListAll(t *testing.T) {
	backend := newPerCallFlightBackend()
	session, ctx := newPerCallFlightSession(t, backend)
	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	ungoverned := map[string]bool{"auth_info": true, "keg_search": true, "list_flights": true, "flight_show": true, "session_refresh": true}
	seen := map[string]bool{}
	for _, tool := range result.Tools {
		seen[tool.Name] = true
		raw, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var schema map[string]any
		require.NoError(t, json.Unmarshal(raw, &schema))
		properties, _ := schema["properties"].(map[string]any)
		_, hasFlight := properties["flight"]
		if ungoverned[tool.Name] {
			require.Falsef(t, hasFlight, "%s must remain ungoverned", tool.Name)
		} else {
			require.Truef(t, hasFlight, "%s must accept optional flight", tool.Name)
			required, _ := schema["required"].([]any)
			for _, name := range required {
				require.NotEqual(t, "flight", name, "%s flight must be optional", tool.Name)
			}
		}
		if tool.Name == "orient" {
			require.NotContains(t, properties, "subflight")
		}
		if tool.Name == "session_refresh" {
			require.Empty(t, properties, "session_refresh must remain zero-argument")
		}
		if tool.Name == "keg_search" {
			require.Contains(t, properties, "query")
			required, _ := schema["required"].([]any)
			require.Contains(t, required, "query")
		}
		_, hasAll := properties["all"]
		require.Falsef(t, hasAll, "%s must not expose removed all selection", tool.Name)
	}
	require.False(t, seen["repo_init"])
	require.True(t, seen["keg_create"], "management tools stay visible even when the root lacks capability")
	require.True(t, seen["flight_create"])

	rejected, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "session_refresh", Arguments: map[string]any{"flight": "+child"},
	})
	require.NoError(t, err)
	require.True(t, rejected.IsError)
	require.Contains(t, extractText(t, rejected), "unexpected additional properties")
}

func TestMCP_SessionRefreshNoFlightRequiresNewSession(t *testing.T) {
	backend := newRefreshFlightBackend("no-flight")
	var notifications atomic.Int64
	session, ctx := newRefreshFlightSession(t, backend, &sdkmcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *sdkmcp.ToolListChangedRequest) {
			notifications.Add(1)
		},
	})

	requireConnectionInstructions(t, session.InitializeResult().Instructions)
	require.Equal(t, 1, backend.loadCount())
	noFlightPayload := orientCall(t, session, ctx, map[string]any{})
	require.Contains(t, noFlightPayload, "No flight was provided")
	require.Equal(t, 1, backend.loadCount(), "no-flight orient must not retry activation")

	refreshed, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "session_refresh", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, refreshed.IsError, extractText(t, refreshed))
	require.Equal(t, "already_active", refreshed.StructuredContent.(map[string]any)["status"])
	require.Equal(t, false, refreshed.StructuredContent.(map[string]any)["toolsChanged"])
	require.Equal(t, "new_session", refreshed.StructuredContent.(map[string]any)["nextAction"])
	require.Equal(t, int64(0), notifications.Load())
	backend.setLoad("active", "", nil)
	unchanged, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "session_refresh", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, unchanged.IsError, extractText(t, unchanged))
	require.Equal(t, "new_session", unchanged.StructuredContent.(map[string]any)["nextAction"])
	require.Equal(t, 1, backend.loadCount(), "no-flight refresh must not consult a newly configured root")
	require.Contains(t, listedToolNames(t, ctx, session), "cat")
	require.Contains(t, orientCall(t, session, ctx, map[string]any{}), "No flight was provided")
}

func TestMCP_ActiveSessionRefreshIsProviderFreeAndKeepsPinnedRoot(t *testing.T) {
	backend := newRefreshFlightBackend("active")
	session, ctx := newRefreshFlightSession(t, backend, nil)
	require.Equal(t, 1, backend.loadCount())

	backend.setLoad("active", "@team/+sibling", nil)
	refreshed, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "session_refresh", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, refreshed.IsError, extractText(t, refreshed))
	structured := refreshed.StructuredContent.(map[string]any)
	require.Equal(t, "already_active", structured["status"])
	require.Equal(t, "@team/+root", structured["root"])
	require.Equal(t, false, structured["toolsChanged"])
	require.Equal(t, "orient", structured["nextAction"])
	require.Equal(t, 1, backend.loadCount(), "active refresh must not call the provider")

	payload := orientCall(t, session, ctx, map[string]any{})
	require.Contains(t, payload, "Launch root: `@team/+root`")
	require.NotContains(t, payload, "Launch root: `@team/+sibling`")
}

func TestMCP_FailedSessionRefreshPreservesRecoveryState(t *testing.T) {
	backend := newRefreshFlightBackend("selection")
	session, ctx := newRefreshFlightSession(t, backend, nil)
	beforeTools := listedToolNames(t, ctx, session)
	beforeOrient := orientCall(t, session, ctx, map[string]any{})

	backend.setLoad("active", "", nil)
	explicit, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "orient", Arguments: map[string]any{"flight": "+root"},
	})
	require.NoError(t, err)
	require.True(t, explicit.IsError)
	require.Contains(t, extractText(t, explicit), "session_refresh")
	resource, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{URI: "tapper://orient"})
	require.NoError(t, err)
	require.Len(t, resource.Contents, 1)
	require.Equal(t, beforeOrient, resource.Contents[0].Text)
	require.Equal(t, 1, backend.loadCount(), "recovery orient and resource reads must not retry activation")

	backend.setLoad("no-flight", "", nil)
	fallback, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "session_refresh", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.True(t, fallback.IsError)
	require.Contains(t, extractText(t, fallback), "cannot fall back to no-flight full access")
	require.Equal(t, beforeTools, listedToolNames(t, ctx, session))
	require.Equal(t, beforeOrient, orientCall(t, session, ctx, map[string]any{}))

	backend.setLoad("active", "", errors.New("temporary provider failure"))
	failed, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "session_refresh", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.True(t, failed.IsError)
	structured := failed.StructuredContent.(map[string]any)
	require.Equal(t, "SESSION_REFRESH_FAILED", structured["code"])
	require.Equal(t, "recovery", structured["mode"])
	require.Equal(t, false, structured["toolsChanged"])
	require.Equal(t, beforeTools, listedToolNames(t, ctx, session))
	require.Equal(t, beforeOrient, orientCall(t, session, ctx, map[string]any{}))

	backend.setLoad("active", "", nil)
	recovered, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "session_refresh", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, recovered.IsError, extractText(t, recovered))
	require.Equal(t, "activated", recovered.StructuredContent.(map[string]any)["status"])
}

func TestMCP_FailedSessionRefreshDoesNotBlockPublishedRecoveryOrientation(t *testing.T) {
	backend := newRefreshFlightBackend("selection")
	session, ctx := newRefreshFlightSession(t, backend, nil)
	before := orientCall(t, session, ctx, map[string]any{})

	started := make(chan struct{})
	release := make(chan struct{})
	backend.setLoad("active", "", errors.New("temporary provider failure"))
	backend.blockNextLoad(started, release)
	refreshDone := make(chan *sdkmcp.CallToolResult, 1)
	go func() {
		result, _ := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "session_refresh", Arguments: map[string]any{}})
		refreshDone <- result
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("session refresh did not reach the provider")
	}
	require.Equal(t, before, orientCall(t, session, ctx, map[string]any{}),
		"published recovery orientation must remain available while refresh is in flight")
	close(release)

	select {
	case failed := <-refreshDone:
		require.NotNil(t, failed)
		require.True(t, failed.IsError)
		require.Equal(t, "SESSION_REFRESH_FAILED", failed.StructuredContent.(map[string]any)["code"])
	case <-time.After(time.Second):
		t.Fatal("session refresh did not finish")
	}
	require.Equal(t, before, orientCall(t, session, ctx, map[string]any{}))
}
