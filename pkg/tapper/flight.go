package tapper

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	"gopkg.in/yaml.v3"
)

// flightsDirName is the reserved directory — a sibling of the @<namespace> dirs
// of a local hub — that holds flight manifests. "flights.d" is an invalid
// namespace (it contains a dot), so it can never collide with a keg path.
const (
	flightsDirName = "flights.d"

	// FlightManifestSchemaURL is the public JSON Schema used by editor
	// modelines for flight manifest YAML.
	FlightManifestSchemaURL      = "https://raw.githubusercontent.com/jlrickert/tapper/main/schemas/flight-manifest.json"
	flightManifestSchemaModeline = "# yaml-language-server: $schema=" + FlightManifestSchemaURL + "\n"
)

type FlightRole string

type FlightCapability string

const (
	FlightRoleViewer FlightRole = "viewer"
	FlightRoleEditor FlightRole = "editor"

	FlightVisibilityPrivate = "private"
	FlightVisibilityPublic  = "public"

	FlightCapabilityManageFlights FlightCapability = "manage_flights"
)

// AtLeast reports whether r grants at least want within a flight cover.
func (r FlightRole) AtLeast(want FlightRole) bool {
	r = normalizeFlightRole(r)
	want = normalizeFlightRole(want)
	if want == FlightRoleViewer {
		return r == FlightRoleViewer || r == FlightRoleEditor
	}
	return r == FlightRoleEditor
}

func normalizeFlightRole(role FlightRole) FlightRole {
	switch FlightRole(strings.TrimSpace(string(role))) {
	case FlightRoleEditor:
		return FlightRoleEditor
	default:
		return FlightRoleViewer
	}
}

type FlightCover struct {
	Namespace string     `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Keg       string     `yaml:"keg" json:"keg"`
	Role      FlightRole `yaml:"role" json:"role"`
}

// FlightManifest is the on-disk/API shape of a flight: explicit covered kegs
// plus markdown instructions. AllowedKegs is kept for backward compatibility
// with local manifests and is normalized into editor-cap cover entries.
type FlightManifest struct {
	Title        string             `yaml:"title,omitempty" json:"title,omitempty"`
	Visibility   string             `yaml:"visibility,omitempty" json:"visibility,omitempty"`
	Capabilities []FlightCapability `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	Cover        []FlightCover      `yaml:"cover,omitempty" json:"cover,omitempty"`
	AllowedKegs  []string           `yaml:"allowedKegs,omitempty" json:"allowedKegs,omitempty"`
	Instructions string             `yaml:"instructions,omitempty" json:"instructions,omitempty"`
}

// Flight is a discovered flight: its manifest plus provenance.
type Flight struct {
	Name         string `yaml:"-" json:"name,omitempty"`
	Namespace    string `yaml:"-" json:"namespace,omitempty"`
	Slug         string `yaml:"-" json:"slug,omitempty"`
	Source       string `yaml:"-" json:"source,omitempty"` // "local" or a hub name
	Revision     int64  `yaml:"-" json:"revision,omitempty"`
	ManifestHash string `yaml:"-" json:"manifest_hash,omitempty"`
	FlightManifest
}

type FlightRef struct {
	Namespace string
	Slug      string
}

func ParseFlightRef(raw string, defaultNamespace string) (FlightRef, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return FlightRef{}, fmt.Errorf("flight reference is required")
	}
	ref := FlightRef{Namespace: strings.TrimPrefix(strings.TrimSpace(defaultNamespace), "@")}
	switch {
	case strings.HasPrefix(raw, "@"):
		ns, rest, ok := strings.Cut(strings.TrimPrefix(raw, "@"), "/")
		if !ok || ns == "" || rest == "" {
			return FlightRef{}, fmt.Errorf("invalid flight reference %q", raw)
		}
		ref.Namespace = ns
		ref.Slug = strings.TrimPrefix(rest, "+")
	case strings.HasPrefix(raw, "+"):
		ref.Slug = strings.TrimPrefix(raw, "+")
	default:
		ref.Slug = raw
	}
	ref.Slug = strings.TrimSpace(ref.Slug)
	if ref.Slug == "" {
		return FlightRef{}, fmt.Errorf("flight slug is required")
	}
	return ref, nil
}

func (r FlightRef) Canonical() string {
	if r.Namespace == "" {
		return "+" + r.Slug
	}
	return "@" + r.Namespace + "/+" + r.Slug
}

