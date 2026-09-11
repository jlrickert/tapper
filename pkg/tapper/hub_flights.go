package tapper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/jlrickert/tapper/pkg/apicontract"
	"github.com/jlrickert/tapper/pkg/keg"
)

const hubFlightsPath = "/api/v1/flights"

type HubFlightCover struct {
	Depth     int    `json:"depth,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Keg       string `json:"keg"`
	Role      string `json:"role"`
}

// HubEffectiveKeg is the Hub-composed identity-capped cover and its provenance.
type HubEffectiveKeg struct {
	Ref      string     `json:"ref"`
	Role     FlightRole `json:"role"`
	Source   string     `json:"source"`
	Distance int        `json:"distance"`
}

type HubFlight struct {
	EffectiveCover []HubEffectiveKeg  `json:"effective_cover,omitempty"`
	Namespace      string             `json:"namespace"`
	Slug           string             `json:"slug"`
	Title          string             `json:"title"`
	Description    string             `json:"description,omitempty" jsonschema:"short description, separate from instructions"`
	Instructions   string             `json:"instructions"`
	Visibility     string             `json:"visibility"`
	Capabilities   []FlightCapability `json:"capabilities"`
	Cover          []HubFlightCover   `json:"cover"`
	Subflights     []string           `json:"subflights"`
	Hash           string             `json:"hash,omitempty"`
}

func ListUserFlights(ctx context.Context, hubURL, token string) ([]HubFlight, error) {
	base, err := normalizeHubURL(hubURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+hubFlightsPath, nil)
	if err != nil {
		return nil, fmt.Errorf("hub: build list-flights request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := apicontract.Do(hubHTTPClient(), req)
	if err != nil {
		return nil, fmt.Errorf("hub: contact hub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden:
		return nil, fmt.Errorf("hub: %w (%s)%s", keg.ErrForbidden, resp.Status, readHubError(resp))
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("hub: %w (%s)", ErrTokenRejected, resp.Status)
	default:
		return nil, fmt.Errorf("hub: list flights returned %s for %s", resp.Status, hubFlightsPath)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("hub: read list-flights response: %w", err)
	}
	var flights []HubFlight
	if err := json.Unmarshal(body, &flights); err != nil {
		return nil, fmt.Errorf("hub: parse list-flights response: %w", err)
	}
	for i := range flights {
		if err := validateHubFlight(flights[i]); err != nil {
			return nil, fmt.Errorf("hub: invalid flight %q: %w", flights[i].Slug, err)
		}
	}
	return flights, nil
}

func GetHubFlight(ctx context.Context, hubURL, token, namespace, slug string) (*HubFlight, error) {
	var out HubFlight
	if err := doHubFlightJSON(ctx, http.MethodGet, hubURL, token, flightManifestPath(namespace, slug), "", nil, &out); err != nil {
		return nil, err
	}
	if err := validateHubFlight(out); err != nil {
		return nil, fmt.Errorf("hub: invalid flight %q: %w", slug, err)
	}
	return &out, nil
}

func CreateHubFlight(ctx context.Context, hubURL, token, namespace string, flight HubFlight) (*HubFlight, error) {
	flight.EffectiveCover = nil
	var out HubFlight
	if err := doHubFlightJSON(ctx, http.MethodPost, hubURL, token, fmt.Sprintf("/api/v1/@%s/flights", namespace), "", flight, &out); err != nil {
		return nil, err
	}
	if err := validateHubFlight(out); err != nil {
		return nil, fmt.Errorf("hub: invalid created flight: %w", err)
	}
	return &out, nil
}

func UpdateHubFlight(ctx context.Context, hubURL, token, namespace, slug string, flight HubFlight, expectedHash string) (*HubFlight, error) {
	flight.EffectiveCover = nil
	var out HubFlight
	if err := doHubFlightJSON(ctx, http.MethodPut, hubURL, token, flightManifestPath(namespace, slug), expectedHash, flight, &out); err != nil {
		return nil, err
	}
	if err := validateHubFlight(out); err != nil {
		return nil, fmt.Errorf("hub: invalid updated flight: %w", err)
	}
	return &out, nil
}

func validateHubFlight(flight HubFlight) error {
	if len(flight.EffectiveCover) > 100 {
		return fmt.Errorf("effective cover exceeds 100: %w", keg.ErrInvalid)
	}
	seen := map[string]bool{}
	for _, row := range flight.EffectiveCover {
		canonical, ok, err := keg.RelationshipTarget("keg:" + row.Ref)
		if err != nil || !ok || canonical != row.Ref || seen[row.Ref] || row.Distance < 0 || row.Distance > 7 || (row.Role != FlightRoleViewer && row.Role != FlightRoleEditor && row.Role != FlightRoleAdmin) {
			return fmt.Errorf("invalid effective cover: %w", keg.ErrInvalid)
		}
		seen[row.Ref] = true
	}

	cover := make([]FlightCover, 0, len(flight.Cover))
	for _, row := range flight.Cover {
		cover = append(cover, FlightCover{
			Namespace: row.Namespace,
			Keg:       row.Keg,
			Role:      FlightRole(row.Role), Depth: row.Depth,
		})
	}
	return validateFlightManifest(&FlightManifest{
		Visibility:   flight.Visibility,
		Capabilities: flight.Capabilities,
		Cover:        cover,
		Subflights:   flight.Subflights,
	}, flight.Namespace)
}

func DeleteHubFlight(ctx context.Context, hubURL, token, namespace, slug, expectedHash string) error {
	return doHubFlightJSON(ctx, http.MethodDelete, hubURL, token, flightManifestPath(namespace, slug), expectedHash, nil, nil)
}

func flightManifestPath(namespace, slug string) string {
	return fmt.Sprintf("/api/v1/@%s/+%s", namespace, slug)
}

func doHubFlightJSON(ctx context.Context, method, hubURL, token, path, expectedHash string, payload any, out any) error {
	base, err := normalizeHubURL(hubURL)
	if err != nil {
		return err
	}
	var body io.Reader
	if payload != nil {
		b, marshalErr := json.Marshal(payload)
		if marshalErr != nil {
			return fmt.Errorf("hub: encode flight request: %w", marshalErr)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return fmt.Errorf("hub: build flight request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if method == http.MethodPut || method == http.MethodDelete {
		req.Header.Set("If-Match", expectedHash)
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := apicontract.Do(hubHTTPClient(), req)
	if err != nil {
		return fmt.Errorf("hub: contact hub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Every branch carries the hub's own message. These statuses are not
	// specific to the flight: the same endpoints answer 404 for an unresolvable
	// *namespace* and 403 for an insufficient namespace role, so translating the
	// status alone names the wrong subject and sends the reader off to fix
	// something that was never wrong — "flight not found" on a create is not
	// even a coherent claim. Naming the request and appending the body keeps the
	// hub's diagnosis intact.
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
	case http.StatusNoContent:
		return nil
	case http.StatusConflict:
		return fmt.Errorf("hub: %s %s conflicts with existing state%s: %w",
			method, path, readHubError(resp), keg.ErrExist)
	case http.StatusPreconditionRequired:
		return fmt.Errorf("hub: %s %s: %w", method, path, keg.ErrPreconditionRequired)
	case http.StatusPreconditionFailed:
		var env struct {
			CurrentHash    string `json:"currentHash"`
			CurrentContent string `json:"currentContent"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&env)
		return &keg.PreconditionConflictError{Resource: path, CurrentHash: env.CurrentHash, CurrentContent: []byte(env.CurrentContent)}
	case http.StatusNotFound:
		return fmt.Errorf("hub: %s %s returned not found%s: %w",
			method, path, readHubError(resp), keg.ErrNotExist)
	case http.StatusForbidden:
		return fmt.Errorf("hub: %w for %s %s (%s)%s", keg.ErrForbidden, method, path, resp.Status, readHubError(resp))
	case http.StatusUnauthorized:
		return fmt.Errorf("hub: %w for %s %s (%s)%s",
			ErrTokenRejected, method, path, resp.Status, readHubError(resp))
	default:
		return fmt.Errorf("hub: flight request failed: %s%s", resp.Status, readHubError(resp))
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("hub: parse flight response: %w", err)
	}
	return nil
}
