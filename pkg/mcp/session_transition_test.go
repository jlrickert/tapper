package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type fakeSessionBackend struct {
	mu          sync.Mutex
	flights     map[string]*tapper.Flight
	active      string
	createdKegs []string
	renderErr   error
	listEnter   chan struct{}
	listWait    chan struct{}
}

func newFakeSessionBackend() *fakeSessionBackend {
	active := transitionFlight("active", []tapper.FlightCapability{tapper.FlightCapabilityManageFlights}, "personal", "initial")
	other := transitionFlight("other", nil, "other", "other")
	return &fakeSessionBackend{
		flights: map[string]*tapper.Flight{active.Name: active, other.Name: other},
		active:  active.Name,
	}
}

func transitionFlight(slug string, capabilities []tapper.FlightCapability, keg, instructions string) *tapper.Flight {
	return &tapper.Flight{
		Name: "@local/+" + slug, Namespace: "local", Slug: slug, Source: "test",
		FlightManifest: tapper.FlightManifest{
			Title: slug, Visibility: tapper.FlightVisibilityPrivate,
			Capabilities: append([]tapper.FlightCapability(nil), capabilities...),
			Cover:        []tapper.FlightCover{{Namespace: "local", Keg: keg, Role: tapper.FlightRoleEditor}},
			Instructions: instructions,
		},
	}
}

func copyTransitionFlight(in *tapper.Flight) *tapper.Flight {
	if in == nil {
		return nil
	}
	out := *in
	out.Capabilities = append([]tapper.FlightCapability(nil), in.Capabilities...)
	out.Cover = append([]tapper.FlightCover(nil), in.Cover...)
	return &out
}

func (p *fakeSessionBackend) Load(ctx context.Context) (*mcp.Orientation, error) {
	p.mu.Lock()
	flight := copyTransitionFlight(p.flights[p.active])
	p.mu.Unlock()
	if flight == nil {
		payload, err := tapper.BuildOrientationPayload(nil, "", nil, nil)
		return &mcp.Orientation{Payload: payload}, err
	}
	return p.Render(ctx, flight)
}

func (p *fakeSessionBackend) Render(_ context.Context, flight *tapper.Flight) (*mcp.Orientation, error) {
	p.mu.Lock()
	err := p.renderErr
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	kegs := []tapper.OrientationKeg{{
		Ref: "@local/personal", Namespace: "local", Alias: "personal", Title: "Personal", Role: "admin", Source: "local", FlightCap: "editor",
	}, {Ref: "@local/other", Namespace: "local", Alias: "other", Title: "Other", Role: "admin", Source: "local", FlightCap: "editor"}}
	payload, err := tapper.BuildOrientationPayload(flight, "", kegs, nil)
	if err != nil {
		return nil, err
	}
	return &mcp.Orientation{Flight: copyTransitionFlight(flight), Payload: payload, Kegs: kegs}, nil
}

func (p *fakeSessionBackend) ListFlights(context.Context) ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.flights))
	for ref := range p.flights {
		out = append(out, ref)
	}
	return out, nil
}

func (p *fakeSessionBackend) GetFlight(_ context.Context, ref string) (*tapper.Flight, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	parsed, err := tapper.ParseFlightRef(ref, "local")
	if err != nil {
		return nil, err
	}
	flight := p.flights[parsed.Canonical()]
	if flight == nil {
		return nil, errors.New("flight not found")
	}
	return copyTransitionFlight(flight), nil
}

func (p *fakeSessionBackend) CreateFlight(_ context.Context, opts tapper.CreateFlightOptions) (*tapper.Flight, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ref, err := tapper.ParseFlightRef(opts.Ref, "local")
	if err != nil {
		return nil, err
	}
	flight := transitionFlight(ref.Slug, opts.Capabilities, "personal", opts.Instructions)
	flight.Title, flight.Visibility, flight.Cover = opts.Title, opts.Visibility, append([]tapper.FlightCover(nil), opts.Cover...)
	p.flights[flight.Name] = flight
	return copyTransitionFlight(flight), nil
}

