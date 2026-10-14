// Package tapper — the Hub's one-call caller context.
//
// GET /api/v1/context answers, in one round trip, what `tap info` otherwise
// asks the selected Hub in three: who the token belongs to, the active flight,
// and the identity KEG catalog. Hubs that predate the endpoint answer 404; the
// client then issues the three requests concurrently and remembers the 404 for
// the life of the Tap.
package tapper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/jlrickert/tapper/pkg/apicontract"
	"github.com/jlrickert/tapper/pkg/keg"
)

const hubContextPath = "/api/v1/context"

// errHubContextUnsupported reports a Hub that has no /api/v1/context.
var errHubContextUnsupported = errors.New("hub has no context endpoint")

// hubContextResponse is the body of GET /api/v1/context. Decoding ignores
// unknown fields so the Hub can extend it without a contract revision.
type hubContextResponse struct {
	User        *WhoAmI           `json:"user"`
	Flight      *hubContextFlight `json:"flight"`
	FlightError *string           `json:"flight_error"`
	Kegs        []hubContextKeg   `json:"kegs"`
}

// hubContextFlight is the requested flight as the Hub resolved it. Name is
// the canonical @namespace/+slug, and Cover is the Hub-composed cover: the
// rows GET /@{namespace}/+{flight} reports as effective_cover.
type hubContextFlight struct {
	Name  string            `json:"name"`
	Title string            `json:"title"`
	Cover []HubEffectiveKeg `json:"cover"`
}