// FlightService discovers and loads flights for configured local and remote
// hubs. Local-hub flights live under <basePath>/flights.d; remote-hub flights
// are served by the Hub API.
type FlightService struct {
	Runtime       *toolkit.Runtime
	ConfigService *ConfigService
	// KegService supplies the token resolver (and its cached auth store)
	// for hub requests. Lazily constructed when not wired by NewTap.
	KegService *KegService

	mu sync.Mutex
	// flightCache memoizes GetFlight results for the life of the process so
	// per-operation flight gating (enforceFlight) doesn't re-fetch over the
	// network on every keg resolution. Flight mutations clear it.
	flightCache map[string]*Flight
}

func (s *FlightService) kegService() *KegService {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.KegService == nil {
		s.KegService = &KegService{Runtime: s.Runtime, ConfigService: s.ConfigService}
	}
	return s.KegService
}

func (s *FlightService) cachedFlight(name string) (*Flight, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.flightCache[name]
	return f, ok
}

func (s *FlightService) storeFlight(name string, f *Flight) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.flightCache == nil {
		s.flightCache = map[string]*Flight{}
	}
	s.flightCache[name] = f
}

// StoreSessionFlight pins a validated flight in the process cache for Tap's
// per-operation cover enforcement. MCP session invalidation is handled by the
// server-owned gate before these cached values are used.
func (s *FlightService) StoreSessionFlight(name string, f *Flight) {
	s.storeFlight(name, f)
}

// invalidateFlights drops every memoized flight. Called after any flight
// mutation; the cache is small, so clearing it wholesale is simpler than
// tracking which refs alias which cache keys.
func (s *FlightService) invalidateFlights() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flightCache = nil
}

