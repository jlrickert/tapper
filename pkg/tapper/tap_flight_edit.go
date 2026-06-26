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
	manifestRaw, err := renderFlightManifestEditorDocument(ref, currentFlight.FlightManifest)
	if err != nil {
		return nil, fmt.Errorf("unable to render flight manifest: %w", err)
	}

	result := currentFlight
	lastManifest := currentFlight.FlightManifest
	apply := func(raw []byte) (*Flight, error) {
		m, err := parseFlightManifestStrict(raw)
		if err != nil {
			return nil, err
		}
		if flightManifestSemanticallyEqual(*m, lastManifest) {
			return result, nil
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
		result = flightFromHub(*hf, hubName)
		lastManifest = result.FlightManifest
		return result, nil
	}

	if opts.Stream != nil && opts.Stream.IsPiped {
		pipedRaw, readErr := io.ReadAll(opts.Stream.In)
		if readErr != nil {
			return nil, fmt.Errorf("unable to read piped input: %w", readErr)
		}
		if len(bytes.TrimSpace(pipedRaw)) > 0 {
			return apply(pipedRaw)
		}
	}

	tempPath, err := newEditorTempFilePath(t.Runtime, flightEditorTempFilePrefix(ref), ".yaml")
	if err != nil {
		return nil, fmt.Errorf("unable to create temp file path: %w", err)
	}
	if err := t.Runtime.WriteFile(tempPath, manifestRaw, 0o600); err != nil {
		return nil, fmt.Errorf("unable to write temp manifest file: %w", err)
	}
	defer func() {
		_ = t.Runtime.Remove(tempPath, false)
	}()

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

func flightEditorTempFilePrefix(ref FlightRef) string {
	return fmt.Sprintf("tap-flight-edit-%s-%s-",
		sanitizeEditorTempSegment(ref.Namespace, "unknown"),
		sanitizeEditorTempSegment(ref.Slug, "flight"),
	)
}

type flightManifestEditorDocument struct {
	Title        string        `yaml:"title"`
	Cover        []FlightCover `yaml:"cover"`
	Instructions string        `yaml:"instructions"`
}

func renderFlightManifestEditorDocument(ref FlightRef, m FlightManifest) ([]byte, error) {
	canonical := canonicalFlightManifest(m)
	doc := flightManifestEditorDocument{
		Title:        canonical.Title,
		Cover:        canonical.Cover,
		Instructions: canonical.Instructions,
	}
	body, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	out.WriteString(flightManifestSchemaModeline)
	fmt.Fprintf(&out, "# Flight %s. Ref is immutable; edit title, cover, and instructions.\n", ref.Canonical())
	out.Write(body)
	if !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
		out.WriteByte('\n')
	}
	return out.Bytes(), nil
}

type comparableFlightManifest struct {
	Title        string
	Cover        []FlightCover
	Instructions string
}

func canonicalFlightManifest(m FlightManifest) comparableFlightManifest {
	if len(m.Cover) > 0 {
		m.Cover = append([]FlightCover(nil), m.Cover...)
	}
	if len(m.AllowedKegs) > 0 {
		m.AllowedKegs = append([]string(nil), m.AllowedKegs...)
	}
	normalizeFlightManifest(&m)
	cover := make([]FlightCover, 0, len(m.Cover))
	for _, c := range m.Cover {
		cover = append(cover, FlightCover{
			Namespace: c.Namespace,
			Keg:       c.Keg,
			Role:      c.Role,
		})
	}
	return comparableFlightManifest{
		Title:        m.Title,
		Cover:        cover,
		Instructions: m.Instructions,
	}
}

func flightManifestSemanticallyEqual(a, b FlightManifest) bool {
	ca := canonicalFlightManifest(a)
	cb := canonicalFlightManifest(b)
	if ca.Title != cb.Title || ca.Instructions != cb.Instructions || len(ca.Cover) != len(cb.Cover) {
		return false
	}
	for i := range ca.Cover {
		if ca.Cover[i] != cb.Cover[i] {
			return false
		}
	}
	return true
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
