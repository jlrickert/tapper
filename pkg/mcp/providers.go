package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/jlrickert/tapper/pkg/apicontract"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapapi"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// Orientation is one complete MCP authority candidate.
type Orientation struct {
	Root             *tapper.Flight
	Flight           *tapper.Flight
	Path             []string
	AvailableFlights []string
	Identity         string
	Revision         string
	// RootHub binds the proof to this connection's validating Hub.
	RootHub        string
	AllowedTargets []string
	Payload        string
	Kegs           []tapper.OrientationKeg
	// AggregateKegs is the pinned root plus every accessible transitive
	// descendant, merged by highest effective role. It exists only on a live
	// per-call candidate and is never published into shared session state.
	AggregateKegs []tapper.OrientationKeg
	Warnings      []string
	// FullAccess marks an ungoverned no-flight candidate. It uses the identity's
	// real KEG roles, publishes the complete tool inventory, and emits no Hub
	// orientation header.
	FullAccess            bool
	ReconnectInstructions string
}

// FlightOrientationProvider refreshes one connection-pinned root and selects the root
// or an identity-accessible transitive descendant for one call.
type FlightOrientationProvider interface {
	// Resolve reloads the pinned root's live graph and selects one flight for
	// the current call; an empty selection resolves to the root.
	Resolve(context.Context, string, string) (*Orientation, error)
}

// OrientationProvider owns transport-specific flight selection and rendering.
type OrientationProvider interface {
	// Load selects the pinned root and renders it into a complete candidate.
	// It is the transport's reload boundary: whatever "which flight am I on"
	// depends on is re-read here and nowhere else.
	Load(context.Context) (*Orientation, error)
	// Render renders the exact supplied manifest. It must not consult a mutable
	// selection again, because its caller has already decided which flight is
	// authoritative and is only asking for the payload.
	Render(context.Context, *tapper.Flight) (*Orientation, error)
}

// FlightProvider supplies identity-authorized flight discovery and mutation.
// Session capability checks are enforced independently by the MCP gate, so
// implementations apply only their own transport's authorization.
type FlightProvider interface {
	// ListFlights returns the canonical refs of every flight this identity can see.
	ListFlights(context.Context) ([]string, error)
	// GetFlight resolves one flight by ref.
	GetFlight(context.Context, string) (*tapper.Flight, error)
	// CreateFlight persists a new flight and returns the stored manifest.
	CreateFlight(context.Context, tapper.CreateFlightOptions) (*tapper.Flight, error)
	// UpdateFlight applies a partial edit and returns the stored manifest. The
	// every subsequent authority-bearing call resolves the live graph again.
	UpdateFlight(context.Context, tapper.UpdateFlightOptions) (*tapper.Flight, error)
	// DeleteFlight removes a flight.
	DeleteFlight(context.Context, tapper.DeleteFlightOptions) error
}

// KegDiscoveryProvider reports the kegs an identity can reach and creates new
// ones. Creation lives here rather than on a keg-agnostic surface because both
// operations answer to the same authenticated catalog.
type KegDiscoveryProvider interface {
	// ListKegs returns every identity-authorized canonical keg ref. MCP applies
	// the call-selected flight cover before releasing results, so
	// implementations do not filter by flight themselves.
	ListKegs(context.Context) ([]string, error)
	// CreateKeg provisions a keg and returns its canonical @namespace/keg ref.
	// The MCP gate has already checked that the session is active; whether it
	// may call keg_create at all is its agent's tools. Implementations apply
	// their own transport's identity authorization.
	CreateKeg(context.Context, tapper.CreateKegOptions) (string, error)
}

// AgentProvider reads and manages Hub agents: a namespace-owned model,
// instructions, and tool allowlist. Implementations apply the caller's
// namespace role (members read, owners and admins write); agent refs are
// @namespace/name.
type AgentProvider interface {
	// ListAgents returns a namespace's agents, or with an empty namespace the
	// agents of every namespace the caller belongs to.
	ListAgents(ctx context.Context, namespace string) ([]tapper.HubAgent, error)
	// GetAgent returns one agent, including its instructions.
	GetAgent(ctx context.Context, ref string) (*tapper.HubAgent, error)
	// CreateAgent creates an agent and returns it as stored.
	CreateAgent(context.Context, tapper.CreateAgentOptions) (*tapper.HubAgent, error)
	// EditAgent applies the set fields and returns the agent as stored.
	EditAgent(context.Context, tapper.EditAgentOptions) (*tapper.HubAgent, error)
	// DeleteAgent removes an agent.
	DeleteAgent(ctx context.Context, ref string) error
}

