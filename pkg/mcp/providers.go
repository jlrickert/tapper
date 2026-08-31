package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
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
	Payload          string
	Kegs             []tapper.OrientationKeg
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
	// The MCP gate has already checked the flight's manage_kegs capability;
	// implementations apply their own transport's identity authorization, which
	// the capability never substitutes for.
	CreateKeg(context.Context, tapper.CreateKegOptions) (string, error)
}

// KegSearchRow is identity-authorized KEG metadata. Search results are not a
// flight projection and never grant operational authority.
type KegSearchRow struct {
	Ref        string `json:"ref"`
	Role       string `json:"role"`
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	Visibility string `json:"visibility"`
	Source     string `json:"source"`
}

// KegSearchResult includes partial-discovery warnings without failing useful
// results from reachable hubs.
type KegSearchResult struct {
	Kegs     []KegSearchRow `json:"kegs"`
	Warnings []string       `json:"warnings,omitempty"`
}

// KegSearchProvider searches identity-authorized KEG metadata independently
// of flight authority.
type KegSearchProvider interface {
	// SearchKegs returns bounded identity-authorized metadata matches.
	SearchKegs(context.Context, string) (KegSearchResult, error)
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
	// Local MCP reports every configured hub login; hosted MCP reports the one
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
	var b strings.Builder
	b.WriteString("This connection started without a configured flight, so normal identity-authorized full access applies.\n\n")
	if len(skipped) > 0 {
		b.WriteString("Some hubs were skipped during discovery, so this projection may be incomplete:\n\n")
		for _, warning := range skipped {
			b.WriteString("- " + warning + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("Use this authority only to bootstrap a least-privilege root. Ask the user to:\n\n")
	b.WriteString("1. Create a flight with `tap flight create @<namespace>/+<slug>` against a hub\n")
	b.WriteString("   they are logged in to. Give it only the KEG cover, roles, capabilities, and\n")
	b.WriteString("   instructions needed.\n")
	b.WriteString("2. Pin it outside MCP by setting `flight: +<slug>` in `~/.config/tapper/config.yaml`\n")
	b.WriteString("   (or the project's `.tapper/config.yaml`), exporting `TAP_FLIGHT=+<slug>`,\n")
	b.WriteString("   or passing `tap mcp --flight +<slug>`.\n")
	b.WriteString("3. Disconnect this MCP connection and start a new one. `session_refresh` cannot\n")
	b.WriteString("   change this connection's no-flight authority.\n\n")
	b.WriteString("`full_access` means the authenticated identities' existing access only; it never raises Hub ACLs or namespace membership.\n")
	return b.String()
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
	p.tap.ConfigService.Reload()
	ref := strings.TrimSpace(p.staticFlight)
	if ref == "" {
		ref = p.tap.ActiveFlightName("")
	}
	if strings.TrimSpace(ref) == "" {
		return p.resolveUnpinned(ctx, "")
	}
	flight, err := p.tap.FlightService.GetFlightFresh(ctx, ref)
	if err != nil {
		return nil, err
	}
	return p.resolve(ctx, flight, "")
}

func (p *localOrientationProvider) Resolve(ctx context.Context, rootRef, selected string) (*Orientation, error) {
	if p.tap == nil || p.tap.ConfigService == nil || p.tap.FlightService == nil {
		return nil, errors.New("Tapper flight service is unavailable")
	}
	p.tap.ConfigService.Reload()
	if strings.TrimSpace(rootRef) == "" {
		return p.resolveUnpinned(ctx, selected)
	}
	root, err := p.tap.FlightService.GetFlightFresh(ctx, rootRef)
	if err != nil {
		if errors.Is(err, keg.ErrNotExist) || errors.Is(err, keg.ErrForbidden) || errors.Is(err, keg.ErrUnauthorized) {
			return nil, fmt.Errorf("%w: launch root %q is no longer available: %v", ErrOrientationRootUnavailable, rootRef, err)
		}
		return nil, fmt.Errorf("%w: refresh launch root %q: %v", ErrOrientationUnavailable, rootRef, err)
	}
	return p.resolve(ctx, root, selected)
}

func (p *localOrientationProvider) resolveUnpinned(ctx context.Context, selected string) (*Orientation, error) {
	var warnings []string
	available, err := p.tap.ListFlights(ctx, tapper.ListFlightsOptions{Warnings: &warnings})
	if err != nil {
		return nil, fmt.Errorf("%w: list identity-accessible flights: %v", ErrOrientationUnavailable, err)
	}
	authorized, kegWarnings := p.tap.IdentityKegCatalog(ctx)
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
		authority := &tapper.OrientationAuthority{FullAccess: true, AvailableFlights: available, Revision: orientation.Revision}
		payload, err := tapper.BuildOrientationPayload(nil, localUnpinnedInstructions(warnings), p.tap.ActiveAgentName(), authorized, warnings, authority)
		if err != nil {
			return nil, err
		}
		orientation.Payload = payload
		return orientation, nil
	}
	active, loadErr := p.tap.FlightService.GetFlightFresh(ctx, selected)
	if loadErr != nil {
		return nil, fmt.Errorf("%w: selected flight %q is unavailable: %v", ErrOrientationDenied, selected, loadErr)
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
	kegs := tapper.ProjectOrientationKegs(active, authorized)
	orientation := &Orientation{
		Root: active, Flight: active, Path: []string{active.Name}, AvailableFlights: append([]string(nil), available...),
		Kegs: kegs, AggregateKegs: append([]tapper.OrientationKeg(nil), kegs...), Warnings: warnings,
	}
	orientation.Identity, err = p.orientationIdentityForSource(ctx, active.Source)
	if err != nil {
		return nil, err
	}
	if err := FinalizeOrientation(orientation); err != nil {
		return nil, err
	}
	authority := &tapper.OrientationAuthority{
		Active: active, Path: orientation.Path, AvailableFlights: available, Revision: orientation.Revision,
		FullAccess: true,
	}
	payload, err := tapper.BuildOrientationPayload(active, "", p.tap.ActiveAgentName(), kegs, warnings, authority)
	if err != nil {
		return nil, err
	}
	orientation.Payload = payload
	return orientation, nil
}

func (p *localOrientationProvider) orientationIdentityForSource(ctx context.Context, source string) (string, error) {
	if source == "local" {
		return "", nil
	}
	identities, err := (localIdentityProvider{tap: p.tap}).Identities(ctx)
	if err != nil {
		return "", fmt.Errorf("%w: load selected Hub identity: %v", ErrOrientationUnavailable, err)
	}
	cfg, err := p.tap.ConfigService.Config()
	if err != nil {
		return "", fmt.Errorf("%w: resolve selected Hub %q: %v", ErrOrientationUnavailable, source, err)
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
			return "", fmt.Errorf("%w: canonicalize selected Hub identity: %v", ErrOrientationUnavailable, err)
		}
		return canonical, nil
	}
	return "", fmt.Errorf("%w: authenticated identity for selected Hub %q is unavailable", ErrOrientationUnavailable, source)
}

func (p *localOrientationProvider) resolve(ctx context.Context, root *tapper.Flight, selected string) (*Orientation, error) {
	graph, err := p.tap.FlightService.ResolveFlightGraph(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("%w: load flight graph rooted at %s: %v", ErrOrientationUnavailable, root.Name, err)
	}
	root = graph.Root
	active, path, err := graph.Select(selected)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOrientationDenied, err)
	}
	authorized, warnings := p.tap.IdentityKegCatalog(ctx)
	kegs := tapper.ProjectOrientationKegs(active, authorized)
	aggregate := AggregateOrientationKegs(graphFlights(graph), authorized)
	orientation := &Orientation{
		Root: root, Flight: active, Path: path, AvailableFlights: selectableFlightRefs(graph),
		Kegs: kegs, AggregateKegs: aggregate, Warnings: warnings,
	}
	identities, err := (localIdentityProvider{tap: p.tap}).Identities(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: load root Hub identity: %v", ErrOrientationUnavailable, err)
	}
	var rootIdentity *AuthIdentity
	rootHubURL := ""
	if root.Source != "local" {
		cfg, cfgErr := p.tap.ConfigService.Config()
		if cfgErr != nil {
			return nil, fmt.Errorf("%w: resolve root Hub %q: %v", ErrOrientationUnavailable, root.Source, cfgErr)
		}
		if entry, ok := cfg.Hub(root.Source); ok && strings.TrimSpace(entry.URL) != "" {
			rootHubURL = tapper.CanonicalConfiguredHubURL(entry.URL)
		} else {
			// A source normally carries the configured alias. Retain support for
			// older manifests that stored a URL directly.
			rootHubURL = tapper.CanonicalConfiguredHubURL(root.Source)
		}
	}
	for _, identity := range identities {
		if root.Source != "local" && tapper.CanonicalConfiguredHubURL(identity.Hub) == rootHubURL {
			matched := identity
			rootIdentity = &matched
			break
		}
	}
	if rootIdentity != nil {
		orientation.Identity, err = CanonicalOrientationIdentity(*rootIdentity)
		if err != nil {
			return nil, fmt.Errorf("%w: canonicalize root Hub identity: %v", ErrOrientationUnavailable, err)
		}
	} else if root.Source != "local" {
		return nil, fmt.Errorf("%w: authenticated identity for root Hub %q is unavailable", ErrOrientationUnavailable, root.Source)
	}
	if err := FinalizeOrientation(orientation); err != nil {
		return nil, err
	}
	authority := &tapper.OrientationAuthority{
		Root: root, Active: active, Path: path, AvailableFlights: orientation.AvailableFlights,
		Revision: orientation.Revision,
	}
	discovery := kegs
	if strings.TrimSpace(selected) == "" {
		discovery = aggregate
	}
	payload, err := tapper.BuildOrientationPayload(active, "", p.tap.ActiveAgentName(), discovery, warnings, authority)
	if err != nil {
		return nil, err
	}
	orientation.Payload = payload
	return orientation, nil
}