func (p *fakeSessionBackend) UpdateFlight(_ context.Context, opts tapper.UpdateFlightOptions) (*tapper.Flight, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ref, err := tapper.ParseFlightRef(opts.Ref, "local")
	if err != nil {
		return nil, err
	}
	current := p.flights[ref.Canonical()]
	if current == nil {
		return nil, errors.New("flight not found")
	}
	next := copyTransitionFlight(current)
	if opts.Title != nil {
		next.Title = *opts.Title
	}
	if opts.Visibility != nil {
		next.Visibility = *opts.Visibility
	}
	if opts.Capabilities != nil {
		next.Capabilities = append([]tapper.FlightCapability(nil), (*opts.Capabilities)...)
	}
	if opts.Instructions != nil {
		next.Instructions = *opts.Instructions
	}
	if opts.Cover != nil {
		next.Cover = append([]tapper.FlightCover(nil), (*opts.Cover)...)
	}
	p.flights[next.Name] = next
	return copyTransitionFlight(next), nil
}

func (p *fakeSessionBackend) DeleteFlight(_ context.Context, opts tapper.DeleteFlightOptions) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	ref, err := tapper.ParseFlightRef(opts.Ref, "local")
	if err != nil {
		return err
	}
	delete(p.flights, ref.Canonical())
	return nil
}

func (p *fakeSessionBackend) ListKegs(context.Context) ([]string, error) {
	if p.listEnter != nil {
		select {
		case p.listEnter <- struct{}{}:
		default:
		}
		<-p.listWait
	}
	return []string{"@local/personal", "@local/other"}, nil
}

func (p *fakeSessionBackend) CreateKeg(_ context.Context, opts tapper.CreateKegOptions) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ns := opts.Namespace
	if ns == "" {
		ns = "local"
	}
	ref := "@" + ns + "/" + opts.Keg
	p.createdKegs = append(p.createdKegs, ref)
	return ref, nil
}

func (p *fakeSessionBackend) Identities(context.Context) ([]mcp.AuthIdentity, error) {
	return []mcp.AuthIdentity{{Hub: "test", UserID: 1, Username: "tester", DefaultNamespace: "local", Namespaces: []string{"local"}}}, nil
}

func newTransitionSession(t *testing.T, provider *fakeSessionBackend, opts *sdkmcp.ClientOptions) (*sdkmcp.ClientSession, context.Context) {
	t.Helper()
	ctx := context.Background()
	sb := newTestSandbox(t)
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{}, mcp.ServerOptions{
		OrientationProvider: provider, FlightProvider: provider, KegProvider: provider, IdentityProvider: provider,
	})
	return connectFlightSession(t, ctx, srv, opts), ctx
}

func TestMCP_SelfEditImmediatelyAdoptsManifestAndCapabilities(t *testing.T) {
	provider := newFakeSessionBackend()
	var notifications atomic.Int64
	session, ctx := newTransitionSession(t, provider, &sdkmcp.ClientOptions{ToolListChangedHandler: func(context.Context, *sdkmcp.ToolListChangedRequest) { notifications.Add(1) }})
	require.False(t, callCat(t, ctx, session).IsError)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "flight_edit", Arguments: map[string]any{
		"ref": "+active", "instructions": "updated immediately", "cover": []string{"@local/other=editor"},
	}})
	require.NoError(t, err)
	require.False(t, res.IsError, extractText(t, res))
	require.Contains(t, extractText(t, res), "updated immediately")
	require.True(t, callCat(t, ctx, session).IsError, "cover change must govern the next call")

	res, err = session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "flight_edit", Arguments: map[string]any{
		"ref": "@local/+active", "capabilities": []string{},
	}})
	require.NoError(t, err)
	require.False(t, res.IsError, extractText(t, res))
	require.NotContains(t, listedToolNames(t, ctx, session), "flight_edit")
	require.Eventually(t, func() bool { return notifications.Load() > 0 }, time.Second, 10*time.Millisecond)
}

func TestMCP_SelfDeleteEntersRecoveryAndNotifies(t *testing.T) {
	provider := newFakeSessionBackend()
	var notifications atomic.Int64
	session, ctx := newTransitionSession(t, provider, &sdkmcp.ClientOptions{ToolListChangedHandler: func(context.Context, *sdkmcp.ToolListChangedRequest) { notifications.Add(1) }})
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "flight_delete", Arguments: map[string]any{"ref": "+active"}})
	require.NoError(t, err)
	require.False(t, res.IsError, extractText(t, res))
	require.ElementsMatch(t, []string{"orient", "list_flights", "flight_show", "auth_info"}, listedToolNames(t, ctx, session))
	require.Eventually(t, func() bool { return notifications.Load() > 0 }, time.Second, 10*time.Millisecond)
}