// NamespaceProvider lists the caller's own namespaces. The hub-wide people
// directory is ExploreProvider's. Listing never grants authority.
type NamespaceProvider interface {
	// ListNamespaces returns the namespaces the caller belongs to, with role.
	ListNamespaces(context.Context) ([]tapper.HubNamespace, error)
}

// NodeSearchProvider ranks nodes by relevance across the KEGs a call may
// read. opts.Keg names one KEG; otherwise the search covers opts.Kegs, narrowed
// to the call's flight cover (opts.FlightContext), or with neither, every KEG
// the caller may read. The node_search tool has already run opts.Validate.
type NodeSearchProvider interface {
	// SearchNodes returns one ranked page for opts.Query.
	SearchNodes(ctx context.Context, opts tapper.SearchOptions) (*tapapi.SearchResponse, error)
}

type localNodeSearchProvider struct{ tap *tapper.Tap }

func (p localNodeSearchProvider) SearchNodes(ctx context.Context, opts tapper.SearchOptions) (*tapapi.SearchResponse, error) {
	return p.tap.Search(ctx, opts)
}

// AuthIdentity is deliberately credential-free. Do not add token, email,
// scope, cookie, expiry, or session fields to this MCP wire shape.
type AuthIdentity struct {
	Hub              string   `json:"hub"`
	UserID           int64    `json:"user_id"`
	Username         string   `json:"username"`
	DisplayName      string   `json:"display_name,omitempty"`
	DefaultNamespace string   `json:"default_namespace"`
	Namespaces       []string `json:"namespaces"`
}

