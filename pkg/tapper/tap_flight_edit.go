package tapper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"gopkg.in/yaml.v3"
)

// EditFlightOptions configures behavior for Tap.EditFlight.
type EditFlightOptions struct {
	Ref string

	// Stream, when piped with non-empty content, supplies the manifest YAML
	// directly so scripts can apply a full manifest without an editor.
	Stream *toolkit.Stream
}

// EditFlight edits a Hub-backed flight's manifest as YAML.
//
// If stdin is piped with non-empty content, the piped manifest is validated
// and applied directly. Otherwise the manifest opens in the configured
// editor; every save is validated and PUT to the hub immediately, so the
// editor session behaves like the rest of tapper's live-save edit flows.
//
// The manifest carries only title, cover, and instructions. The slug and
// namespace are fixed by the ref: the YAML document cannot express them, and
// a stray "slug" key is rejected as an unknown field.
func (t *Tap) EditFlight(ctx context.Context, opts EditFlightOptions) (*Flight, error) {
	ref, entry, hubName, err := t.resolveWriteFlightRef(opts.Ref)
	if err != nil {
		return nil, err
	}
	token := t.FlightService.hubToken(entry)
	current, err := GetHubFlight(ctx, entry.URL, token, ref.Namespace, ref.Slug)
	if err != nil {
		return nil, err
	}
	currentFlight := flightFromHub(*current, hubName)
	manifestRaw, err := yaml.Marshal(currentFlight.FlightManifest)
	if err != nil {
		return nil, fmt.Errorf("unable to render flight manifest: %w", err)
	}

	apply := func(raw []byte) (*Flight, error) {
		m, err := parseFlightManifestStrict(raw)
		if err != nil {
			return nil, err
		}
		next := HubFlight{
			Namespace:    ref.Namespace,
			Slug:         ref.Slug,
			Title:        m.Title,
			Instructions: m.Instructions,
			Cover:        hubCoverFromFlightCover(m.Cover),
		}
		hf, err := UpdateHubFlight(ctx, entry.URL, token, ref.Namespace, ref.Slug, next)
		if err != nil {
			return nil, err
		}
		t.FlightService.invalidateFlights()
		return flightFromHub(*hf, hubName), nil
	}

	if opts.Stream != nil && opts.Stream.IsPiped {
		pipedRaw, readErr := io.ReadAll(opts.Stream.In)
		if readErr != nil {
			return nil, fmt.Errorf("unable to read piped input: %w", readErr)
		}
		if len(bytes.TrimSpace(pipedRaw)) > 0 {
			if bytes.Equal(pipedRaw, manifestRaw) {
				return currentFlight, nil
			}
			return apply(pipedRaw)
		}
	}

	tempPath, err := newEditorTempFilePath(t.Runtime, "flight-edit-", ".yaml")
	if err != nil {
		return nil, fmt.Errorf("unable to create temp file path: %w", err)
	}
	header := fmt.Sprintf("# flight %s — slug is immutable; edit title, cover, and instructions\n", ref.Canonical())
	if err := t.Runtime.WriteFile(tempPath, append([]byte(header), manifestRaw...), 0o600); err != nil {
		return nil, fmt.Errorf("unable to write temp manifest file: %w", err)
	}
	defer func() {
		_ = t.Runtime.Remove(tempPath, false)
	}()

	result := currentFlight
	if err := editWithLiveSaves(ctx, t.Runtime, tempPath, nil, func(editedRaw []byte) error {
		flight, applyErr := apply(editedRaw)
		if applyErr != nil {
			return applyErr
		}
		result = flight
		return nil
	}); err != nil {
		return nil, fmt.Errorf("unable to edit flight: %w", err)
	}
	return result, nil
}

// parseFlightManifestStrict decodes an edited manifest, rejecting unknown
// keys (so a slug change attempt fails loudly) and cover roles other than
// viewer/editor (normalizeFlightRole would otherwise coerce typos to viewer).
// An omitted role keeps the manifest parser's viewer default.
func parseFlightManifestStrict(raw []byte) (*FlightManifest, error) {
	var m FlightManifest
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("flight manifest is invalid: %w", err)
	}
	for _, c := range m.Cover {
		switch FlightRole(strings.TrimSpace(string(c.Role))) {
		case "", FlightRoleViewer, FlightRoleEditor:
		default:
			return nil, fmt.Errorf("invalid flight cover role %q", c.Role)
		}
	}
	normalizeFlightManifest(&m)
	return &m, nil
}