func TestMCP_SelfEditRenderFailureReportsAppliedAndRecovers(t *testing.T) {
	provider := newFakeSessionBackend()
	provider.renderErr = errors.New("render unavailable")
	// Initialization must succeed; arm the failure afterward.
	provider.renderErr = nil
	session, ctx := newTransitionSession(t, provider, nil)
	provider.mu.Lock()
	provider.renderErr = errors.New("render unavailable")
	provider.mu.Unlock()
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "flight_edit", Arguments: map[string]any{"ref": "+active", "instructions": "persisted"}})
	require.NoError(t, err)
	require.False(t, res.IsError, extractText(t, res))
	require.Contains(t, extractText(t, res), "update was applied")
	require.ElementsMatch(t, []string{"orient", "list_flights", "flight_show", "auth_info"}, listedToolNames(t, ctx, session))
	stored, err := provider.GetFlight(ctx, "+active")
	require.NoError(t, err)
	require.Equal(t, "persisted", stored.Instructions)
}

func TestMCP_NonSelfMutationKeepsSessionAuthority(t *testing.T) {
	provider := newFakeSessionBackend()
	session, ctx := newTransitionSession(t, provider, nil)
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "flight_edit", Arguments: map[string]any{"ref": "+other", "instructions": "changed other"}})
	require.NoError(t, err)
	require.False(t, res.IsError, extractText(t, res))
	require.False(t, callCat(t, ctx, session).IsError)
	require.Contains(t, session.InitializeResult().Instructions, "initial")
}

func TestMCP_SelfTransitionWaitsForOlderInFlightCall(t *testing.T) {
	provider := newFakeSessionBackend()
	provider.listEnter, provider.listWait = make(chan struct{}, 1), make(chan struct{})
	session, ctx := newTransitionSession(t, provider, nil)
	listDone := make(chan struct{})
	go func() {
		_, _ = session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{}})
		close(listDone)
	}()
	<-provider.listEnter
	editDone := make(chan struct{})
	go func() {
		_, _ = session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "flight_edit", Arguments: map[string]any{"ref": "+active", "instructions": "after wait"}})
		close(editDone)
	}()
	select {
	case <-editDone:
		t.Fatal("self transition returned before the older call released its authority snapshot")
	case <-time.After(50 * time.Millisecond):
	}
	close(provider.listWait)
	select {
	case <-listDone:
	case <-time.After(time.Second):
		t.Fatal("keg_list did not finish")
	}
	select {
	case <-editDone:
	case <-time.After(time.Second):
		t.Fatal("flight_edit did not finish")
	}
}

