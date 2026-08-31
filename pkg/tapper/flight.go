package tapper

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/schemas"
)

const (
	// FlightManifestSchemaURL is the published JSON Schema for flight manifest
	// YAML. Editor modelines prefer the local copy materialized by pkg/schemas
	// and fall back to this.
	FlightManifestSchemaURL = schemas.FlightManifestURL
)

type FlightRole string

type FlightCapability string

const (
	FlightRoleViewer FlightRole = "viewer"
	FlightRoleEditor FlightRole = "editor"
	FlightRoleAdmin  FlightRole = "admin"

	FlightVisibilityPrivate = "private"
	FlightVisibilityPublic  = "public"

	FlightCapabilityManageFlights FlightCapability = "manage_flights"
	FlightCapabilityManageKegs    FlightCapability = "manage_kegs"
	FlightCapabilityFullAccess    FlightCapability = "full_access"

	// MaxFlightSubflights bounds the ordered direct allowlist on one manifest.
	MaxFlightSubflights = 64
	// MaxFlightGraphDescendants bounds unique reachable flights, excluding the
	// pinned root. Shared descendants count once. This is the only bound on
	// traversal: it is enforced inline during the breadth-first walk and is
	// dedup-based, so it holds regardless of the graph's shape.
	MaxFlightGraphDescendants = 256
)

// ErrFlightSubflightNotAllowed marks a selection outside the immutable root's
// live, identity-accessible transitive graph. The historical name is retained
// for callers that classify this authority denial; selection is no longer
// limited to direct children.
var ErrFlightSubflightNotAllowed = errors.New("flight is not available from the pinned root")

// AtLeast reports whether r grants at least want within a flight cover.
func (r FlightRole) AtLeast(want FlightRole) bool {
	r = normalizeFlightRole(r)
	want = normalizeFlightRole(want)
	rank := map[FlightRole]int{
		FlightRoleViewer: 1,
		FlightRoleEditor: 2,
		FlightRoleAdmin:  3,
	}
	gotRank, gotOK := rank[r]
	wantRank, wantOK := rank[want]
	return gotOK && wantOK && gotRank >= wantRank
}

func normalizeFlightRole(role FlightRole) FlightRole {
	switch FlightRole(strings.TrimSpace(string(role))) {
	case FlightRoleAdmin:
		return FlightRoleAdmin
	case FlightRoleEditor:
		return FlightRoleEditor
	case "", FlightRoleViewer:
		return FlightRoleViewer
	default:
		return FlightRole(strings.TrimSpace(string(role)))
	}
}

type FlightCover struct {
	Namespace string     `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Keg       string     `yaml:"keg" json:"keg"`
	Role      FlightRole `yaml:"role" json:"role"`
}

// FlightManifest is the Hub API shape of a flight: explicit covered kegs plus
// markdown instructions. AllowedKegs remains a legacy wire field and is
// normalized into editor-cap cover entries.
type FlightManifest struct {
	Title        string             `yaml:"title,omitempty" json:"title,omitempty"`
	Visibility   string             `yaml:"visibility,omitempty" json:"visibility,omitempty"`
	Capabilities []FlightCapability `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
	Cover        []FlightCover      `yaml:"cover,omitempty" json:"cover,omitempty"`
	Subflights   []string           `yaml:"subflights,omitempty" json:"subflights,omitempty"`
	AllowedKegs  []string           `yaml:"allowedKegs,omitempty" json:"allowedKegs,omitempty"`
	Instructions string             `yaml:"instructions,omitempty" json:"instructions,omitempty"`
}

