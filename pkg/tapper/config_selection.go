package tapper

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
	"gopkg.in/yaml.v3"
)

// selection applies invocation overrides and the connection-pinned Hub.
func (s *ConfigService) selection(out *resolved) error {
	cfg := out.merged
	if s.FlightOverride != "" {
		cfg.data.Flight = s.FlightOverride
	}
	if s.KegOverride != "" {
		cfg.data.Keg = s.KegOverride
	}
	if s.HubOverride != "" {
		cfg.data.HubName = s.HubOverride
		if strings.HasPrefix(s.HubOverride, "http://") || strings.HasPrefix(s.HubOverride, "https://") {
			entry := HubEntry{URL: s.HubOverride}
			for _, h := range cfg.Hubs() {
				if CanonicalConfiguredHubURL(h.URL) == CanonicalConfiguredHubURL(s.HubOverride) {
					entry = h
					break
				}
			}
			cfg.data.Hubs[s.HubOverride] = entry
		}
	}
	if s.pinnedHubURL != "" {
		// Credentials are keyed by URL. Never borrow the token from an alias that
		// has since been pointed at another server.
		entry := HubEntry{URL: s.pinnedHubURL}
		names := make([]string, 0, len(cfg.Hubs()))
		for name := range cfg.Hubs() {
			names = append(names, name)
		}
		sort.Strings(names)
		names = append([]string{s.pinnedHubName}, names...)
		for _, name := range names {
			h, ok := cfg.Hub(name)
			if ok && CanonicalConfiguredHubURL(h.URL) == s.pinnedHubURL {
				entry = h
				break
			}
		}

		cfg.data.HubName = s.pinnedHubName
		cfg.data.Hubs[s.pinnedHubName] = entry
	}
	return nil
}

// PinHub fixes the canonical Hub URL for a connection. Reloads may refresh its
// credentials and live Flight authority, but can never change its destination.
func (s *ConfigService) PinHub() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pinnedHubURL != "" {
		return nil
	}
	if s.snap == nil {
		snap, err := s.load()
		if err != nil {
			return err
		}
		s.snap = snap
	}
	cfg := s.snap.merged
	name := cfg.resolveHubName()
	entry, ok := cfg.Hub(name)
	if !ok {
		return fmt.Errorf("hub %q is not configured", name)
	}
	url, err := normalizeHubURL(CanonicalConfiguredHubURL(entry.URL))
	if err != nil {
		return err
	}
	s.pinnedHubName, s.pinnedHubURL = name, CanonicalConfiguredHubURL(url)
	return nil
}

// SelectedHub returns the single selected connection. Explicit requests on a
// pinned connection must identify the same canonical URL.
func (s *ConfigService) SelectedHub(explicit string) (string, HubEntry, error) {
	cfg, err := s.Config()
	if err != nil {
		return "", HubEntry{}, err
	}
	name := explicit
	if name == "" {
		name = cfg.resolveHubName()
	}
	entry, ok := cfg.Hub(name)
	if !ok {
		return "", HubEntry{}, fmt.Errorf("hub %q is not configured", name)
	}
	if entry.invalid != nil {
		return "", HubEntry{}, fmt.Errorf("hub %q: %w", name, entry.invalid)
	}
	url, err := normalizeHubURL(CanonicalConfiguredHubURL(entry.URL))
	if err != nil {
		return "", HubEntry{}, fmt.Errorf("hub %q: %w", name, err)
	}
	s.mu.Lock()
	pinned := s.pinnedHubURL
	s.mu.Unlock()
	if pinned != "" && CanonicalConfiguredHubURL(url) != pinned {
		return "", HubEntry{}, fmt.Errorf("%w: target is outside the pinned Hub", keg.ErrOrientationDenied)
	}
	entry.URL = url
	return name, entry, nil
}

func (m *KegMapEntry) UnmarshalYAML(n *yaml.Node) error {
	m.raw = cloneYAMLNode(n)
	_, m.hasKeg = mappingValue(n, "keg")
	if n.Kind != yaml.MappingNode {
		m.invalid = fmt.Errorf("kegMap entry must be a mapping")
		return nil
	}
	for key, dst := range map[string]*string{"keg": &m.Keg, "hub": &m.Hub, "flight": &m.Flight, "pathPrefix": &m.PathPrefix, "pathRegex": &m.PathRegex} {
		if value, ok := mappingValue(n, key); ok {
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				*dst = "\x00invalid " + key
				if key == "keg" {
					m.invalid = fmt.Errorf("%s must be a string", key)
				}
				continue
			}
			*dst = value.Value
		}
	}
	return nil
}

func (m KegMapEntry) MarshalYAML() (any, error) {
	if m.raw != nil && m.invalid != nil {
		return m.raw, nil
	}
	type plain KegMapEntry
	return plain(m), nil
}

func (h *HubEntry) UnmarshalYAML(n *yaml.Node) error {
	type plain HubEntry
	for _, key := range []string{"url", "token", "tokenEnv"} {
		if value, ok := mappingValue(n, key); ok && (value.Kind != yaml.ScalarNode || value.Tag != "!!str") {
			h.invalid = fmt.Errorf("%s must be a string", key)
			h.raw = cloneYAMLNode(n)
			return nil
		}
	}
	for _, key := range []string{"token", "tokenEnv"} {
		if value, ok := mappingValue(n, key); ok && strings.TrimSpace(value.Value) == "" {
			h.invalid = fmt.Errorf("configured %s must not be empty", key)
			h.raw = cloneYAMLNode(n)
			return nil
		}
	}
	var value plain
	if err := n.Decode(&value); err != nil {
		h.invalid = err
		h.raw = cloneYAMLNode(n)
		return nil
	}
	*h = HubEntry(value)
	return nil
}
func (h HubEntry) MarshalYAML() (any, error) {
	if h.invalid != nil {
		return h.raw, nil
	}
	type plain HubEntry
	return plain(h), nil
}

// validateTargetHub covers direct URLs and resolved node links as well as KEG selectors.
func (s *ConfigService) validateTargetHub(target *keg.Target, explicit string) error {
	name, entry, err := s.SelectedHub(explicit)
	if err != nil {
		return err
	}
	base := target.HubURL
	if base == "" {
		base, _, _ = strings.Cut(target.Url, "/api/v1/")
	}
	if CanonicalConfiguredHubURL(base) != CanonicalConfiguredHubURL(entry.URL) {
		return fmt.Errorf("%w: target is outside the selected Hub", keg.ErrOrientationDenied)
	}
	target.Hub = name
	target.HubURL = entry.URL
	target.Token, target.TokenEnv = entry.Token, entry.TokenEnv
	return nil
}

// defaultValue returns only the selected rule's own field. Missing fields never
// inherit from a broader rule. Preserve an explicitly empty KEG as invalid.
func (m KegMapEntry) defaultValue(field string) string {
	switch field {
	case "keg":
		value := m.Keg
		if m.hasKeg && strings.TrimSpace(value) == "" {
			return "\x00invalid selected kegMap KEG"
		}
		return value
	case "hub":
		return m.Hub
	case "flight":
		if m.Flight != "" && strings.TrimSpace(m.Flight) == "" {
			return "\x00invalid selected kegMap flight"
		}
		return m.Flight
	default:
		return ""
	}
}