// CanonicalOrientationIdentity returns transport-neutral revision material for
// the authenticated identity on the pinned root's Hub. Hub routing aliases and
// credentials are deliberately absent so stdio and hosted MCP hash the same
// authority while unrelated Hub logins cannot stale a call.
func CanonicalOrientationIdentity(identity AuthIdentity) (string, error) {
	type revisionIdentity struct {
		UserID           int64    `json:"user_id"`
		Username         string   `json:"username"`
		DisplayName      string   `json:"display_name,omitempty"`
		DefaultNamespace string   `json:"default_namespace"`
		Namespaces       []string `json:"namespaces"`
	}
	namespaces := append([]string(nil), identity.Namespaces...)
	sort.Strings(namespaces)
	raw, err := json.Marshal(revisionIdentity{
		UserID: identity.UserID, Username: identity.Username,
		DisplayName: identity.DisplayName, DefaultNamespace: identity.DefaultNamespace,
		Namespaces: namespaces,
	})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// IdentityProvider reports who the session is authenticated as.
type IdentityProvider interface {
	// Identities returns the authenticated identities, without credentials.
	// Local MCP reports the selected Hub login; hosted MCP reports the one
	// authenticated account.
	Identities(context.Context) ([]AuthIdentity, error)
}

type localOrientationProvider struct {
	tap          *tapper.Tap
	staticFlight string
}

// localUnpinnedInstructions is the stdio nudge for an unpinned connection.
// The no-flight state stays active for the connection lifetime; configuration
// changes intentionally take effect only after the host starts a new session.
func localUnpinnedInstructions(skipped []string) string {
	return "To restrict this connection, pin a least-privilege flight outside MCP using Tapper configuration or TAP_FLIGHT, then disconnect and start a new one. `session_refresh` cannot change no-flight authority."
}

func localUnpinnedReconnect() string {
	return "Create a least-privilege flight, pin it outside MCP with Tapper configuration or `tap mcp --flight`, then disconnect and start a new MCP connection; this connection remains on no-flight full access."
}

func (p *localOrientationProvider) Load(ctx context.Context) (*Orientation, error) {
	if p.tap == nil || p.tap.ConfigService == nil || p.tap.FlightService == nil {
		return nil, errors.New("Tapper flight service is unavailable")
	}
	// Adoption is the reload boundary for both session kinds: config-driven
	// sessions re-resolve their selection here, and launcher-bound sessions keep
	// their connection-pinned --flight but still need fresh hub routing and credentials.
	// Configuration is otherwise fixed for the life of the process, so this is
	// where an edit made outside the session takes effect.
	if err := p.tap.ConfigService.PinHub(); err != nil {
		return nil, err
	}
	if s := apicontract.FromContext(ctx); s != nil {
		_, hub, err := p.tap.ConfigService.SelectedHub("")
		if err != nil {
			return nil, err
		}
		if err := s.Check(ctx, http.DefaultClient, hub.URL); err != nil {
			return nil, err
		}
	}
	p.tap.ConfigService.Reload()
	p.tap.KegService.ReloadAuthStore()
	ref := strings.TrimSpace(p.staticFlight)
	if ref == "" {
		ref = p.tap.ActiveFlightName("")
	}
	if agentRef := strings.TrimSpace(p.tap.Runtime.Env().Get("TAP_AGENT")); agentRef != "" {
		agent, err := p.tap.HubAgent(ctx, agentRef)
		if err != nil {
			return nil, err
		}
		if ref == "" {
			ref = strings.TrimSpace(agent.Flight)
		}
		if ref == "" {
			return nil, fmt.Errorf("agent %s has no flight, so it has no KEG access: %w", agentRef, ErrOrientationDenied)
		}
	}
	if strings.TrimSpace(ref) == "" {
		return p.resolveUnpinned(ctx, "")
	}
	pre := p.prefetchOrientation(ctx, true)
	defer pre.wait()
	flight, err := p.tap.FlightService.GetFlightFresh(ctx, ref)
	if err != nil {
		return nil, err
	}
	return p.resolve(ctx, flight, "", pre)
}

func (p *localOrientationProvider) Resolve(ctx context.Context, rootRef, selected string) (*Orientation, error) {
	if p.tap == nil || p.tap.ConfigService == nil || p.tap.FlightService == nil {
		return nil, errors.New("Tapper flight service is unavailable")
	}
	if err := p.tap.ConfigService.PinHub(); err != nil {
		return nil, err
	}
	if s := apicontract.FromContext(ctx); s != nil {
		_, hub, err := p.tap.ConfigService.SelectedHub("")
		if err != nil {
			return nil, err
		}
		if err := s.Check(ctx, http.DefaultClient, hub.URL); err != nil {
			return nil, err
		}
	}
	p.tap.ConfigService.Reload()
	p.tap.KegService.ReloadAuthStore()
	if strings.TrimSpace(rootRef) == "" {
		return p.resolveUnpinned(ctx, selected)
	}
	pre := p.prefetchOrientation(ctx, true)
	defer pre.wait()
	root, err := p.tap.FlightService.GetFlightFresh(ctx, rootRef)
	if err != nil {
		if errors.Is(err, keg.ErrNotExist) || errors.Is(err, keg.ErrForbidden) || errors.Is(err, keg.ErrUnauthorized) {
			return nil, fmt.Errorf("%w: launch root %q is no longer available: %w", ErrOrientationRootUnavailable, rootRef, err)
		}
		return nil, fmt.Errorf("%w: refresh launch root %q: %w", ErrOrientationUnavailable, rootRef, err)
	}
	return p.resolve(ctx, root, selected, pre)
}

func (p *localOrientationProvider) resolveUnpinned(ctx context.Context, selected string) (*Orientation, error) {
	// The flight catalog, the KEG catalog, the selected flight, and the Hub
	// identity are independent, so they load together.
	pinned := strings.TrimSpace(selected) != ""
	pre := p.prefetchOrientation(ctx, pinned)
	defer pre.wait()
	var (
		active  *tapper.Flight
		loadErr error
		loaded  sync.WaitGroup
	)
	if pinned {
		loaded.Add(1)
		go func() {
			defer loaded.Done()
			active, loadErr = p.tap.FlightService.GetFlightFresh(ctx, selected)
		}()
	}
	defer loaded.Wait()
	var warnings []string
	catalog, err := p.tap.FlightService.FlightCatalog(ctx, "", &warnings)
	available := make([]string, 0, len(catalog))
	for _, row := range catalog {
		available = append(available, row.Name)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: list identity-accessible flights: %w", ErrOrientationUnavailable, err)
	}
	authorized, kegWarnings, err := pre.catalog()
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, kegWarnings...)
	if strings.TrimSpace(selected) == "" {
		orientation := &Orientation{
			AvailableFlights: append([]string(nil), available...), Kegs: authorized,
			AggregateKegs: append([]tapper.OrientationKeg(nil), authorized...), Warnings: warnings,
			FullAccess: true, ReconnectInstructions: localUnpinnedReconnect(),
		}
		if err := FinalizeOrientation(orientation); err != nil {
			return nil, err
		}
		authority := &tapper.OrientationAuthority{FullAccess: true, AvailableFlights: available, Revision: orientation.Revision, Agent: p.tap.ActiveAgent()}
		payload, err := tapper.BuildOrientationPayload(nil, localUnpinnedInstructions(warnings), p.tap.ActiveLaunch(), authorized, warnings, authority)
		if err != nil {
			return nil, err
		}
		orientation.Payload = payload
		return orientation, nil
	}
	loaded.Wait()
	if loadErr != nil {
		return nil, fmt.Errorf("%w: selected flight %q is unavailable: %w", ErrOrientationDenied, selected, loadErr)
	}
	allowed := false
	for _, ref := range available {
		if ref == active.Name {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("%w: selected flight %q is not identity-accessible", ErrOrientationDenied, selected)
	}
	active = tapper.ComposedFlight(active)
	kegs := tapper.ProjectOrientationKegs(active, authorized)
	orientation := &Orientation{
		Root: active, Flight: active, Path: []string{active.Name}, AvailableFlights: append([]string(nil), available...),
		Kegs: kegs, AggregateKegs: append([]tapper.OrientationKeg(nil), kegs...), Warnings: warnings,
	}
	if orientation.Root != nil {
		if err := p.bindRootAuthority(ctx, orientation, pre); err != nil {
			return nil, err
		}
	}
	if err := FinalizeOrientation(orientation); err != nil {
		return nil, err
	}
	authority := &tapper.OrientationAuthority{
		Active: active, Path: orientation.Path, AvailableFlights: available, Revision: orientation.Revision,
		Children:   tapper.ImmediateFlightChildren(active, catalog),
		FullAccess: true,
		Agent:      p.tap.ActiveAgent(),
	}
	payload, err := tapper.BuildOrientationPayload(active, "", p.tap.ActiveLaunch(), kegs, warnings, authority)
	if err != nil {
		return nil, err
	}
	orientation.Payload = payload
	return orientation, nil
}

func (p *localOrientationProvider) orientationIdentityForSource(ctx context.Context, source string) (string, error) {
	identities, err := (localIdentityProvider{tap: p.tap}).Identities(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: load selected Hub identity: %w", ErrOrientationUnavailable, err)
	}
	cfg, err := p.tap.ConfigService.Config()
	if err != nil {
		return "", fmt.Errorf("%w: resolve selected Hub %q: %w", ErrOrientationUnavailable, source, err)
	}
	hubURL := tapper.CanonicalConfiguredHubURL(source)
	if entry, ok := cfg.Hub(source); ok && strings.TrimSpace(entry.URL) != "" {
		hubURL = tapper.CanonicalConfiguredHubURL(entry.URL)
	}
	for _, identity := range identities {
		if tapper.CanonicalConfiguredHubURL(identity.Hub) != hubURL {
			continue
		}
		canonical, err := CanonicalOrientationIdentity(identity)
		if err != nil {
			return "", fmt.Errorf("%w: canonicalize selected Hub identity: %w", ErrOrientationUnavailable, err)
		}
		return canonical, nil
	}
	return "", fmt.Errorf("%w: authenticated identity for selected Hub %q is unavailable", ErrOrientationUnavailable, source)
}

// resolve loads the flight graph rooted at root and selects from it. pre
// carries the KEG catalog and Hub identity, already loading alongside the
// graph.
func (p *localOrientationProvider) resolve(ctx context.Context, root *tapper.Flight, selected string, pre *orientationPrefetch) (*Orientation, error) {
	graph, err := p.tap.FlightService.ResolveFlightGraph(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("%w: load flight graph rooted at %s: %w", ErrOrientationUnavailable, root.Name, err)
	}
	root = graph.Root
	active, path, err := graph.Select(selected)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrOrientationDenied, err)
	}
	active, err = p.tap.FlightService.GetFlightFresh(ctx, active.Name)
	if err != nil {
		return nil, fmt.Errorf("%w: load active composed cover: %w", ErrOrientationUnavailable, err)
	}
	active = tapper.ComposedFlight(active)
	authorized, warnings, err := pre.catalog()
	if err != nil {
		return nil, err
	}
	kegs := tapper.ProjectOrientationKegs(active, authorized)
	for i := range kegs {
		for _, row := range active.EffectiveCover {
			if row.Ref == kegs[i].Ref {
				kegs[i].CoverSource = row.Source
				kegs[i].Distance = row.Distance
			}
		}
	}

	aggregate := AggregateOrientationKegs(graphFlights(graph), authorized)
	orientation := &Orientation{
		Root: root, Flight: active, Path: path, AvailableFlights: selectableFlightRefs(graph),
		Kegs: kegs, AggregateKegs: aggregate, Warnings: warnings,
	}
	if orientation.Root != nil {
		if err := p.bindRootAuthority(ctx, orientation, pre); err != nil {
			return nil, err
		}
	}
	if err := FinalizeOrientation(orientation); err != nil {
		return nil, err
	}
	authority := &tapper.OrientationAuthority{
		Root: root, Active: active, Path: path, AvailableFlights: orientation.AvailableFlights,
		Children: tapper.ImmediateFlightChildren(active, graphFlights(graph)),
		Revision: orientation.Revision,
		Agent:    p.tap.ActiveAgent(),
	}
	discovery := kegs
	if strings.TrimSpace(selected) == "" {
		discovery = aggregate
	}
	payload, err := tapper.BuildOrientationPayload(active, "", p.tap.ActiveLaunch(), discovery, warnings, authority)
	if err != nil {
		return nil, err
	}
	orientation.Payload = payload
	return orientation, nil
}

