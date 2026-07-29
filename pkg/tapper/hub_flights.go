package tapper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/jlrickert/tapper/pkg/keg"
)

const hubFlightsPath = "/api/v1/flights"

type HubFlightCover struct {
	Namespace string `json:"namespace,omitempty"`
	Keg       string `json:"keg"`
	Role      string `json:"role"`
}

type HubFlight struct {
	Namespace    string             `json:"namespace"`
	Slug         string             `json:"slug"`
	Title        string             `json:"title"`
	Instructions string             `json:"instructions"`
	Visibility   string             `json:"visibility"`
	Capabilities []FlightCapability `json:"capabilities"`
	Cover        []HubFlightCover   `json:"cover"`
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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hub: contact hub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
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
	if err := doHubFlightJSON(ctx, http.MethodGet, hubURL, token, flightManifestPath(namespace, slug), nil, &out); err != nil {
		return nil, err
	}
	if err := validateHubFlight(out); err != nil {
		return nil, fmt.Errorf("hub: invalid flight %q: %w", slug, err)
	}
	return &out, nil
}

func CreateHubFlight(ctx context.Context, hubURL, token, namespace string, flight HubFlight) (*HubFlight, error) {
	var out HubFlight
	if err := doHubFlightJSON(ctx, http.MethodPost, hubURL, token, fmt.Sprintf("/api/v1/@%s/flights", namespace), flight, &out); err != nil {
		return nil, err
	}
	if err := validateHubFlight(out); err != nil {
		return nil, fmt.Errorf("hub: invalid created flight: %w", err)
	}
	return &out, nil
}

func UpdateHubFlight(ctx context.Context, hubURL, token, namespace, slug string, flight HubFlight) (*HubFlight, error) {
	var out HubFlight
	if err := doHubFlightJSON(ctx, http.MethodPut, hubURL, token, flightManifestPath(namespace, slug), flight, &out); err != nil {
		return nil, err
	}
	if err := validateHubFlight(out); err != nil {
		return nil, fmt.Errorf("hub: invalid updated flight: %w", err)
	}
	return &out, nil
}

func validateHubFlight(flight HubFlight) error {
	cover := make([]FlightCover, 0, len(flight.Cover))
	for _, row := range flight.Cover {
		cover = append(cover, FlightCover{
			Namespace: row.Namespace,
			Keg:       row.Keg,
			Role:      FlightRole(row.Role),
		})
	}
	return validateFlightManifest(&FlightManifest{
		Visibility:   flight.Visibility,
		Capabilities: flight.Capabilities,
		Cover:        cover,
	})
}

func DeleteHubFlight(ctx context.Context, hubURL, token, namespace, slug string) error {
	return doHubFlightJSON(ctx, http.MethodDelete, hubURL, token, flightManifestPath(namespace, slug), nil, nil)
}

func flightManifestPath(namespace, slug string) string {
	return fmt.Sprintf("/api/v1/@%s/+%s", namespace, slug)
}

func doHubFlightJSON(ctx context.Context, method, hubURL, token, path string, payload any, out any) error {
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
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("hub: contact hub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
	case http.StatusNoContent:
		return nil
	case http.StatusConflict:
		return fmt.Errorf("hub: flight already exists: %w", keg.ErrExist)
	case http.StatusNotFound:
		return fmt.Errorf("hub: flight not found: %w", keg.ErrNotExist)
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("hub: %w (%s)", ErrTokenRejected, resp.Status)
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