func (s *FlightService) config() (*Config, error) {
	cfg, err := s.ConfigService.Config(true)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// localFlightsDirFor returns <hub-basePath>/flights.d for a local hub entry,
// resolving the basePath the same way Config.ResolveRef does.
func (s *FlightService) localFlightsDirFor(entry HubEntry) (string, error) {
	base := strings.TrimSpace(entry.BasePath)
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

// ListFlights returns canonical @namespace/+slug refs for flights discovered
// across configured hubs, sorted. Remote/auth/network errors are best-effort
// so shell completion and discovery remain responsive; when warnings is
// non-nil each skipped hub appends one message so user-facing listings can
// say what was left out.
func (s *FlightService) ListFlights(ctx context.Context, warnings *[]string) ([]string, error) {
	cfg, err := s.config()
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	var out []string
	add := func(ref string) {
		if ref == "" {
			return
		}
		if _, ok := seen[ref]; ok {
			return
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}

	for _, name := range s.allHubNames(cfg) {
		entry, ok := cfg.Hub(name)
		if !ok {
			continue
		}
		kind := strings.TrimSpace(entry.Kind)
		if kind == "" {
			kind = HubKindRemote
		}
		switch kind {
		case HubKindLocal:
			dir, dirErr := s.localFlightsDirFor(entry)
			if dirErr != nil {
				continue
			}
			for _, ref := range s.listLocalFlights(dir, localFlightNamespace(entry)) {
				add(ref)
			}
		case HubKindRemote, HubKindReadonly:
			flights, listErr := s.listRemoteFlights(ctx, name, entry)
			if listErr != nil {
				if warnings != nil {
					*warnings = append(*warnings, fmt.Sprintf("skipped hub %q: %v", name, listErr))
				}
				continue
			}
			for _, f := range flights {
				add((FlightRef{Namespace: f.Namespace, Slug: f.Slug}).Canonical())
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *FlightService) listLocalFlights(dir, namespace string) []string {
	entries, err := s.Runtime.ReadDir(dir)
	if err != nil {
		// A missing flights.d is "no flights", not an error.
		return []string{}
	}
	var refs []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if stem, ok := flightStem(name); ok {
			refs = append(refs, (FlightRef{Namespace: namespace, Slug: stem}).Canonical())
		}
	}
	sort.Strings(refs)
	return refs
}

// GetFlight loads a single flight by ref. Returns keg.ErrNotExist when no
// manifest exists. Unqualified refs first try local manifests for backward
// compatibility, then unique remote slug matches. Results are memoized for
// the life of the process (see flightCache).
func (s *FlightService) GetFlight(ctx context.Context, name string) (*Flight, error) {
	if f, ok := s.cachedFlight(name); ok {
		return f, nil
	}
	f, err := s.getFlight(ctx, name)
	if err != nil {
		return nil, err
	}
	s.storeFlight(name, f)
	return f, nil
}

// GetFlightFresh bypasses the process cache. MCP sessions use this before
// gated calls so a Hub revision or local manifest hash change invalidates the
// pinned session instead of silently changing its authority.
func (s *FlightService) GetFlightFresh(ctx context.Context, name string) (*Flight, error) {
	return s.getFlight(ctx, name)
}

func (s *FlightService) getFlight(ctx context.Context, name string) (*Flight, error) {
	cfg, err := s.config()
	if err != nil {
		return nil, err
	}
	ref, err := ParseFlightRef(name, "")
	if err != nil {
		return nil, err
	}
	if ref.Namespace != "" {
		return s.getFlightInNamespace(ctx, cfg, ref)
	}

	if f, err := s.getLocalFlightAnyHub(cfg, ref.Slug); err == nil {
		return f, nil
	} else if !errors.Is(err, keg.ErrNotExist) {
		return nil, err
	}

	var matches []*Flight
	for _, hubName := range s.allHubNames(cfg) {
		entry, ok := cfg.Hub(hubName)
		if !ok {
			continue
		}
		kind := strings.TrimSpace(entry.Kind)
		if kind == "" {
			kind = HubKindRemote
		}
		if kind == HubKindLocal {
			continue
		}
		flights, listErr := s.listRemoteFlights(ctx, hubName, entry)
		if listErr != nil {
			continue
		}
		for _, hf := range flights {
			if hf.Slug == ref.Slug {
				f := flightFromHub(hf, hubName)
				matches = append(matches, f)
			}
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return nil, fmt.Errorf("flight %q not found: %w", name, keg.ErrNotExist)
	default:
		return nil, fmt.Errorf("flight %q is ambiguous; use @namespace/+%s", name, ref.Slug)
	}
}

func (s *FlightService) getFlightInNamespace(ctx context.Context, cfg *Config, ref FlightRef) (*Flight, error) {
	hubName := cfg.resolveHubForNamespace(ref.Namespace)
	entry, ok := cfg.Hub(hubName)
	if !ok {
		return nil, fmt.Errorf("hub %q is not configured", hubName)
	}
	kind := strings.TrimSpace(entry.Kind)
	if kind == "" {
		kind = HubKindRemote
	}
	if kind == HubKindLocal {
		dir, err := s.localFlightsDirFor(entry)
		if err != nil {
			return nil, err
		}
		return s.getLocalFlight(dir, ref.Namespace, ref.Slug)
	}
	if strings.TrimSpace(entry.URL) == "" {
		return nil, fmt.Errorf("hub %q has no url configured", hubName)
	}
	token := s.hubToken(entry)
	if token == "" {
		return nil, fmt.Errorf("hub %q has no auth token (run `tap auth login --hub %s`)", hubName, strings.TrimSpace(entry.URL))
	}
	hf, err := GetHubFlight(ctx, entry.URL, token, ref.Namespace, ref.Slug)
	if err != nil {
		return nil, err
	}
	return flightFromHub(*hf, hubName), nil
}

func (s *FlightService) getLocalFlightAnyHub(cfg *Config, slug string) (*Flight, error) {
	for _, hubName := range s.allHubNames(cfg) {
		entry, ok := cfg.Hub(hubName)
		if !ok {
			continue
		}
		kind := strings.TrimSpace(entry.Kind)
		if kind != HubKindLocal {
			continue
		}
		dir, err := s.localFlightsDirFor(entry)
		if err != nil {
			return nil, err
		}
		f, err := s.getLocalFlight(dir, localFlightNamespace(entry), slug)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, keg.ErrNotExist) {
			return nil, err
		}
	}
	return nil, keg.ErrNotExist
}

func (s *FlightService) getLocalFlight(dir, namespace, slug string) (*Flight, error) {
	for _, ext := range []string{".yaml", ".yml"} {
		path := filepath.Join(dir, slug+ext)
		b, readErr := s.Runtime.ReadFile(path)
		if readErr != nil {
			continue
		}
		var m FlightManifest
		if err := yaml.Unmarshal(b, &m); err != nil {
			return nil, fmt.Errorf("parse flight %q: %w", slug, err)
		}
		if err := validateFlightManifest(&m); err != nil {
			return nil, fmt.Errorf("parse flight %q: %w", slug, err)
		}
		normalizeFlightManifest(&m)
		ref := FlightRef{Namespace: namespace, Slug: slug}
		return &Flight{Name: ref.Canonical(), Namespace: namespace, Slug: slug, Source: "local", ManifestHash: s.Runtime.Hasher().Hash(b), FlightManifest: m}, nil
	}
	return nil, fmt.Errorf("flight %q not found: %w", slug, keg.ErrNotExist)
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

func normalizeFlightManifest(m *FlightManifest) {
	if m == nil {
		return
	}
	if strings.TrimSpace(m.Visibility) == "" {
		m.Visibility = FlightVisibilityPrivate
	} else {
		m.Visibility = strings.TrimSpace(m.Visibility)
	}
	if len(m.Cover) == 0 && len(m.AllowedKegs) > 0 {
		for _, entry := range m.AllowedKegs {
			if c, ok := parseFlightCoverEntry(entry); ok {
				// Bare legacy entries keep their historical editor cap;
				// an explicit "=role" suffix is honored as written.
				if !strings.Contains(entry, "=") {
					c.Role = FlightRoleEditor
				}
				m.Cover = append(m.Cover, c)
			}
		}
	}
	for i := range m.Cover {
		m.Cover[i].Namespace = strings.TrimPrefix(strings.TrimSpace(m.Cover[i].Namespace), "@")
		m.Cover[i].Keg = strings.TrimSpace(m.Cover[i].Keg)
		m.Cover[i].Role = normalizeFlightRole(m.Cover[i].Role)
	}
}

func validateFlightManifest(m *FlightManifest) error {
	if m == nil {
		return nil
	}
	visibility := strings.TrimSpace(m.Visibility)
	if visibility != "" && visibility != FlightVisibilityPrivate && visibility != FlightVisibilityPublic {
		return fmt.Errorf("invalid flight visibility %q", visibility)
	}
	seen := map[FlightCapability]struct{}{}
	for _, capability := range m.Capabilities {
		capability = FlightCapability(strings.TrimSpace(string(capability)))
		if capability != FlightCapabilityManageFlights {
			return fmt.Errorf("unknown flight capability %q", capability)
		}
		if _, ok := seen[capability]; ok {
			return fmt.Errorf("duplicate flight capability %q", capability)
		}
		seen[capability] = struct{}{}
	}
	return nil
}

// HasCapability reports whether a validated manifest grants capability.
func (f *Flight) HasCapability(capability FlightCapability) bool {
	if f == nil {
		return false
	}
	for _, got := range f.Capabilities {
		if got == capability {
			return true
		}
	}
	return false
}

func parseFlightCoverEntry(entry string) (FlightCover, bool) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return FlightCover{}, false
	}
	entry, roleRaw, _ := strings.Cut(entry, "=")
	role := FlightRoleViewer
	if strings.TrimSpace(roleRaw) != "" {
		role = FlightRole(strings.TrimSpace(roleRaw))
	}
	entry = strings.TrimSpace(entry)
	var ns, name string
	if strings.HasPrefix(entry, "@") {
		ns, name, _ = strings.Cut(strings.TrimPrefix(entry, "@"), "/")
	} else {
		name = entry
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return FlightCover{}, false
	}
	return FlightCover{Namespace: strings.TrimSpace(ns), Keg: name, Role: normalizeFlightRole(role)}, true
}

// RoleFor reports the flight-scoped role for a keg. An empty cover denies all
// KEG access.
// Manifests normally arrive through normalizeFlightManifest, which folds
// legacy AllowedKegs into Cover; the fallback here only covers Flight values
// constructed by hand.
func (f *Flight) RoleFor(alias, namespace, kegName string) (FlightRole, bool) {
	if f == nil {
		return "", false
	}
	cover := append([]FlightCover(nil), f.Cover...)
	if len(cover) == 0 && len(f.AllowedKegs) > 0 {
		for _, entry := range f.AllowedKegs {
			if c, ok := parseFlightCoverEntry(entry); ok {
				if !strings.Contains(entry, "=") {
					c.Role = FlightRoleEditor
				}
				cover = append(cover, c)
			}
		}
	}
	if len(cover) == 0 {
		return "", false
	}
	namespace = strings.TrimPrefix(strings.TrimSpace(namespace), "@")
	kegName = strings.TrimSpace(kegName)
	alias = strings.TrimSpace(alias)
	for _, c := range cover {
		c.Namespace = strings.TrimPrefix(strings.TrimSpace(c.Namespace), "@")
		c.Keg = strings.TrimSpace(c.Keg)
		if c.Keg == "" {
			continue
		}
		if c.Namespace == "" {
			if (alias != "" && c.Keg == alias) || (kegName != "" && c.Keg == kegName) {
				return normalizeFlightRole(c.Role), true
			}
			continue
		}
		if c.Namespace == namespace && c.Keg == kegName {
			return normalizeFlightRole(c.Role), true
		}
	}
	return "", false
}

// ParseFlightCoverSpecs parses CLI/MCP cover specs of the form
// "keg", "@ns/keg", or "@ns/keg=role". Bare entries default to viewer,
// matching the manifest parser; editor must be requested explicitly.
func ParseFlightCoverSpecs(specs []string) ([]FlightCover, error) {
	out := make([]FlightCover, 0, len(specs))
	for _, spec := range specs {
		raw := strings.TrimSpace(spec)
		if raw == "" {
			continue
		}
		target, roleRaw, hasRole := strings.Cut(raw, "=")
		role := FlightRoleViewer
		if hasRole {
			switch FlightRole(strings.TrimSpace(roleRaw)) {
			case FlightRoleViewer:
				role = FlightRoleViewer
			case FlightRoleEditor:
				role = FlightRoleEditor
			default:
				return nil, fmt.Errorf("invalid flight cover role %q", roleRaw)
			}
		}
		target = strings.TrimSpace(target)
		var ns, kegName string
		if strings.HasPrefix(target, "@") {
			ns, kegName, _ = strings.Cut(strings.TrimPrefix(target, "@"), "/")
		} else {
			kegName = target
		}
		if strings.TrimSpace(kegName) == "" {
			return nil, fmt.Errorf("invalid flight cover %q", spec)
		}
		out = append(out, FlightCover{
			Namespace: strings.TrimSpace(ns),
			Keg:       strings.TrimSpace(kegName),
			Role:      role,
		})
	}
	return out, nil
}

func localFlightNamespace(entry HubEntry) string {
	if ns := strings.TrimPrefix(strings.TrimSpace(entry.DefaultNamespace), "@"); ns != "" {
		return ns
	}
	return LocalHubName
}

func (s *FlightService) allHubNames(cfg *Config) []string {
	hubs := cfg.Hubs()
	if len(hubs) == 0 {
		return dedupeStrings([]string{cfg.localHubName(), cfg.resolveHubName()})
	}
	names := make([]string, 0, len(hubs))
	for n := range hubs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (s *FlightService) listRemoteFlights(ctx context.Context, hubName string, entry HubEntry) ([]HubFlight, error) {
	url := strings.TrimSpace(entry.URL)
	if url == "" {
		return nil, fmt.Errorf("hub %q has no url configured", hubName)
	}
	token := s.hubToken(entry)
	if token == "" {
		return nil, fmt.Errorf("hub %q has no auth token (run `tap auth login --hub %s`)", hubName, url)
	}
	return ListUserFlights(ctx, url, token)
}

func (s *FlightService) hubToken(entry HubEntry) string {
	url := hubURLWithScheme(strings.TrimSpace(entry.URL))
	if url == "" {
		return ""
	}
	if entry.TokenEnv != "" {
		if v := s.Runtime.Get(entry.TokenEnv); v != "" {
			return v
		}
	}
	if entry.Token != "" {
		return entry.Token
	}
	target := keg.Target{
		Url:      strings.TrimRight(url, "/"),
		HubURL:   url,
		Token:    entry.Token,
		TokenEnv: entry.TokenEnv,
	}
	if s.ConfigService == nil {
		return ""
	}
	return s.kegService().tokenResolver().ResolveToken(&target)
}

func flightFromHub(hf HubFlight, hubName string) *Flight {
	cover := make([]FlightCover, 0, len(hf.Cover))
	for _, c := range hf.Cover {
		cover = append(cover, FlightCover{
			Namespace: c.Namespace,
			Keg:       c.Keg,
			Role:      normalizeFlightRole(FlightRole(c.Role)),
		})
	}
	m := FlightManifest{
		Title:        hf.Title,
		Visibility:   hf.Visibility,
		Capabilities: append([]FlightCapability{}, hf.Capabilities...),
		Cover:        cover,
		Instructions: hf.Instructions,
	}
	normalizeFlightManifest(&m)
	ref := FlightRef{Namespace: hf.Namespace, Slug: hf.Slug}
	return &Flight{
		Name:           ref.Canonical(),
		Namespace:      hf.Namespace,
		Slug:           hf.Slug,
		Source:         hubName,
		Revision:       hf.Revision,
		FlightManifest: m,
	}
}