func (p *localOrientationProvider) Render(ctx context.Context, flight *tapper.Flight) (*Orientation, error) {
	pre := p.prefetchOrientation(ctx, flight != nil)
	defer pre.wait()
	authorized, warnings, err := pre.catalog()
	if err != nil {
		return nil, err
	}
	kegs := tapper.ProjectOrientationKegs(flight, authorized)
	orientation := &Orientation{Root: flight, Flight: flight, Path: []string{flight.Name}, Kegs: kegs, AggregateKegs: append([]tapper.OrientationKeg(nil), kegs...), Warnings: warnings}
	if orientation.Root != nil {
		if err := p.bindRootAuthority(ctx, orientation, pre); err != nil {
			return nil, err
		}
	}
	if err := FinalizeOrientation(orientation); err != nil {
		return nil, err
	}
	authority := &tapper.OrientationAuthority{
		Root: flight, Active: flight, Path: orientation.Path, Revision: orientation.Revision,
		Agent: p.tap.ActiveAgent(),
	}
	payload, err := tapper.BuildOrientationPayload(flight, "", p.tap.ActiveLaunch(), kegs, warnings, authority)
	if err != nil {
		return nil, err
	}
	orientation.Payload = payload
	return orientation, nil
}

func graphFlights(graph *tapper.FlightGraph) []*tapper.Flight {
	if graph == nil || graph.Root == nil {
		return nil
	}
	out := make([]*tapper.Flight, 0, 1+len(graph.Available))
	out = append(out, graph.Root)
	out = append(out, graph.Available...)
	return out
}