func (p *localOrientationProvider) Render(ctx context.Context, flight *tapper.Flight) (*Orientation, error) {
	authorized, warnings := p.tap.IdentityKegCatalog(ctx)
	kegs := tapper.ProjectOrientationKegs(flight, authorized)
	orientation := &Orientation{Root: flight, Flight: flight, Path: []string{flight.Name}, Kegs: kegs, AggregateKegs: append([]tapper.OrientationKeg(nil), kegs...), Warnings: warnings}
	if err := FinalizeOrientation(orientation); err != nil {
		return nil, err
	}
	authority := &tapper.OrientationAuthority{
		Root: flight, Active: flight, Path: orientation.Path, Revision: orientation.Revision,
	}
	payload, err := tapper.BuildOrientationPayload(flight, "", p.tap.ActiveAgentName(), kegs, warnings, authority)
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
// the selected flight's cover cap. full_access is represented by an admin cap,
// so it naturally contributes the identity role without widening it.
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
		Name         string                    `json:"name"`
		Visibility   string                    `json:"visibility"`
		Capabilities []tapper.FlightCapability `json:"capabilities"`
		Cover        []tapper.FlightCover      `json:"cover"`
		Instructions string                    `json:"instructions"`
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
			Capabilities: append([]tapper.FlightCapability(nil), orientation.Flight.Capabilities...),
			Cover:        append([]tapper.FlightCover(nil), orientation.Flight.Cover...),
			Instructions: orientation.Flight.Instructions,
		}
		sort.Slice(in.Active.Capabilities, func(i, j int) bool { return in.Active.Capabilities[i] < in.Active.Capabilities[j] })
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
	for _, row := range orientation.Kegs {
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
		Namespace:        opts.Namespace,
		Title:            opts.Title,
		Visibility:       opts.Visibility,
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

func (p localKegDiscoveryProvider) SearchKegs(ctx context.Context, query string) (KegSearchResult, error) {
	rows, warnings := p.tap.IdentityKegCatalog(ctx)
	return KegSearchResult{Kegs: SearchIdentityKegs(rows, query), Warnings: warnings}, nil
}

// SearchIdentityKegs performs case-insensitive literal matching over canonical
// ref, title, and summary, returning at most 50 canonically ordered rows.
func SearchIdentityKegs(rows []tapper.OrientationKeg, query string) []KegSearchRow {
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return nil
	}
	matched := make([]KegSearchRow, 0, len(rows))
	for _, row := range rows {
		haystack := strings.ToLower(row.Ref + "\n" + row.Title + "\n" + row.Summary)
		if !strings.Contains(haystack, needle) {
			continue
		}
		matched = append(matched, KegSearchRow{
			Ref: row.Ref, Role: tapper.EffectiveOrientationRole(row),
			Title: row.Title, Summary: row.Summary, Visibility: row.Visibility, Source: row.Source,
		})
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].Ref < matched[j].Ref })
	if len(matched) > 50 {
		matched = matched[:50]
	}
	return matched
}

type localIdentityProvider struct{ tap *tapper.Tap }

func (p localIdentityProvider) Identities(ctx context.Context) ([]AuthIdentity, error) {
	if p.tap == nil || p.tap.PathService == nil || p.tap.Runtime == nil {
		return nil, errors.New("Tapper authentication service is unavailable")
	}
	store, err := tapper.LoadAuthStore(ctx, p.tap.Runtime, p.tap.PathService.AuthStorePath())
	if err != nil {
		return nil, err
	}
	var out []AuthIdentity
	for _, hub := range store.Hubs() {
		entry, ok := store.Get(hub)
		if !ok || strings.TrimSpace(entry.AccessToken) == "" || p.tap.AuthValidateFn == nil {
			continue
		}
		who, err := p.tap.AuthValidateFn(ctx, p.tap.Runtime, hub, entry.AccessToken)
		if err != nil || who == nil {
			continue
		}
		namespaces := append([]string(nil), who.Namespaces...)
		sort.Strings(namespaces)
		out = append(out, AuthIdentity{
			Hub: hub, UserID: who.UserID, Username: who.Username,
			DisplayName: who.DisplayName, DefaultNamespace: who.DefaultNamespace,
			Namespaces: namespaces,
		})
	}
	return out, nil
}
