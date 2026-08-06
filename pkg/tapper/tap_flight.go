package tapper

import (
	"context"
	"fmt"
	"path/filepath"
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
}

type DeleteFlightOptions struct {
	Ref string
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
	details := FlightManifest{Visibility: opts.Visibility, Capabilities: opts.Capabilities, Cover: opts.Cover}
	if err := validateFlightManifest(&details); err != nil {
		return nil, err
	}
	ref, entry, hubName, err := t.resolveWriteFlightRef(opts.Ref)
	if err != nil {
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
	}
	hf, err := CreateHubFlight(ctx, entry.URL, t.FlightService.hubToken(entry), ref.Namespace, flight)
	if err != nil {
		return nil, err
	}
	t.FlightService.invalidateFlights()
	return flightFromHub(*hf, hubName), nil
}

func (t *Tap) UpdateFlight(ctx context.Context, opts UpdateFlightOptions) (*Flight, error) {
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
	if err := validateFlightManifest(&details); err != nil {
		return nil, err
	}
	ref, entry, hubName, err := t.resolveWriteFlightRef(opts.Ref)
	if err != nil {
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
	hf, err := UpdateHubFlight(ctx, entry.URL, token, ref.Namespace, ref.Slug, next)
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
	if err := DeleteHubFlight(ctx, entry.URL, t.FlightService.hubToken(entry), ref.Namespace, ref.Slug); err != nil {
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
	ref, err := ParseFlightRef(raw, defaultFlightNamespace(cfg))
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
	if kind == HubKindLocal {
		// Reading local manifests is fully supported — discovery, orientation,
		// and cover enforcement all work off flights.d. Only mutation is
		// unimplemented, so say that rather than describing it as a
		// requirement the caller failed to meet.
		dir, dirErr := t.FlightService.localFlightsDirFor(entry)
		if dirErr != nil {
			dir = "<hub basePath>/" + flightsDirName
		}
		return FlightRef{}, HubEntry{}, "", fmt.Errorf(
			"flight create/update/delete is not implemented for local hubs (hub %q); "+
				"write the manifest to %s/%s.yaml by hand instead", hubName, dir, ref.Slug)
	}
	if strings.TrimSpace(entry.URL) == "" {
		return FlightRef{}, HubEntry{}, "", fmt.Errorf("hub %q has no url configured", hubName)
	}
	if t.FlightService.hubToken(entry) == "" {
		return FlightRef{}, HubEntry{}, "", fmt.Errorf("hub %q has no auth token (run `tap auth login --hub %s`)", hubName, strings.TrimSpace(entry.URL))
	}
	return ref, entry, hubName, nil
}

func defaultFlightNamespace(cfg *Config) string {
	if cfg == nil {
		return ""
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
	kind := strings.TrimSpace(entry.Kind)
	if kind == HubKindLocal {
		return LocalHubName
	}
	return ""
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
// so this error only ever reaches an agent over MCP — and an agent's session
// pins its flight snapshot until it orients again. A flight edited elsewhere
// mid-session is therefore the most common cause of a denial that the reader
// believes should have succeeded, and the reader cannot discover that from a
// bare "not available" line.
const flightRestrictionRecovery = ". Call `orient` to refresh this session's flight" +
	" authority: it may have changed since you oriented. If orient still does not" +
	" cover this keg, the flight genuinely excludes it — ask the user to widen the" +
	" flight's cover rather than retrying."

func (e *FlightRestrictionError) Error() string {
	if e.Want == FlightRoleEditor && e.Got == FlightRoleViewer {
		return fmt.Sprintf("keg %q is viewer-only in flight %q", e.Keg, e.Flight) + flightRestrictionRecovery
	}
	if e.Want == FlightRoleAdmin && (e.Got == FlightRoleViewer || e.Got == FlightRoleEditor) {
		return fmt.Sprintf("keg %q requires admin flight authority in flight %q", e.Keg, e.Flight) + flightRestrictionRecovery
	}
	return fmt.Sprintf("keg %q is not available in flight %q", e.Keg, e.Flight) + flightRestrictionRecovery
}

// enforceFlight rejects a resolved keg that falls outside the active flight's
// cover or does not meet the requested role cap. A blank flight or full_access
// capability bypasses the cover check; normal keg authorization still applies.
// Without full_access, an active flight with an empty cover denies every keg.
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
		if namespace == "" || kegName == "" {
			if localNamespace, localKegName, ok := localHubPathKegIdentity(k.Target()); ok {
				if namespace == "" {
					namespace = localNamespace
				}
				if kegName == "" {
					kegName = localKegName
				}
			}
		}
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
// or "" when the target names no namespaced keg. A filesystem keg carries its
// identity in the path rather than in the Namespace/KegName fields, so this
// applies the same derivation enforceFlightSnapshot uses — callers reporting a
// keg back to an agent must name it the way the flight cover does.
func CanonicalKegRef(target *keg.Target) string {
	if target == nil {
		return ""
	}
	namespace, kegName := target.Namespace, target.KegName
	if namespace == "" || kegName == "" {
		if pathNamespace, pathKeg, ok := localHubPathKegIdentity(target); ok {
			if namespace == "" {
				namespace = pathNamespace
			}
			if kegName == "" {
				kegName = pathKeg
			}
		}
	}
	if namespace == "" || kegName == "" {
		return ""
	}
	return "@" + namespace + "/" + kegName
}

func localHubPathKegIdentity(target *keg.Target) (string, string, bool) {
	if target == nil {
		return "", "", false
	}
	file := strings.TrimSpace(target.File)
	if file == "" {
		return "", "", false
	}
	clean := filepath.Clean(file)
	kegName := strings.TrimSpace(filepath.Base(clean))
	parent := strings.TrimSpace(filepath.Base(filepath.Dir(clean)))
	if strings.HasPrefix(parent, "@") && len(parent) > 1 && kegName != "" && kegName != "." {
		return strings.TrimPrefix(parent, "@"), kegName, true
	}
	return "", "", false
}