func selectableFlightRefs(graph *tapper.FlightGraph) []string {
	if graph == nil || graph.Root == nil {
		return nil
	}
	out := make([]string, 0, 1+len(graph.Available))
	out = append(out, graph.Root.Name)
	out = append(out, graph.AvailableRefs()...)
	return out
}

// EffectiveOrientationRole intersects the identity's current ACL role with
// the selected flight's cover cap, so a cover never widens the identity role.
func EffectiveOrientationRole(row tapper.OrientationKeg) string {
	return tapper.EffectiveOrientationRole(row)
}

// AggregateOrientationKegs projects each reachable flight over one identity
// load and merges duplicate KEGs by highest effective role.
func AggregateOrientationKegs(flights []*tapper.Flight, authorized []tapper.OrientationKeg) []tapper.OrientationKeg {
	best := map[string]tapper.OrientationKeg{}
	for _, flight := range flights {
		for _, row := range tapper.ProjectOrientationKegs(flight, authorized) {
			current, exists := best[row.Ref]
			rowRank := orientationRoleRank(EffectiveOrientationRole(row))
			currentRank := orientationRoleRank(EffectiveOrientationRole(current))
			if !exists || rowRank > currentRank {
				if exists {
					row.Flights = append(row.Flights, current.Flights...)
				}
				best[row.Ref] = row
				continue
			}
			current.Flights = append(current.Flights, row.Flights...)
			best[row.Ref] = current
		}
	}
	out := make([]tapper.OrientationKeg, 0, len(best))
	for _, row := range best {
		sort.Strings(row.Flights)
		row.Flights = compactStrings(row.Flights)
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

func compactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}

func orientationRoleRank(role string) int {
	switch strings.TrimSpace(role) {
	case string(tapper.FlightRoleAdmin):
		return 3
	case string(tapper.FlightRoleEditor):
		return 2
	default:
		return 1
	}
}

// FinalizeOrientation computes a deterministic revision when a provider has
// not supplied one.
func FinalizeOrientation(orientation *Orientation) error {
	if orientation == nil || orientation.Revision != "" {
		return nil
	}
	type flightAuthority struct {
		Name         string               `json:"name"`
		Visibility   string               `json:"visibility"`
		Cover        []tapper.FlightCover `json:"cover"`
		Instructions string               `json:"instructions"`
	}
	type kegAuthority struct {
		Ref        string `json:"ref"`
		Role       string `json:"role"`
		Visibility string `json:"visibility"`
		FlightCap  string `json:"flight_cap"`
	}
	type revisionInput struct {
		RootRef  string          `json:"root_ref"`
		Active   flightAuthority `json:"active"`
		Path     []string        `json:"path"`
		Identity string          `json:"identity"`
		Kegs     []kegAuthority  `json:"kegs"`
	}
	in := revisionInput{
		Identity: orientation.Identity, Path: append([]string(nil), orientation.Path...),
		Kegs: make([]kegAuthority, 0, len(orientation.Kegs)),
	}
	if orientation.Root != nil {
		in.RootRef = orientation.Root.Name
	}
	if orientation.Flight != nil {
		in.Active = flightAuthority{
			Name: orientation.Flight.Name, Visibility: orientation.Flight.Visibility,
			Cover:        append([]tapper.FlightCover(nil), orientation.Flight.Cover...),
			Instructions: orientation.Flight.Instructions,
		}
		sort.Slice(in.Active.Cover, func(i, j int) bool {
			left, right := in.Active.Cover[i], in.Active.Cover[j]
			if left.Namespace != right.Namespace {
				return left.Namespace < right.Namespace
			}
			if left.Keg != right.Keg {
				return left.Keg < right.Keg
			}
			return left.Role < right.Role
		})
	}
	rows := orientation.Kegs
	for _, row := range rows {
		in.Kegs = append(in.Kegs, kegAuthority{Ref: row.Ref, Role: row.Role, Visibility: row.Visibility, FlightCap: row.FlightCap})
	}
	sort.Slice(in.Kegs, func(i, j int) bool { return in.Kegs[i].Ref < in.Kegs[j].Ref })
	raw, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("marshal orientation revision: %w", err)
	}
	orientation.Revision = fmt.Sprintf("%x", sha256.Sum256(raw))
	return nil
}

