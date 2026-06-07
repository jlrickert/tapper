package tapper

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	"gopkg.in/yaml.v3"
)

// flightsDirName is the reserved directory — a sibling of the @<namespace> dirs
// of a local hub — that holds flight manifests. "flights.d" is an invalid
// namespace (it contains a dot), so it can never collide with a keg path.
const flightsDirName = "flights.d"

// FlightManifest is the on-disk (and, in future, API) shape of a flight: an
// optional restriction on which kegs are available plus an optional block of
// agent instructions. An empty AllowedKegs means the flight restricts nothing
// (an instructions-only flight).
type FlightManifest struct {
	Title        string   `yaml:"title,omitempty"`
	AllowedKegs  []string `yaml:"allowedKegs,omitempty"`
	Instructions string   `yaml:"instructions,omitempty"`
}

// Flight is a discovered flight: its manifest plus provenance.
type Flight struct {
	Name   string `yaml:"-"`
	Source string `yaml:"-"` // "local" or a hub name
	FlightManifest
}

// FlightService discovers and loads flights for the active hub. Local-hub
// flights live under <basePath>/flights.d; remote-hub flights (served by the
// hub API) are not yet implemented.
type FlightService struct {
	Runtime       *toolkit.Runtime
	ConfigService *ConfigService
}

// localFlightsDir returns <local-hub-basePath>/flights.d, resolving the local
// hub's basePath the same way Config.ResolveRef does.
func (s *FlightService) localFlightsDir() (string, error) {
	cfg, err := s.ConfigService.Config(true)
	if err != nil {
		return "", err
	}
	base := ""
	if entry, ok := cfg.Hub(cfg.localHubName()); ok {
		base = strings.TrimSpace(entry.BasePath)
	}
	if base == "" {
		root, rootErr := defaultUserKegRoot(s.Runtime)
		if rootErr != nil {
			return "", rootErr
		}
		base = root
	}
	base = toolkit.ExpandEnv(s.Runtime, base)
	if expanded, expErr := toolkit.ExpandPath(s.Runtime, base); expErr == nil {
		base = expanded
	}
	return filepath.Join(base, flightsDirName), nil
}

// ListFlights returns the names of all flights discovered for the active hub,
// sorted. Today only local (filesystem) flights are discovered; a missing
// flights.d directory yields an empty list, not an error.
func (s *FlightService) ListFlights(_ context.Context) ([]string, error) {
	dir, err := s.localFlightsDir()
	if err != nil {
		return nil, err
	}
	entries, err := s.Runtime.ReadDir(dir)
	if err != nil {
		// A missing flights.d is "no flights", not an error.
		return []string{}, nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if stem, ok := flightStem(name); ok {
			names = append(names, stem)
		}
	}
	sort.Strings(names)
	return names, nil
}

// GetFlight loads a single flight by name. Returns keg.ErrNotExist when no
// manifest exists.
func (s *FlightService) GetFlight(_ context.Context, name string) (*Flight, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("flight name is required")
	}
	dir, err := s.localFlightsDir()
	if err != nil {
		return nil, err
	}
	for _, ext := range []string{".yaml", ".yml"} {
		path := filepath.Join(dir, name+ext)
		b, readErr := s.Runtime.ReadFile(path)
		if readErr != nil {
			continue
		}
		var m FlightManifest
		if err := yaml.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("parse flight %q: %w", name, err)
		}
		return &Flight{Name: name, Source: "local", FlightManifest: m}, nil
	}
	return nil, fmt.Errorf("flight %q not found: %w", name, keg.ErrNotExist)
}

// flightStem returns the flight name for a manifest filename and whether the
// filename is a flight manifest (.yaml/.yml).
func flightStem(filename string) (string, bool) {
	for _, ext := range []string{".yaml", ".yml"} {
		if strings.HasSuffix(filename, ext) {
			return strings.TrimSuffix(filename, ext), true
		}
	}
	return "", false
}

// allows reports whether a keg identified by the given alias and/or @ns/keg
// reference is permitted by the flight. An empty AllowedKegs permits everything
// (an instructions-only flight).
func (f *Flight) allows(alias, namespace, kegName string) bool {
	if f == nil || len(f.AllowedKegs) == 0 {
		return true
	}
	qualified := ""
	if namespace != "" && kegName != "" {
		qualified = "@" + namespace + "/" + kegName
	}
	for _, entry := range f.AllowedKegs {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if alias != "" && entry == alias {
			return true
		}
		if qualified != "" && strings.TrimPrefix(entry, "@") == strings.TrimPrefix(qualified, "@") {
			return true
		}
	}
	return false
}
