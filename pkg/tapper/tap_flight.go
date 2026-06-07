package tapper

import (
	"context"
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// ListFlightsOptions controls Tap.ListFlights. Flights are hub-scoped, so there
// is no keg target; the struct exists for CLI/MCP parity symmetry.
type ListFlightsOptions struct{}

// GetFlightOptions selects a single flight by name.
type GetFlightOptions struct {
	Name string
}

// ListFlights returns the names of the flights discovered for the active hub.
func (t *Tap) ListFlights(ctx context.Context, _ ListFlightsOptions) ([]string, error) {
	return t.FlightService.ListFlights(ctx)
}

// GetFlight loads a single flight by name.
func (t *Tap) GetFlight(ctx context.Context, opts GetFlightOptions) (*Flight, error) {
	return t.FlightService.GetFlight(ctx, opts.Name)
}

// FlightRestrictionError is returned when a resolved keg falls outside the
// active flight's allow-list.
type FlightRestrictionError struct {
	Flight string
	Keg    string
}

func (e *FlightRestrictionError) Error() string {
	return fmt.Sprintf("keg %q is not available in flight %q", e.Keg, e.Flight)
}

// enforceFlight rejects a resolved keg that falls outside the active flight's
// allow-list. A blank flight or an instructions-only flight (empty allow-list)
// restricts nothing.
func (t *Tap) enforceFlight(ctx context.Context, flightName string, k *keg.Keg) error {
	flightName = strings.TrimSpace(flightName)
	if flightName == "" || k == nil {
		return nil
	}
	flight, err := t.FlightService.GetFlight(ctx, flightName)
	if err != nil {
		return err
	}
	if len(flight.AllowedKegs) == 0 {
		return nil
	}

	var alias, namespace, kegName string
	if k.Target != nil {
		namespace = k.Target.Namespace
		kegName = k.Target.KegName
		if cfg, cErr := t.ConfigService.Config(true); cErr == nil {
			alias = cfg.LookupAliasForTarget(t.Runtime, k.Target.String())
		}
	}
	if flight.allows(alias, namespace, kegName) {
		return nil
	}

	label := alias
	if label == "" && kegName != "" {
		label = "@" + namespace + "/" + kegName
	}
	if label == "" && k.Target != nil {
		label = k.Target.String()
	}
	return &FlightRestrictionError{Flight: flightName, Keg: label}
}