type localFlightProvider struct{ tap *tapper.Tap }

func (p localFlightProvider) ListFlights(ctx context.Context) ([]string, error) {
	return p.tap.ListFlights(ctx, tapper.ListFlightsOptions{})
}
func (p localFlightProvider) GetFlight(ctx context.Context, ref string) (*tapper.Flight, error) {
	return p.tap.GetFlight(ctx, tapper.GetFlightOptions{Name: ref})
}
func (p localFlightProvider) CreateFlight(ctx context.Context, opts tapper.CreateFlightOptions) (*tapper.Flight, error) {
	return p.tap.CreateFlight(ctx, opts)
}
func (p localFlightProvider) UpdateFlight(ctx context.Context, opts tapper.UpdateFlightOptions) (*tapper.Flight, error) {
	return p.tap.UpdateFlight(ctx, opts)
}
func (p localFlightProvider) DeleteFlight(ctx context.Context, opts tapper.DeleteFlightOptions) error {
	return p.tap.DeleteFlight(ctx, opts)
}

type localKegDiscoveryProvider struct{ tap *tapper.Tap }

func (p localKegDiscoveryProvider) ListKegs(ctx context.Context) ([]string, error) {
	return p.tap.HubListKegs(ctx, tapper.HubListOptions{})
}

func (p localKegDiscoveryProvider) CreateKeg(ctx context.Context, opts tapper.CreateKegOptions) (string, error) {
	target, err := p.tap.InitKeg(ctx, tapper.InitOptions{
		Keg:              opts.Keg,
		Title:            opts.Title,
		Visibility:       opts.Visibility,
		KegVersion:       opts.KegVersion,
		RequireBootstrap: true,
	})
	if err != nil {
		return "", err
	}
	if ref := tapper.CanonicalKegRef(target); ref != "" {
		return ref, nil
	}
	return opts.Keg, nil
}

