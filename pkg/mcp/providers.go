package mcp

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// Orientation is one complete, immutable MCP authority candidate.
type Orientation struct {
	Flight   *tapper.Flight
	Payload  string
	Kegs     []tapper.OrientationKeg
	Warnings []string
}

// OrientationProvider owns transport-specific flight selection and rendering.
type OrientationProvider interface {
	// Load selects the active flight and renders it into a complete candidate.
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
	// return value is authoritative: a session editing its own flight adopts
	// exactly these bytes.
	UpdateFlight(context.Context, tapper.UpdateFlightOptions) (*tapper.Flight, error)
	// DeleteFlight removes a flight.
	DeleteFlight(context.Context, tapper.DeleteFlightOptions) error
}

// KegDiscoveryProvider reports the kegs an identity can reach.
type KegDiscoveryProvider interface {
	// ListKegs returns every identity-authorized canonical keg ref. MCP applies
	// the immutable active-flight cover before releasing results, so
	// implementations do not filter by flight themselves.
	ListKegs(context.Context) ([]string, error)
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

func (p *localOrientationProvider) Load(ctx context.Context) (*Orientation, error) {
	if p.tap == nil || p.tap.ConfigService == nil || p.tap.FlightService == nil {
		return nil, errors.New("Tapper flight service is unavailable")
	}
	// Adoption is the reload boundary for both session kinds: config-driven
	// sessions re-resolve their selection here, and launcher-bound sessions keep
	// their immutable --flight but still need fresh hub routing and credentials.
	// Configuration is otherwise fixed for the life of the process, so this is
	// where an edit made outside the session takes effect.
	p.tap.ConfigService.Reload()
	ref := strings.TrimSpace(p.staticFlight)
	if ref == "" {
		ref = p.tap.ActiveFlightName("")
	}
	if strings.TrimSpace(ref) == "" {
		payload, err := tapper.BuildOrientationPayload(nil, "", nil, nil)
		return &Orientation{Payload: payload}, err
	}
	flight, err := p.tap.FlightService.GetFlightFresh(ctx, ref)
	if err != nil {
		return nil, err
	}
	return p.Render(ctx, flight)
}

func (p *localOrientationProvider) Render(ctx context.Context, flight *tapper.Flight) (*Orientation, error) {
	payload, kegs, warnings, err := p.tap.OrientationForFlight(ctx, flight)
	if err != nil {
		return nil, err
	}
	return &Orientation{Flight: flight, Payload: payload, Kegs: kegs, Warnings: warnings}, nil
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