// hubContextKeg is one row of the identity KEG catalog. FlightCap and
// EffectiveRole are decoded for completeness; the client projects the
// composed cover itself so the result matches the fallback path exactly.
type hubContextKeg struct {
	Ref           string `json:"ref"`
	Namespace     string `json:"namespace"`
	Alias         string `json:"alias"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	Visibility    string `json:"visibility"`
	KegVersion    string `json:"keg_version"`
	LegacyVersion string `json:"kegv"`
	Role          string `json:"role"`
	FlightCap     string `json:"flight_cap"`
	EffectiveRole string `json:"effective_role"`
}

// getHubContext calls GET /api/v1/context. flight, when non-empty, is an
// @namespace/+slug ref, or a bare +slug the Hub resolves in the caller's
// personal namespace; a flight the Hub cannot load comes back in
// FlightError, not as a failure.
// A 404 wraps errHubContextUnsupported.
func getHubContext(ctx context.Context, hubURL, token, flight string) (*hubContextResponse, error) {
	base, err := normalizeHubURL(hubURL)
	if err != nil {
		return nil, err
	}
	endpoint := base + hubContextPath
	if flight != "" {
		endpoint += "?flight=" + url.QueryEscape(flight)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("hub: build context request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := apicontract.Do(hubHTTPClient(), req)
	if err != nil {
		return nil, fmt.Errorf("hub: contact hub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("hub: %w", errHubContextUnsupported)
	case http.StatusForbidden:
		return nil, fmt.Errorf("hub: %w (%s)%s", keg.ErrForbidden, resp.Status, readHubError(resp))
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("hub: %w (%s)", ErrTokenRejected, resp.Status)
	default:
		return nil, fmt.Errorf("hub: context returned %s for %s", resp.Status, hubContextPath)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("hub: read context response: %w", err)
	}
	var out hubContextResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("hub: parse context response: %w", err)
	}
	return &out, nil
}

// hubContextProbe remembers which Hubs answered 404 for /api/v1/context, so a
// long-lived Tap asks an older Hub only once.
type hubContextProbe struct {
	mu          sync.Mutex
	unsupported map[string]bool
}

func (p *hubContextProbe) supported(hubURL string) bool {
	if p == nil {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.unsupported[CanonicalConfiguredHubURL(hubURL)]
}

func (p *hubContextProbe) markUnsupported(hubURL string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.unsupported == nil {
		p.unsupported = map[string]bool{}
	}
	p.unsupported[CanonicalConfiguredHubURL(hubURL)] = true
}

// hubContextRequest selects what loadHubContext resolves.
type hubContextRequest struct {
	// hub is the explicit hub selector; "" uses the selected hub.
	hub string
	// flight is the flight to resolve; "" resolves none.
	flight string
	// kegs requests the identity KEG catalog. The context endpoint always
	// returns it; the flag only spares the separate requests.
	kegs bool
}

// hubContext is the caller's context on the selected hub. Each part carries
// the error its standalone request would have produced, so callers keep their
// own reporting order:
//   - whoErr is the identity probe's error.
//   - flight, flightNote, and flightErr match resolveOrientFlight.
//   - kegs, warnings, and kegsErr match identityKegCatalog.
type hubContext struct {
	who    *WhoAmI
	whoErr error

	flight     *Flight
	flightNote string
	flightErr  error

	kegs     []OrientationKeg
	warnings []string
	kegsErr  error
}

// loadHubContext resolves the selected hub's identity, the requested flight,
// and the identity KEG catalog. The selected hub answers all three with one
// GET /api/v1/context; a hub without it gets the three requests concurrently.
func (t *Tap) loadHubContext(ctx context.Context, req hubContextRequest) hubContext {
	if t == nil || t.ConfigService == nil {
		return t.loadHubContextSeparately(ctx, req)
	}
	cfg, _, err := t.ConfigService.Load()
	if err != nil {
		return t.loadHubContextSeparately(ctx, req)
	}
	name, entry, err := t.ConfigService.SelectedHub(req.hub)
	if err != nil || !t.hubContextProbe.supported(entry.URL) {
		return t.loadHubContextSeparately(ctx, req)
	}
	token := t.hubToken(entry)
	if token == "" {
		return t.loadHubContextSeparately(ctx, req)
	}

	// The Hub resolves the flight itself, a bare +slug included; a ref it
	// cannot parse comes back as flight_error like any other unreadable one.
	flightName := strings.TrimSpace(req.flight)
	contextFlight := flightName
	if ref, err := ParseFlightRef(flightName, ""); err == nil {
		contextFlight = ref.Canonical()
	}

	var (
		resp    *hubContextResponse
		respErr error
		out     hubContext
		others  []hubKegsResult
		wg      sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		resp, respErr = getHubContext(ctx, entry.URL, token, contextFlight)
	}()
	go func() {
		defer wg.Done()
		if req.kegs {
			others = t.orientKegsForHubs(ctx, cfg, name)
		}
	}()
	wg.Wait()

	if errors.Is(respErr, errHubContextUnsupported) {
		t.hubContextProbe.markUnsupported(entry.URL)
		return t.loadHubContextSeparately(ctx, req)
	}

	// One failed request stands in for all three: each part reports it the
	// way its own request would have.
	selected := hubKegsResult{name: name, err: respErr}
	if respErr != nil {
		out.whoErr = respErr
	} else {
		out.who = resp.User
		if out.who == nil {
			out.whoErr = fmt.Errorf("auth: hub returned no user for %s", hubContextPath)
		}
		selected.rows = hubContextKegRows(resp.Kegs, name)
	}
	if flightName != "" {
		out.flight, out.flightNote, out.flightErr = hubContextFlightResult(flightName, name, resp, respErr)
	}
	for i := range others {
		if others[i].name == name {
			others[i] = selected
		}
	}
	out.kegs, out.warnings, out.kegsErr = mergeHubKegs(others)
	return out
}

// loadHubContextSeparately is the fallback for hubs without /api/v1/context:
// the identity probe, the flight, and the catalog, issued concurrently.
func (t *Tap) loadHubContextSeparately(ctx context.Context, req hubContextRequest) hubContext {
	var out hubContext
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		out.who, out.whoErr = t.hubIdentity(ctx, req.hub)
	}()
	go func() {
		defer wg.Done()
		out.flight, out.flightNote, out.flightErr = t.resolveOrientFlight(ctx, req.flight)
	}()
	if req.kegs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out.kegs, out.warnings, out.kegsErr = t.identityKegCatalog(ctx)
		}()
	}
	wg.Wait()
	return out
}

// hubContextFlightResult reads the flight part of a context response the way
// resolveOrientFlight reports a standalone flight load: a compatibility error
// fails, and any other problem becomes a note.
func hubContextFlightResult(name, hubName string, resp *hubContextResponse, respErr error) (*Flight, string, error) {
	unavailable := func(reason any) (*Flight, string, error) {
		return &Flight{Name: name}, fmt.Sprintf("Flight %q is unavailable: %v", name, reason), nil
	}
	switch {
	case respErr != nil:
		if apicontract.IsCompatibility(respErr) {
			return nil, "", respErr
		}
		return unavailable(respErr)
	case resp.FlightError != nil && strings.TrimSpace(*resp.FlightError) != "":
		return unavailable(strings.TrimSpace(*resp.FlightError))
	case resp.Flight == nil:
		return unavailable("hub returned no flight")
	}
	f := resp.Flight
	ref, err := ParseFlightRef(f.Name, "")
	if err != nil || ref.Namespace == "" {
		return unavailable(fmt.Sprintf("hub returned flight %q", f.Name))
	}
	cover := f.Cover
	if cover == nil {
		cover = []HubEffectiveKeg{}
	}
	hf := HubFlight{Namespace: ref.Namespace, Slug: ref.Slug, Title: f.Title, EffectiveCover: cover}
	if err := validateHubFlight(hf); err != nil {
		return unavailable(fmt.Errorf("hub: invalid flight %q: %w", hf.Slug, err))
	}
	// The cover is already composed, so it is the flight's cover here.
	return ComposedFlight(flightFromHub(hf, hubName)), "", nil
}

// hubContextKegRows converts the context catalog to orientation rows shaped
// exactly as orientKegsForHub builds them from /api/v1/kegs.
func hubContextKegRows(kegs []hubContextKeg, hubName string) []OrientationKeg {
	out := make([]OrientationKeg, 0, len(kegs))
	for _, k := range kegs {
		ns, alias := k.Namespace, k.Alias
		if ns == "" || alias == "" {
			var ok bool
			if ns, alias, ok = splitKegRef(k.Ref); !ok {
				continue
			}
		}
		version := k.KegVersion
		if version == "" {
			version = k.LegacyVersion
		}
		out = append(out, OrientationKeg{
			Ref:         "@" + ns + "/" + alias,
			Namespace:   ns,
			Alias:       alias,
			Title:       k.Title,
			Description: k.Description,
			Role:        k.Role,
			Source:      hubName,
			Visibility:  k.Visibility,
			KegVersion:  version,
		})
	}
	return out
}

// hubIdentity probes the hub selected by explicit ("" for the active hub)
// with its live credentials.
func (t *Tap) hubIdentity(ctx context.Context, explicit string) (*WhoAmI, error) {
	_, entry, err := t.ConfigService.SelectedHub(explicit)
	if err != nil {
		return nil, err
	}
	return t.AuthValidateFn(ctx, t.Runtime, entry.URL, t.hubToken(entry))
}