func TestMCP_ProviderInjectionPreservesToolAndResourceContract(t *testing.T) {
	local, localCtx := newTestSession(t)
	hosted, hostedCtx := newTransitionSession(t, newFakeSessionBackend(), nil)

	type toolContract struct {
		Name        string
		InputSchema string
		Annotations string
	}
	tools := func(session *sdkmcp.ClientSession, ctx context.Context) []toolContract {
		listed, err := session.ListTools(ctx, nil)
		require.NoError(t, err)
		out := make([]toolContract, 0, len(listed.Tools))
		for _, tool := range listed.Tools {
			input, err := json.Marshal(tool.InputSchema)
			require.NoError(t, err)
			annotations, err := json.Marshal(tool.Annotations)
			require.NoError(t, err)
			out = append(out, toolContract{Name: tool.Name, InputSchema: string(input), Annotations: string(annotations)})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out
	}
	require.Equal(t, tools(local, localCtx), tools(hosted, hostedCtx))

	resources := func(session *sdkmcp.ClientSession, ctx context.Context) []string {
		listed, err := session.ListResources(ctx, nil)
		require.NoError(t, err)
		out := make([]string, 0, len(listed.Resources))
		for _, resource := range listed.Resources {
			out = append(out, resource.URI+"|"+resource.Name+"|"+resource.MIMEType)
		}
		sort.Strings(out)
		return out
	}
	require.Equal(t, resources(local, localCtx), resources(hosted, hostedCtx))

	templates := func(session *sdkmcp.ClientSession, ctx context.Context) []string {
		listed, err := session.ListResourceTemplates(ctx, nil)
		require.NoError(t, err)
		out := make([]string, 0, len(listed.ResourceTemplates))
		for _, resource := range listed.ResourceTemplates {
			out = append(out, resource.URITemplate+"|"+resource.Name+"|"+resource.MIMEType)
		}
		sort.Strings(out)
		return out
	}
	require.Equal(t, templates(local, localCtx), templates(hosted, hostedCtx))
}

func TestMCP_AuthInfoReportsMultipleLocalHubIdentitiesWithoutSecrets(t *testing.T) {
	ctx := context.Background()
	sb := newTestSandbox(t)
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	store := &tapper.AuthStore{}
	store.Set("https://one.example", tapper.AuthEntry{AccessToken: "secret-one", Scope: "admin", RefreshToken: "refresh-one"})
	store.Set("https://two.example", tapper.AuthEntry{AccessToken: "secret-two", Scope: "viewer"})
	require.NoError(t, store.Save(ctx, sb.Runtime(), tap.PathService.AuthStorePath()))
	tap.AuthValidateFn = func(_ context.Context, _ *toolkit.Runtime, hubURL, _ string) (*tapper.WhoAmI, error) {
		if hubURL == "https://one.example" {
			return &tapper.WhoAmI{UserID: 1, Username: "one", DisplayName: "One User", Email: "one@example.test", DefaultNamespace: "one", Namespaces: []string{"team", "one"}}, nil
		}
		return &tapper.WhoAmI{UserID: 2, Username: "two", DisplayName: "Two User", Email: "two@example.test", DefaultNamespace: "two", Namespaces: []string{"two"}}, nil
	}
	kegs := newFakeSessionBackend()
	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{KegTargetOptions: tapper.KegTargetOptions{Flight: "@local/+test"}}, mcp.ServerOptions{KegProvider: kegs})
	session := connectFlightSession(t, ctx, srv, nil)
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "auth_info", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, res.IsError, extractText(t, res))
	var structured struct {
		Identities []mcp.AuthIdentity `json:"identities"`
		Kegs       []string           `json:"kegs"`
	}
	raw, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &structured))
	require.Len(t, structured.Identities, 2)
	require.Equal(t, "https://one.example", structured.Identities[0].Hub)
	require.Equal(t, []string{"one", "team"}, structured.Identities[0].Namespaces)
	require.Equal(t, "https://two.example", structured.Identities[1].Hub)
	combined := strings.ToLower(extractText(t, res) + "\n" + string(raw))
	for _, secret := range []string{"secret-one", "secret-two", "refresh-one", "one@example.test", "two@example.test", "scope", "expires", "cookie", "session"} {
		require.NotContains(t, combined, secret)
	}
}

func TestMCP_DoctorInspectsOnlySelectedKeg(t *testing.T) {
	ctx := context.Background()
	sb := newTestSandbox(t)
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte("hubs: [invalid\n"), 0o600))
	_, _ = tap.ConfigService.Config()
	require.NotEmpty(t, tap.DoctorConfig(), "fixture must contain a local configuration issue")
	k := keg.NewLocalKeg(keg.NewMemoryRepo(sb.Runtime()), sb.Runtime())
	require.NoError(t, k.Init(ctx))
	k.SetTarget(&keg.Target{Namespace: "local", KegName: "personal"})
	tap.KegResolver = func(context.Context, tapper.KegTargetOptions, tapper.FlightRole) (keg.Keg, error) { return k, nil }
	backend := newFakeSessionBackend()
	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{}, mcp.ServerOptions{
		OrientationProvider: backend, FlightProvider: backend, KegProvider: backend, IdentityProvider: backend,
	})
	session := connectFlightSession(t, ctx, srv, nil)
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "doctor", Arguments: map[string]any{"keg": "@local/personal"}})
	require.NoError(t, err)
	require.False(t, res.IsError, extractText(t, res))
	require.Equal(t, "ok: keg is healthy", extractText(t, res))
}
