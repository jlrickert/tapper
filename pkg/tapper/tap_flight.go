package tapper

import (
	"context"
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// ListFlightsOptions controls Tap.ListFlights. Flights are hub-scoped, so
// there is no keg target.
type ListFlightsOptions struct {
	// Hub limits discovery to one configured hub. Empty discovers flights across
	// every configured hub.
	Hub string
	// Warnings, when non-nil, collects one message per hub that was skipped
	// (missing token, network failure) instead of failing the whole listing.
	// Completion paths leave it nil so discovery stays silent and best-effort.
	Warnings *[]string
}

// GetFlightOptions selects a single flight by name.
type GetFlightOptions struct {
	Name string
}

type CreateFlightOptions struct {
	Ref          string
	Title        string
	Visibility   string
	Capabilities []FlightCapability
	Instructions string
	Cover        []FlightCover
	Subflights   []string
}

// UpdateFlightOptions is a partial update: nil fields keep the flight's
// current value. The merge against the existing flight happens inside
// Tap.UpdateFlight against the same resolved ref the PUT targets, so a
// slug that exists in several places cannot read one flight and
// overwrite another.
type UpdateFlightOptions struct {
	Ref          string
	Title        *string
	Visibility   *string
	Capabilities *[]FlightCapability
	Instructions *string
	Cover        *[]FlightCover
	Subflights   *[]string
	ExpectedHash string
}

type DeleteFlightOptions struct {
	Ref          string
	ExpectedHash string
}

// ListFlights returns canonical refs discovered across configured hubs, or
// only opts.Hub when a filter is supplied.
func (t *Tap) ListFlights(ctx context.Context, opts ListFlightsOptions) ([]string, error) {
	return t.FlightService.ListFlights(ctx, opts.Hub, opts.Warnings)
}

// GetFlight loads a single flight by name.
func (t *Tap) GetFlight(ctx context.Context, opts GetFlightOptions) (*Flight, error) {
	return t.FlightService.GetFlight(ctx, opts.Name)
}

func (t *Tap) CreateFlight(ctx context.Context, opts CreateFlightOptions) (*Flight, error) {
	ref, entry, hubName, err := t.resolveWriteFlightRef(opts.Ref)
	if err != nil {
		return nil, err
	}
	details := FlightManifest{Visibility: opts.Visibility, Capabilities: opts.Capabilities, Cover: opts.Cover, Subflights: opts.Subflights}
	if err := validateFlightManifest(&details, ref.Namespace); err != nil {
		return nil, err
	}
	flight := HubFlight{
		Namespace:    ref.Namespace,
		Slug:         ref.Slug,
		Title:        opts.Title,
		Visibility:   normalizeFlightVisibility(opts.Visibility),
		Capabilities: append([]FlightCapability{}, opts.Capabilities...),
		Instructions: opts.Instructions,
		Cover:        hubCoverFromFlightCover(opts.Cover),
		Subflights:   append([]string(nil), opts.Subflights...),
	}
	hf, err := CreateHubFlight(ctx, entry.URL, t.FlightService.hubToken(entry), ref.Namespace, flight)
	if err != nil {
		return nil, err
	}
	t.FlightService.invalidateFlights()
	return flightFromHub(*hf, hubName), nil
}

func (t *Tap) UpdateFlight(ctx context.Context, opts UpdateFlightOptions) (*Flight, error) {
	ref, entry, hubName, err := t.resolveWriteFlightRef(opts.Ref)
	if err != nil {
		return nil, err
	}
	details := FlightManifest{}
	if opts.Visibility != nil {
		details.Visibility = *opts.Visibility
	}
	if opts.Capabilities != nil {
		details.Capabilities = *opts.Capabilities
	}
	if opts.Cover != nil {
		details.Cover = *opts.Cover
	}
	if opts.Subflights != nil {
		details.Subflights = *opts.Subflights
	}
	if err := validateFlightManifest(&details, ref.Namespace); err != nil {
		return nil, err
	}
	token := t.FlightService.hubToken(entry)
	current, err := GetHubFlight(ctx, entry.URL, token, ref.Namespace, ref.Slug)
	if err != nil {
		return nil, err
	}
	next := *current
	if opts.Title != nil {
		next.Title = *opts.Title
	}
	if opts.Visibility != nil {
		next.Visibility = normalizeFlightVisibility(*opts.Visibility)
	}
	if opts.Capabilities != nil {
		next.Capabilities = append([]FlightCapability{}, (*opts.Capabilities)...)
	}
	if opts.Instructions != nil {
		next.Instructions = *opts.Instructions
	}
	if opts.Cover != nil {
		next.Cover = hubCoverFromFlightCover(*opts.Cover)
	}
	if opts.Subflights != nil {
		next.Subflights = append([]string(nil), (*opts.Subflights)...)
	}
	hf, err := UpdateHubFlight(ctx, entry.URL, token, ref.Namespace, ref.Slug, next, opts.ExpectedHash)
	if err != nil {
		return nil, err
	}
	t.FlightService.invalidateFlights()
	return flightFromHub(*hf, hubName), nil
}

func normalizeFlightVisibility(visibility string) string {
	visibility = strings.TrimSpace(visibility)
	if visibility == "" {
		return FlightVisibilityPrivate
	}
	return visibility
}

func (t *Tap) DeleteFlight(ctx context.Context, opts DeleteFlightOptions) error {
	ref, entry, _, err := t.resolveWriteFlightRef(opts.Ref)
	if err != nil {
		return err
	}
	if err := DeleteHubFlight(ctx, entry.URL, t.FlightService.hubToken(entry), ref.Namespace, ref.Slug, opts.ExpectedHash); err != nil {
		return err
	}
	t.FlightService.invalidateFlights()
	return nil
}

func (t *Tap) resolveWriteFlightRef(raw string) (FlightRef, HubEntry, string, error) {
	cfg, err := t.ConfigService.Config()
	if err != nil {
		return FlightRef{}, HubEntry{}, "", err
	}
	ref, err := ParseFlightRef(raw, t.defaultFlightNamespace(cfg))
	if err != nil {
		return FlightRef{}, HubEntry{}, "", err
	}
	if ref.Namespace == "" {
		return FlightRef{}, HubEntry{}, "", fmt.Errorf("flight namespace is required; use @namespace/+slug")
	}
	hubName := cfg.resolveHubForNamespace(ref.Namespace)
	entry, ok := cfg.Hub(hubName)
	if !ok {
		return FlightRef{}, HubEntry{}, "", fmt.Errorf("hub %q is not configured", hubName)
	}
	kind := strings.TrimSpace(entry.Kind)
	if kind == "" {
		kind = HubKindRemote
	}
	if kind != HubKindRemote && kind != HubKindReadonly {
		return FlightRef{}, HubEntry{}, "", fmt.Errorf("hub %q has unsupported kind %q", hubName, kind)
	}
	if strings.TrimSpace(entry.URL) == "" {
		return FlightRef{}, HubEntry{}, "", fmt.Errorf("hub %q has no url configured", hubName)
	}
	if t.FlightService.hubToken(entry) == "" {
		return FlightRef{}, HubEntry{}, "", fmt.Errorf("hub %q has no auth token (run `tap auth login --hub %s`)", hubName, strings.TrimSpace(entry.URL))
	}
	return ref, entry, hubName, nil
}

// defaultFlightNamespace supplies the namespace for a flight reference that
// omits one. Precedence:
//
//	active KEG's namespace → defaultNamespace → fallbackNamespace →
//	the resolved hub's per-hub defaultNamespace
//
// The active KEG comes first because a bare flight name typed while working in
// an org KEG means a flight in that org, not one in the user's personal
// namespace. Resolving it last put flights in the wrong namespace silently
// (tapper#74); an explicitly qualified @namespace/+slug never reaches here.
func (t *Tap) defaultFlightNamespace(cfg *Config) string {
	if cfg == nil {
		return ""
	}
	if ns := t.activeKegNamespace(cfg); ns != "" {
		return ns
	}
	if ns := strings.TrimSpace(cfg.resolveNamespaceForName()); ns != "" {
		return ns
	}
	hubName := cfg.resolveHubName()
	entry, ok := cfg.Hub(hubName)
	if !ok {
		return ""
	}
	if ns := strings.TrimPrefix(strings.TrimSpace(entry.DefaultNamespace), "@"); ns != "" {
		return ns
	}
	return ""
}

// activeKegNamespace returns the namespace of the KEG currently in context, or
// "" when no KEG is selected or the selector names no namespace. The selector
// chain mirrors resolveIdentity and resolveKegAdminRef: defaultKeg → project
// alias → fallbackKeg.
//
// Only a namespace the selector states explicitly counts. Running the selector
// through resolveNamespaceHub would fill an omitted namespace from
// defaultNamespace, so a bare keg name would report the personal namespace as
// though the KEG had named it, and this step would stop being distinguishable
// from the one after it.
func (t *Tap) activeKegNamespace(cfg *Config) string {
	if t == nil || cfg == nil {
		return ""
	}
	selector := strings.TrimSpace(cfg.DefaultKeg())
	if selector == "" {
		selector = strings.TrimSpace(cfg.LookupAlias(t.Runtime, t.Root))
	}
	if selector == "" {
		selector = strings.TrimSpace(cfg.FallbackKeg())
	}
	if selector == "" {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(parseKegRef(selector).Namespace), "@")
}

func hubCoverFromFlightCover(cover []FlightCover) []HubFlightCover {
	out := make([]HubFlightCover, 0, len(cover))
	for _, c := range cover {
		out = append(out, HubFlightCover{
			Namespace: strings.TrimPrefix(strings.TrimSpace(c.Namespace), "@"),
			Keg:       strings.TrimSpace(c.Keg),
			Role:      string(normalizeFlightRole(c.Role)),
		})
	}
	return out
}

// FlightRestrictionError is returned when a resolved keg falls outside the
// active flight's cover or role cap.
type FlightRestrictionError struct {
	Flight string
	Keg    string
	Want   FlightRole
	Got    FlightRole
}

// flightRestrictionRecovery is appended to every cover/role-cap denial. The
// direct CLI bypasses flight restrictions entirely (see applyKegTargetProfile),
// so this error only ever reaches an agent over MCP. Authority was resolved for
// this call and the refusal never performs or replays the operation.
const flightRestrictionRecovery = ". The selected flight's current authority lacks this permission; the operation was not performed."

func (e *FlightRestrictionError) Error() string {
	if e.Want == FlightRoleEditor && e.Got == FlightRoleViewer {
		return fmt.Sprintf("ORIENTATION_DENIED: keg %q is viewer-only in flight %q", e.Keg, e.Flight) + flightRestrictionRecovery
	}
	if e.Want == FlightRoleAdmin && (e.Got == FlightRoleViewer || e.Got == FlightRoleEditor) {
		return fmt.Sprintf("ORIENTATION_DENIED: keg %q requires admin flight authority in flight %q", e.Keg, e.Flight) + flightRestrictionRecovery
	}
	return fmt.Sprintf("ORIENTATION_DENIED: keg %q is not available in flight %q", e.Keg, e.Flight) + flightRestrictionRecovery
}

// enforceFlight rejects a resolved keg that falls outside the selected flight's
// cover or does not meet the requested role cap. A blank flight or full_access
// capability bypasses the cover check; normal keg authorization still applies.
// Without full_access, a selected flight with an empty cover denies every keg.
func (t *Tap) enforceFlight(ctx context.Context, flightName string, k keg.Keg, want FlightRole) error {
	flightName = strings.TrimSpace(flightName)
	if flightName == "" || k == nil {
		return nil
	}
	flight, err := t.FlightService.GetFlight(ctx, flightName)
	if err != nil {
		return err
	}
	return t.enforceFlightSnapshot(flight, k, want)
}

// enforceFlightSnapshot applies one immutable, already-resolved flight
// snapshot. MCP uses this path so concurrent sessions never share mutable
// process-level flight authority.
func (t *Tap) enforceFlightSnapshot(flight *Flight, k keg.Keg, want FlightRole) error {
	if flight == nil || k == nil {
		return nil
	}
	if flight.HasCapability(FlightCapabilityFullAccess) {
		return nil
	}
	var alias, namespace, kegName string
	if k.Target() != nil {
		namespace = k.Target().Namespace
		kegName = k.Target().KegName
		if cfg, cErr := t.ConfigService.Config(); cErr == nil {
			alias = cfg.LookupAliasForTarget(t.Runtime, k.Target().String())
		}
	}
	role, ok := flight.RoleFor(alias, namespace, kegName)
	if ok && role.AtLeast(want) {
		return nil
	}

	label := alias
	if label == "" && namespace != "" && kegName != "" {
		label = "@" + namespace + "/" + kegName
	}
	if label == "" && kegName != "" {
		label = kegName
	}
	if label == "" && k.Target() != nil {
		label = k.Target().String()
	}
	return &FlightRestrictionError{Flight: flight.Name, Keg: label, Want: want, Got: role}
}

// CanonicalKegRef returns the @namespace/keg reference for a resolved target,
// or "" when the target names no namespaced keg.
func CanonicalKegRef(target *keg.Target) string {
	if target == nil {
		return ""
	}
	namespace, kegName := target.Namespace, target.KegName
	if namespace == "" || kegName == "" {
		return ""
	}
	return "@" + namespace + "/" + kegName
}
