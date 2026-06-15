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
	Instructions *string
	Cover        *[]FlightCover
}

type DeleteFlightOptions struct {
	Ref string
}

// ListFlights returns the names of the flights discovered for the active hub.
func (t *Tap) ListFlights(ctx context.Context, opts ListFlightsOptions) ([]string, error) {
	return t.FlightService.ListFlights(ctx, opts.Warnings)
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
	flight := HubFlight{
		Namespace:    ref.Namespace,
		Slug:         ref.Slug,
		Title:        opts.Title,
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
	cfg, err := t.ConfigService.Config(true)
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
		return FlightRef{}, HubEntry{}, "", fmt.Errorf("flight create/update/delete require a remote hub-backed namespace")
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

func (e *FlightRestrictionError) Error() string {
	if e.Want == FlightRoleEditor && e.Got == FlightRoleViewer {
		return fmt.Sprintf("keg %q is viewer-only in flight %q", e.Keg, e.Flight)
	}
	return fmt.Sprintf("keg %q is not available in flight %q", e.Keg, e.Flight)
}

// enforceFlight rejects a resolved keg that falls outside the active flight's
// cover or does not meet the requested role cap. A blank flight or an
// instructions-only flight (empty cover) restricts nothing.
func (t *Tap) enforceFlight(ctx context.Context, flightName string, k keg.Keg, want FlightRole) error {
	flightName = strings.TrimSpace(flightName)
	if flightName == "" || k == nil {
		return nil
	}
	flight, err := t.FlightService.GetFlight(ctx, flightName)
	if err != nil {
		return err
	}
	var alias, namespace, kegName string
	if k.Target() != nil {
		namespace = k.Target().Namespace
		kegName = k.Target().KegName
		if cfg, cErr := t.ConfigService.Config(true); cErr == nil {
			alias = cfg.LookupAliasForTarget(t.Runtime, k.Target().String())
		}
	}
	role, ok := flight.RoleFor(alias, namespace, kegName)
	if ok && role.AtLeast(want) {
		return nil
	}

	label := alias
	if label == "" && kegName != "" {
		label = "@" + namespace + "/" + kegName
	}
	if label == "" && k.Target() != nil {
		label = k.Target().String()
	}
	return &FlightRestrictionError{Flight: flightName, Keg: label, Want: want, Got: role}
}