type localAgentProvider struct{ tap *tapper.Tap }

func (p localAgentProvider) ListAgents(ctx context.Context, namespace string) ([]tapper.HubAgent, error) {
	return p.tap.ListAgents(ctx, tapper.ListAgentsOptions{Namespace: namespace})
}
func (p localAgentProvider) GetAgent(ctx context.Context, ref string) (*tapper.HubAgent, error) {
	return p.tap.GetAgent(ctx, tapper.GetAgentOptions{Ref: ref})
}
func (p localAgentProvider) CreateAgent(ctx context.Context, opts tapper.CreateAgentOptions) (*tapper.HubAgent, error) {
	return p.tap.CreateAgent(ctx, opts)
}
func (p localAgentProvider) EditAgent(ctx context.Context, opts tapper.EditAgentOptions) (*tapper.HubAgent, error) {
	return p.tap.EditAgent(ctx, opts)
}
func (p localAgentProvider) DeleteAgent(ctx context.Context, ref string) error {
	return p.tap.DeleteAgent(ctx, tapper.DeleteAgentOptions{Ref: ref})
}

type localNamespaceProvider struct{ tap *tapper.Tap }

func (p localNamespaceProvider) ListNamespaces(ctx context.Context) ([]tapper.HubNamespace, error) {
	res, err := p.tap.NamespaceList(ctx, tapper.NamespaceListOptions{})
	return res.Namespaces, err
}
func (p localNamespaceProvider) ListInvitations(ctx context.Context) ([]tapper.HubInvitation, error) {
	return p.tap.InvitationList(ctx, tapper.InvitationOptions{})
}

type localIdentityProvider struct{ tap *tapper.Tap }

func (p localIdentityProvider) Identities(ctx context.Context) ([]AuthIdentity, error) {
	if p.tap == nil || p.tap.PathService == nil || p.tap.Runtime == nil {
		return nil, errors.New("Tapper authentication service is unavailable")
	}
	name, entry, err := p.tap.ConfigService.SelectedHub("")
	if err != nil {
		return nil, err
	}
	_ = name
	who, err := p.tap.SelectedHubIdentity(ctx)
	if err != nil {
		return nil, err
	}
	namespaces := append([]string(nil), who.Namespaces...)
	sort.Strings(namespaces)
	out := []AuthIdentity{{Hub: entry.URL, UserID: who.UserID, Username: who.Username, DisplayName: who.DisplayName, DefaultNamespace: who.DefaultNamespace, Namespaces: namespaces}}

	return out, nil
}