// Flight is a discovered flight: its manifest plus provenance.
type Flight struct {
	Name         string `yaml:"-" json:"name,omitempty"`
	Namespace    string `yaml:"-" json:"namespace,omitempty"`
	Slug         string `yaml:"-" json:"slug,omitempty"`
	Source       string `yaml:"-" json:"source,omitempty"` // configured hub name
	ManifestHash string `yaml:"-" json:"-"`
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
			return FlightRef{}, fmt.Errorf(
				"invalid flight reference %q: use @namespace/+slug, or a bare +slug for the default namespace", raw)
		}
		// Validate here rather than letting the segment travel. The `+` sigil
		// marks the slug, so "@+slug/..." puts it where the namespace belongs —
		// an easy transposition that otherwise reaches the hub as a namespace
		// that cannot exist and comes back as an opaque 404.
		if err := ValidateNamespace(ns); err != nil {
			return FlightRef{}, fmt.Errorf(
				"invalid flight reference %q: %w; the + sigil marks the slug, so write @namespace/+slug", raw, err)
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

// FlightService discovers and loads flights from configured remote hubs.
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
	cfg, err := s.ConfigService.Config()
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// ListFlights returns canonical @namespace/+slug refs for flights discovered
// across configured hubs, sorted. When hub is non-empty, discovery is limited
// to that configured hub. Remote/auth/network errors are best-effort so shell
// completion and discovery remain responsive; when warnings is non-nil each
// skipped hub appends one message so user-facing listings can say what was
// left out.
func (s *FlightService) ListFlights(ctx context.Context, hub string, warnings *[]string) ([]string, error) {
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

	hub = strings.TrimSpace(hub)
	hubNames := s.allHubNames(cfg)
	if hub != "" {
		if _, ok := cfg.Hub(hub); !ok {
			return nil, fmt.Errorf("hub %q is not configured", hub)
		}
		hubNames = []string{hub}
	}

	for _, name := range hubNames {
		entry, ok := cfg.Hub(name)
		if !ok {
			continue
		}
		kind := strings.TrimSpace(entry.Kind)
		if kind == "" {
			kind = HubKindRemote
		}
		switch kind {
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
		default:
			if warnings != nil {
				*warnings = append(*warnings, fmt.Sprintf("skipped hub %q: unsupported kind %q", name, kind))
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

// GetFlight loads a single Hub flight by ref. Returns keg.ErrNotExist when no
// manifest exists. Unqualified refs resolve through unique Hub slug matches.
// Results are memoized for the life of the process (see flightCache).
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
// gated calls so a manifest change invalidates the pinned session instead of
// silently changing its authority.
func (s *FlightService) GetFlightFresh(ctx context.Context, name string) (*Flight, error) {
	return s.getFlight(ctx, name)
}

// ResolveFlightGraph reloads and flattens the identity-accessible transitive
// descendants of root. It intentionally bypasses the process cache so every
// MCP call observes live relations and manifests.
func (s *FlightService) ResolveFlightGraph(ctx context.Context, root *Flight) (*FlightGraph, error) {
	if root == nil {
		return nil, errors.New("root flight is required")
	}
	cfg, err := s.config()
	if err != nil {
		return nil, err
	}
	entry, ok := cfg.Hub(root.Source)
	if !ok {
		return nil, fmt.Errorf("root flight source %q is not a configured hub", root.Source)
	}
	flights, err := s.listRemoteFlights(ctx, root.Source, entry)
	if err != nil {
		return nil, err
	}
	byRef := make(map[string]*Flight, len(flights))
	for _, manifest := range flights {
		flight := flightFromHub(manifest, root.Source)
		byRef[flight.Name] = flight
	}
	root = byRef[root.Name]
	if root == nil {
		return nil, fmt.Errorf("root flight is no longer accessible: %w", keg.ErrNotExist)
	}
	return FlattenFlightGraph(ctx, root, func(_ context.Context, ref string) (*Flight, error) {
		flight := byRef[ref]
		if flight == nil {
			return nil, keg.ErrForbidden
		}
		return flight, nil
	})
}

// FlightGraph is a deterministic breadth-first projection rooted at Root.
// Available excludes the root and contains each accessible descendant once.
// Paths holds the first (therefore shortest and ordered) canonical path found.
type FlightGraph struct {
	Root      *Flight
	Available []*Flight
	Paths     map[string][]string
	byName    map[string]*Flight
}

// AvailableRefs returns the flattened canonical descendant list.
func (g *FlightGraph) AvailableRefs() []string {
	if g == nil {
		return nil
	}
	out := make([]string, 0, len(g.Available))
	for _, flight := range g.Available {
		out = append(out, flight.Name)
	}
	return out
}

// Select resolves omitted input to the root and an explicit input to either
// the root or an accessible flattened descendant. Per-tool +slug references
// are relative to the pinned root namespace.
func (g *FlightGraph) Select(raw string) (*Flight, []string, error) {
	if g == nil || g.Root == nil {
		return nil, nil, errors.New("flight graph root is required")
	}
	if strings.TrimSpace(raw) == "" {
		return g.Root, append([]string(nil), g.Paths[g.Root.Name]...), nil
	}
	ref, err := ParseFlightRef(raw, g.Root.Namespace)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrFlightSubflightNotAllowed, err)
	}
	canonical := ref.Canonical()
	flight := g.byName[canonical]
	if flight == nil {
		return nil, nil, fmt.Errorf("%w: %s is outside the accessible graph rooted at %s", ErrFlightSubflightNotAllowed, canonical, g.Root.Name)
	}
	return flight, append([]string(nil), g.Paths[canonical]...), nil
}

// FlattenFlightGraph loads an ordered, bounded, single-source flight graph.
// A missing/forbidden descendant excludes that branch; all other load errors
// make the runtime graph unavailable. The root is supplied by the caller so
// root-loss classification remains transport-specific.
func FlattenFlightGraph(ctx context.Context, root *Flight, fetch func(context.Context, string) (*Flight, error)) (*FlightGraph, error) {
	if root == nil || fetch == nil {
		return nil, errors.New("root flight and fetch function are required")
	}
	if root.Name == "" {
		return nil, errors.New("root flight canonical name is required")
	}
	type queued struct {
		flight *Flight
	}
	graph := &FlightGraph{
		Root:   root,
		Paths:  map[string][]string{root.Name: {root.Name}},
		byName: map[string]*Flight{root.Name: root},
	}
	queue := []queued{{flight: root}}
	visited := map[string]bool{}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		parent := item.flight
		if visited[parent.Name] {
			continue
		}
		visited[parent.Name] = true
		if len(parent.Subflights) > MaxFlightSubflights {
			return nil, fmt.Errorf("flight %s exceeds maximum direct subflight count %d", parent.Name, MaxFlightSubflights)
		}
		seenDirect := map[string]struct{}{}
		for _, raw := range parent.Subflights {
			ref, err := ParseFlightRef(raw, parent.Namespace)
			if err != nil {
				return nil, fmt.Errorf("flight %s has invalid subflight %q: %w", parent.Name, raw, err)
			}
			childRef := ref.Canonical()
			if _, duplicate := seenDirect[childRef]; duplicate {
				return nil, fmt.Errorf("flight %s has duplicate canonical subflight %s", parent.Name, childRef)
			}
			seenDirect[childRef] = struct{}{}
			if _, known := graph.byName[childRef]; known {
				continue
			}
			child, err := fetch(ctx, childRef)
			if err != nil {
				if errors.Is(err, keg.ErrNotExist) || errors.Is(err, keg.ErrForbidden) || errors.Is(err, keg.ErrUnauthorized) {
					continue
				}
				return nil, fmt.Errorf("load descendant %s: %w", childRef, err)
			}
			if child == nil {
				continue
			}
			if child.Name != childRef {
				return nil, fmt.Errorf("descendant %s loaded as non-canonical flight %s", childRef, child.Name)
			}
			if child.Source != root.Source {
				return nil, fmt.Errorf("flight %s is on source %q, outside root source %q", childRef, child.Source, root.Source)
			}
			if len(graph.Available) >= MaxFlightGraphDescendants {
				return nil, fmt.Errorf("flight graph rooted at %s exceeds maximum unique descendant count %d", root.Name, MaxFlightGraphDescendants)
			}
			graph.byName[childRef] = child
			graph.Available = append(graph.Available, child)
			path := append([]string(nil), graph.Paths[parent.Name]...)
			graph.Paths[childRef] = append(path, childRef)
			queue = append(queue, queued{flight: child})
		}
	}
	// No cycle or depth pass. A subflight relation is an ordered list entry on
	// its parent, not an assertion about the shape of the whole graph, so the
	// walk above tolerates any shape: `visited` skips a parent already
	// expanded and `graph.byName` skips a child already loaded, which makes a
	// cycle finite rather than fatal. Authority is never inherited from an
	// ancestor, so mutual reference grants nothing. Traversal cost is bounded
	// by MaxFlightGraphDescendants, checked inline above and dedup-based, so
	// it holds for cyclic graphs too.
	return graph, nil
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
		if kind != HubKindRemote && kind != HubKindReadonly {
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
	if kind != HubKindRemote && kind != HubKindReadonly {
		return nil, fmt.Errorf("hub %q has unsupported kind %q", hubName, kind)
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

func normalizeFlightManifest(m *FlightManifest) {
	if m == nil {
		return
	}
	if strings.TrimSpace(m.Visibility) == "" {
		m.Visibility = FlightVisibilityPrivate
	} else {
		m.Visibility = strings.TrimSpace(m.Visibility)
	}
	for i := range m.Capabilities {
		m.Capabilities[i] = FlightCapability(strings.TrimSpace(string(m.Capabilities[i])))
	}
	sort.Slice(m.Capabilities, func(i, j int) bool {
		return m.Capabilities[i] < m.Capabilities[j]
	})
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
	for i := range m.Subflights {
		m.Subflights[i] = strings.TrimSpace(m.Subflights[i])
	}
}

// hashFlightManifest returns an internal change token for a normalized flight
// manifest. JSON field order is fixed by FlightManifest's struct definition,
// and SHA-256 keeps client and Hub revision comparison deterministic.
func hashFlightManifest(m FlightManifest) string {
	normalizeFlightManifest(&m)
	b, err := json.Marshal(m)
	if err != nil {
		panic(fmt.Sprintf("marshal normalized flight manifest: %v", err))
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// FlightManifestHash returns the deterministic revision token for a manifest.
func FlightManifestHash(m FlightManifest) string { return hashFlightManifest(m) }

func validateFlightManifest(m *FlightManifest, namespace string) error {
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
		switch capability {
		case FlightCapabilityManageFlights, FlightCapabilityManageKegs, FlightCapabilityFullAccess:
		default:
			return fmt.Errorf("unknown flight capability %q", capability)
		}
		if _, ok := seen[capability]; ok {
			return fmt.Errorf("duplicate flight capability %q", capability)
		}
		seen[capability] = struct{}{}
	}
	for _, cover := range m.Cover {
		switch normalizeFlightRole(cover.Role) {
		case FlightRoleViewer, FlightRoleEditor, FlightRoleAdmin:
		default:
			return fmt.Errorf("invalid flight cover role %q", cover.Role)
		}
	}
	for _, entry := range m.AllowedKegs {
		_, roleRaw, hasRole := strings.Cut(entry, "=")
		if !hasRole {
			continue
		}
		switch normalizeFlightRole(FlightRole(roleRaw)) {
		case FlightRoleViewer, FlightRoleEditor, FlightRoleAdmin:
		default:
			return fmt.Errorf("invalid flight cover role %q", strings.TrimSpace(roleRaw))
		}
	}
	if len(m.Subflights) > MaxFlightSubflights {
		return fmt.Errorf("subflight count exceeds %d", MaxFlightSubflights)
	}
	seenSubflights := map[string]struct{}{}
	for _, raw := range m.Subflights {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return errors.New("subflight reference cannot be empty")
		}
		ref, err := ParseFlightRef(raw, namespace)
		if err != nil {
			return fmt.Errorf("invalid subflight reference %q: %w", raw, err)
		}
		canonical := ref.Canonical()
		if _, duplicate := seenSubflights[canonical]; duplicate {
			return fmt.Errorf("duplicate subflight reference %q", raw)
		}
		seenSubflights[canonical] = struct{}{}
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
			case FlightRoleAdmin:
				role = FlightRoleAdmin
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

func (s *FlightService) allHubNames(cfg *Config) []string {
	hubs := cfg.Hubs()
	if len(hubs) == 0 {
		return dedupeStrings([]string{cfg.resolveHubName()})
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
		Subflights:   append([]string(nil), hf.Subflights...),
		Instructions: hf.Instructions,
	}
	normalizeFlightManifest(&m)
	ref := FlightRef{Namespace: hf.Namespace, Slug: hf.Slug}
	manifestHash := hf.Hash
	if manifestHash == "" {
		manifestHash = hashFlightManifest(m)
	}
	return &Flight{
		Name:           ref.Canonical(),
		Namespace:      hf.Namespace,
		Slug:           hf.Slug,
		Source:         hubName,
		ManifestHash:   manifestHash,
		FlightManifest: m,
	}
}