// bindRootAuthority binds the live proof to this connection's selected Hub,
// using the identity pre already loaded when it has one.
func (p *localOrientationProvider) bindRootAuthority(ctx context.Context, orientation *Orientation, pre *orientationPrefetch) error {
	var err error
	if pre != nil && pre.withIdentity {
		pre.wait()
		orientation.RootHub, orientation.Identity, err = pre.rootHub, pre.identity, pre.identityErr
	} else {
		orientation.RootHub, orientation.Identity, err = p.rootIdentity(ctx)
	}
	if err != nil {
		return err
	}
	orientation.AllowedTargets = []string{}
	for _, row := range orientation.Kegs {
		orientation.AllowedTargets = append(orientation.AllowedTargets, orientation.RootHub+"/api/v1/@"+row.Namespace+"/kegs/"+row.Alias)
	}
	return nil
}

// rootIdentity resolves the selected Hub and the canonical identity
// authenticated to it.
func (p *localOrientationProvider) rootIdentity(ctx context.Context) (rootHub, identity string, err error) {
	_, entry, err := p.tap.ConfigService.SelectedHub("")
	if err != nil {
		return "", "", err
	}
	rootHub = tapper.CanonicalConfiguredHubURL(entry.URL)
	identity, err = p.orientationIdentityForSource(ctx, rootHub)
	return rootHub, identity, err
}

// orientationPrefetch loads the parts of an orientation that do not depend on
// the flight graph, the identity KEG catalog and the selected Hub identity,
// while the caller loads the graph. Callers read each result where the
// sequential code made the request, so errors surface in the same order.
type orientationPrefetch struct {
	wg           sync.WaitGroup
	withIdentity bool

	authorized []tapper.OrientationKeg
	warnings   []string
	catalogErr error

	rootHub     string
	identity    string
	identityErr error
}

// prefetchOrientation starts the KEG catalog and, when withIdentity, the Hub
// identity. Callers defer wait so no request outlives the orientation.
func (p *localOrientationProvider) prefetchOrientation(ctx context.Context, withIdentity bool) *orientationPrefetch {
	pre := &orientationPrefetch{withIdentity: withIdentity}
	pre.wg.Add(1)
	go func() {
		defer pre.wg.Done()
		pre.authorized, pre.warnings, pre.catalogErr = p.tap.IdentityKegCatalog(ctx)
	}()
	if withIdentity {
		pre.wg.Add(1)
		go func() {
			defer pre.wg.Done()
			pre.rootHub, pre.identity, pre.identityErr = p.rootIdentity(ctx)
		}()
	}
	return pre
}

func (pre *orientationPrefetch) wait() { pre.wg.Wait() }

// catalog returns the identity KEG catalog once it has loaded.
func (pre *orientationPrefetch) catalog() ([]tapper.OrientationKeg, []string, error) {
	pre.wait()
	return pre.authorized, pre.warnings, pre.catalogErr
}

// KegDeletionProvider permanently removes catalog KEGs.
type KegDeletionProvider interface {
	// DeleteKeg removes the explicit canonical KEG and all its data, including snapshots.
	DeleteKeg(context.Context, string) error
}

// KegDeletionAuthorizer is a KegDeletionProvider that authorizes a delete
// with the session's full authority itself, as a Hub does: that authority may
// reach past the session flight's cover (a flight managing the namespace's
// KEGs), so keg_delete leaves the decision to it instead of pre-checking the
// cover.
type KegDeletionAuthorizer interface {
	// AuthorizesKegDeletion reports whether DeleteKeg authorizes the delete
	// itself, so keg_delete skips its cover pre-check.
	AuthorizesKegDeletion() bool
}

func (p localKegDiscoveryProvider) DeleteKeg(ctx context.Context, ref string) error {
	_, err := p.tap.KegDelete(ctx, tapper.KegDeleteOptions{Keg: ref})
	return err
}
