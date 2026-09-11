package tapper

import (
	"context"
	"fmt"
	"html"
	"io/fs"
	"sort"
	"strings"

	"github.com/jlrickert/tapper/pkg/apicontract"
	"github.com/jlrickert/tapper/pkg/integrations"
	"github.com/jlrickert/tapper/pkg/keg"
)

const orientPurpose = "Tapper provides an MCP interface for KEG (Knowledge Exchange Graph) systems. A KEG is a numbered collection of markdown nodes with metadata, links, tags, and snapshot history. Agents operate on a KEG through the `mcp__tapper__*` tools; reading or writing node files directly bypasses indexing, locking, and snapshots."

const orientRulesSummary = "Rules:\n" +
	"- Call `orient` first in every session, before any other tool and before replying. The active flight carries this session's instructions, so until you orient you do not know what the session is for.\n" +
	"- Call `orient` again after any context reset such as a clear or a compact. These instructions were delivered into the conversation and are discarded with it; the connection survives, so nothing re-sends them on its own. If you cannot tell whether you have oriented in the current context, you have not.\n" +
	"- These operating rules also ship once at initialization and do not change. The flight, its cover, and its instructions do change, and this payload is the only current source for them: a compaction summary may paraphrase a previous orientation, and an older copy can still be present. If anything you remember about the flight, its cover, or its instructions disagrees with what you are reading here, this is current and that is stale. Do not merge them; replace.\n" +
	"- Use the `mcp__tapper__*` tools for every KEG operation; never read or write node files directly.\n" +
	"- The target keg resolves from the working directory unless the `keg` parameter overrides it.\n" +
	"- Take a snapshot before non-trivial edits. Snapshots do not protect against `remove`; preserve content some other way before deletion.\n" +
	"- Writes may invalidate the hash you were holding; not every mutation returns a replacement. Re-read with `cat`, `schema_read`, or `keg_settings` before each guarded write; a hash never covers two writes, so an edit followed by a delete needs two reads.\n" +
	"- Node ids are per-keg counters. Node 4 in one keg has nothing to do with node 4 in another, ids are never reused after a removal, and a create takes the next free id rather than filling a gap.\n" +
	"- Node 0 is the keg's placeholder landing node. Leave it alone: it carries no `type` on purpose, it is where links to unwritten content land, and removing it makes the keg read as uninitialized. Write your content in a new node instead.\n" +
	"- Attachments on a node are linked relative to that node's own directory: `[label](./assets/FILE)` for files and `![alt](./images/IMAGE)` for images. Both directory names are plural.\n"

// OrientOptions is the input to Tap.Orient. Flight is the only selector used
// by orientation; the embedded target options remain for CLI profile
// compatibility and are intentionally absent from the MCP input.
type OrientOptions struct {
	KegTargetOptions
}

// Orient returns one deterministic KEG system payload. It is best-effort:
// flight and hub-listing failures do not suppress the core orientation
// document.
func (t *Tap) Orient(ctx context.Context, opts OrientOptions) (string, error) {
	// Orientation is the one reload boundary for configuration. Everywhere else
	// reads a snapshot fixed for the life of the process, so this is where an
	// edited config file takes effect. Unconditional, so an explicit --flight
	// still gets a keg listing built from the same fresh cascade as the
	// config-driven form.
	if t != nil && t.ConfigService != nil {
		t.ConfigService.Reload()
	}
	// Credentials reload on the same boundary. `tap auth login` runs in a
	// separate process, so without this a long-lived MCP session keeps the
	// token it loaded at startup and reorienting cannot clear a 401 (#87).
	if t != nil && t.KegService != nil {
		t.KegService.ReloadAuthStore()
	}
	flightName := t.ActiveFlightName(opts.Flight)
	flight, flightNote, err := t.resolveOrientFlight(ctx, flightName)
	if err != nil {
		return "", err
	}
	available, warnings, err := t.OrientationKegsForFlight(ctx, flight)
	if err != nil {
		return "", err
	}
	var authority *OrientationAuthority
	if strings.TrimSpace(flightName) == "" {
		available, warnings, err = t.IdentityKegCatalog(ctx)
		if err != nil {
			return "", err
		}
		flightNote = "No flight is configured, so normal identity-authorized full access applies. Pin a least-privilege flight outside MCP and start a new connection to narrow it."
		authority = &OrientationAuthority{FullAccess: true}
	}
	if flight != nil && flightNote == "" {
		graph, err := t.FlightService.ResolveFlightGraph(ctx, flight)
		if err != nil {
			return "", err
		}
		rows := append([]*Flight{graph.Root}, graph.Available...)
		authority = &OrientationAuthority{Root: graph.Root, Active: flight, Children: ImmediateFlightChildren(flight, rows)}
	}
	payload, err := BuildOrientationPayload(flight, flightNote, t.ActiveAgentName(), available, warnings, authority)
	if err != nil {
		return "", err
	}
	return strings.Replace(
		payload,
		"3. In MCP, call `session_refresh`, then `orient` on this same connection. The stateless CLI preview is refreshed by running `tap orient` again.",
		"3. Run this stateless preview again after the user changes the selection.",
		1,
	), nil
}

// OrientationKegsForFlight returns the effective KEG authority projection
// without rendering a payload. Providers use it to compute the revision first
// and then render exactly once.
func (t *Tap) OrientationKegsForFlight(ctx context.Context, flight *Flight) ([]OrientationKeg, []string, error) {
	rows, warnings, err := t.IdentityKegCatalog(ctx)
	if err != nil {
		return nil, warnings, err
	}
	if flight == nil {
		return nil, warnings, nil
	}
	return ProjectOrientationKegs(flight, rows), warnings, nil
}

// IdentityKegCatalog discovers the identity-authorized KEGs from every
// configured hub without applying flight authority. Each hub is queried at
// most once. Callers must explicitly project these rows through a selected
// flight or use them only for identity search.
func (t *Tap) IdentityKegCatalog(ctx context.Context) ([]OrientationKeg, []string, error) {
	return t.identityKegCatalog(ctx)
}

// ProjectOrientationKegs applies one flight's cover to a previously loaded
// identity projection. The returned rows retain the identity role and record
// the independent flight cap so callers can compute the lesser effective role.
func ProjectOrientationKegs(flight *Flight, rows []OrientationKeg) []OrientationKeg {
	if flight == nil {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]OrientationKeg, 0, len(rows))
	for _, row := range rows {
		capRole, ok := flightCapForKeg(flight, row.Namespace, row.Alias)
		if !ok {
			continue
		}
		row.FlightCap = capRole
		row.Flights = []string{flight.Name}
		if _, duplicate := seen[row.Ref]; duplicate {
			continue
		}
		seen[row.Ref] = struct{}{}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

// ActiveFlightName resolves an explicit flight, falling back to the flight in
// the process configuration snapshot. It is a pure read: it neither writes
// configuration nor reloads it, so callers that need a fresh cascade call
// ConfigService.Reload at their own boundary (see Orient and the MCP session
// gate).
func (t *Tap) ActiveFlightName(explicit string) string {
	if name := strings.TrimSpace(explicit); name != "" {
		return name
	}
	if t == nil || t.ConfigService == nil {
		return ""
	}
	cfg, err := t.ConfigService.Config()
	if err != nil || cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Flight())
}

// ActiveAgentName reports the agent driving this process, or "" when none is
// selected. Like ActiveFlightName it is a pure read of the current snapshot;
// the value only changes when a caller reloads.
func (t *Tap) ActiveAgentName() string {
	if t == nil || t.ConfigService == nil {
		return ""
	}
	cfg, err := t.ConfigService.Config()
	if err != nil || cfg == nil {
		return ""
	}
	return cfg.AgentName()
}

func (t *Tap) resolveOrientFlight(ctx context.Context, name string) (*Flight, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || t == nil || t.FlightService == nil {
		return nil, "", nil
	}
	flight, err := t.FlightService.GetFlightFresh(ctx, name)
	if err != nil {
		if apicontract.IsCompatibility(err) {
			return nil, "", err
		}
		return &Flight{Name: name}, fmt.Sprintf("Flight %q is unavailable: %v", name, err), nil
	}
	return flight, "", nil
}

// OrientationKeg is one effective KEG exposed by an orientation context.
type OrientationKeg struct {
	CoverSource string
	Distance    int
	Ref         string
	Namespace   string
	Alias       string
	Title       string
	Description string
	Role        string
	Source      string
	Visibility  string
	FlightCap   string
	Flights     []string
}

// OrientationAuthority describes the connection-pinned launch root and the flight
// selected from its live transitive graph for this call.
type OrientationAuthority struct {
	Children         []*Flight
	Root             *Flight
	Active           *Flight
	Path             []string
	AvailableFlights []string
	Revision         string
	FullAccess       bool
}

func (t *Tap) identityKegCatalog(ctx context.Context) ([]OrientationKeg, []string, error) {
	if t == nil || t.ConfigService == nil {
		return nil, []string{"KEG listing unavailable: no config service is configured."}, nil
	}
	cfg, loadWarnings, err := t.ConfigService.Load()
	if err != nil {
		return nil, []string{fmt.Sprintf("KEG listing unavailable: %v", err)}, nil
	}

	var warnings []string
	// Agent-selection warnings are the one config-load class that belongs in the
	// payload: they explain why the session is on a different flight than the
	// user expects, and the reader is the only one who can fix it. The rest stay
	// out so orientation does not turn into a config linter.
	for _, w := range loadWarnings {
		if w.Source == "agent" {
			warnings = append(warnings, w.Message)
		}
	}
	var out []OrientationKeg
	for _, hubName := range t.allHubNames(cfg) {
		entry, ok := cfg.Hub(hubName)
		if !ok {
			continue
		}
		rows, err := t.orientKegsForHub(ctx, cfg, hubName, entry)
		if err != nil {
			if apicontract.IsCompatibility(err) {
				return nil, warnings, err
			}
			warnings = append(warnings, fmt.Sprintf("skipped hub %q: %v", hubName, err))
			continue
		}
		for _, row := range rows {
			out = append(out, row)
		}
	}
	seen := map[string]struct{}{}
	identity := make([]OrientationKeg, 0, len(out))
	for _, row := range out {
		if _, duplicate := seen[row.Ref]; duplicate {
			continue
		}
		seen[row.Ref] = struct{}{}
		row.FlightCap = ""
		row.Flights = nil
		identity = append(identity, row)
	}
	sort.Slice(identity, func(i, j int) bool { return identity[i].Ref < identity[j].Ref })
	return identity, warnings, nil
}

func (t *Tap) orientKegsForHub(ctx context.Context, _ *Config, hubName string, entry HubEntry) ([]OrientationKeg, error) {
	url := strings.TrimSpace(entry.URL)
	if url == "" {
		return nil, fmt.Errorf("hub has no url configured")
	}
	token := t.hubToken(entry)
	if token == "" {
		return nil, fmt.Errorf("hub has no authenticated session for %s", url)
	}
	kegs, err := ListUserKegs(ctx, url, token)
	if err != nil {
		return nil, err
	}
	out := make([]OrientationKeg, 0, len(kegs))
	for _, k := range kegs {
		row := OrientationKeg{
			Ref:         "@" + k.Namespace + "/" + k.Alias,
			Namespace:   k.Namespace,
			Alias:       k.Alias,
			Title:       k.Title,
			Description: k.Description,
			Role:        k.Role,
			Source:      hubName,
			Visibility:  k.Visibility,
		}
		out = append(out, row)
	}
	return out, nil
}

func flightCapForKeg(flight *Flight, namespace, alias string) (string, bool) {
	if flight == nil {
		return "", false
	}
	if flight.HasCapability(FlightCapabilityFullAccess) {
		return string(FlightRoleAdmin), true
	}
	if len(flight.Cover) == 0 {
		return "", false
	}
	namespace = strings.TrimPrefix(strings.TrimSpace(namespace), "@")
	alias = strings.TrimSpace(alias)
	for _, c := range flight.Cover {
		cns := strings.TrimPrefix(strings.TrimSpace(c.Namespace), "@")
		ckeg := strings.TrimSpace(c.Keg)
		if ckeg == "" {
			continue
		}
		if cns == "" && ckeg == alias {
			return string(normalizeFlightRole(c.Role)), true
		}
		if cns == namespace && ckeg == alias {
			return string(normalizeFlightRole(c.Role)), true
		}
	}
	return "", false
}

// EffectiveOrientationRole intersects the identity role with the flight cap.
// Identity catalog rows have no cap and therefore retain their identity role
// only for metadata search; operational projections always carry a cap.
func EffectiveOrientationRole(row OrientationKeg) string {
	identity := orientationRoleRank(row.Role)
	if strings.TrimSpace(row.FlightCap) == "" {
		return orientationRoleName(identity)
	}
	capRole := orientationRoleRank(row.FlightCap)
	if identity < capRole {
		return orientationRoleName(identity)
	}
	return orientationRoleName(capRole)
}

func orientationRoleRank(role string) int {
	switch strings.TrimSpace(role) {
	case string(FlightRoleAdmin):
		return 3
	case string(FlightRoleEditor):
		return 2
	default:
		return 1
	}
}

func orientationRoleName(rank int) string {
	switch rank {
	case 3:
		return string(FlightRoleAdmin)
	case 2:
		return string(FlightRoleEditor)
	default:
		return string(FlightRoleViewer)
	}
}

func splitKegRef(ref string) (namespace, alias string, ok bool) {
	ns, rest, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(ref), "@"), "/")
	if !ok || ns == "" || rest == "" {
		return "", "", false
	}
	return ns, rest, true
}

func kegRefLabel(target *keg.Target) string {
	if target == nil {
		return ""
	}
	name := strings.TrimSpace(target.KegName)
	if name == "" {
		return ""
	}
	if ns := strings.TrimSpace(target.Namespace); ns != "" {
		return "@" + ns + "/" + name
	}
	return name
}

// OrientationOperatingRules returns the static KEG operating preamble: what a
// KEG is, and the rules for working in one. It carries no session state, so an
// MCP server can deliver it once at initialization and let a caller that
// already knows which flight to pass start work without orienting first.
//
// Orientation carries a concise reminder after context resets. The guide tool
// serves this full preamble and canonical operating guidance on demand.
func OrientationOperatingRules() string {
	return "# KEG System\n\n" + orientPurpose + "\n\n" + orientRulesSummary
}

// BuildOrientationPayload renders the provider-neutral orientation document
// from one flight snapshot and its effective KEG listing. agent names
// the `tap launch` agent driving the session, or "" when a human is; it is
// reported because it explains where the flight came from and how to change it.
func BuildOrientationPayload(flight *Flight, flightNote, agent string, kegs []OrientationKeg, warnings []string, authority *OrientationAuthority) (string, error) {
	var b strings.Builder
	b.WriteString("# KEG System\n\n")
	b.WriteString("Call `orient` at session start and after every context reset. Authority is bounded by identity permissions and the selected flight; child authority and instructions are not inherited. Use only `mcp__tapper__*` tools for KEG operations. Call `keg_settings` before operating in a KEG. Snapshot before meaningful edits; snapshots do not protect deletion. Re-read for a fresh hash before each guarded write. Call `guide` for detailed operating, authoring/linking, snapshots, tools, or troubleshooting guidance.\n\n")
	for _, warning := range warnings {
		fmt.Fprintf(&b, "Warning: %s\n\n", orientationTableCell(warning))
	}

	if agent != "" {
		fmt.Fprintf(&b, "Session agent `%s` selects only the model and telemetry identity; it cannot select or replace the connection root.\n\n", agent)
	}
	b.WriteString("## Flight\n\n")
	if flightNote != "" {
		b.WriteString(flightNote + "\n\n")
	}
	if flight == nil {
		if authority != nil && authority.FullAccess {
			b.WriteString("No flight is active. This session runs under your full identity authority; nothing here is scoped. Use `keg_search` and `flight_search` to discover readable resources; results confer no access. An explicit flight selects only that flight for one call. Pin a least-privilege flight outside MCP and start a new connection to narrow the session.\n")
		} else {
			b.WriteString("The configured flight is unavailable: fail-closed recovery; KEG tools are locked. Repair the selection, then call `session_refresh` and `orient`. Use `flight_search`, `list_flights`, `flight_show`, or `auth_info` for recovery.\n")
		}
		return b.String(), nil
	}
	fmt.Fprintf(&b, "## %s\n\nSelected flight: `%s`\n\n", orientationTableCell(resourceTitle(flight.Title, flight.Name)), flight.Name)
	if flight.Description != "" {
		b.WriteString(orientationTableCell(DescriptionPreview(flight.Description)) + "\n\n")
	}
	if authority != nil {
		if authority.FullAccess {
			b.WriteString("Launch root: (none; identity-authorized full access)\n\n")
		}
		if authority.Root != nil {
			fmt.Fprintf(&b, "Launch root: `%s`\n\n", authority.Root.Name)
		}
		if authority.Revision != "" {
			fmt.Fprintf(&b, "Authority revision: `%s`\n\n", authority.Revision)
		}
	}
	fmt.Fprintf(&b, "Effective authority: identity permissions intersect this flight's cover and capabilities (%s). Explicit selection never changes the connection root.\n\n", orientationTableCell(strings.Join(flightCapabilitiesText(flight), ", ")))
	b.WriteString("### Active instructions\n\n")
	b.WriteString(flight.Instructions)
	b.WriteString("\n\n### Effective KEG cover\n\n")
	// Full-access capabilities expand authority, but discovery remains direct-cover only.
	direct := *flight
	direct.Capabilities = nil
	usable := ProjectOrientationKegs(&direct, kegs)
	for i := range usable {
		cap, _ := flightCapForKeg(flight, usable[i].Namespace, usable[i].Alias)
		usable[i].FlightCap = cap
	}
	if len(usable) == 0 {
		b.WriteString("No covered readable KEGs.\n")
	} else {
		writeOrientationKegTable(&b, usable, "")
	}
	b.WriteString("\nInspect a flight explicitly to see its children. Use `keg_search` and `flight_search` for metadata discovery. Select a reachable descendant explicitly with the `flight` argument; its instructions apply only to that call.\n")
	return b.String(), nil
}

func flightCapabilitiesText(f *Flight) []string {
	out := make([]string, 0, len(f.Capabilities))
	for _, c := range f.Capabilities {
		out = append(out, string(c))
	}
	if len(out) == 0 {
		return []string{"none"}
	}
	return out
}
func resourceTitle(title, ref string) string {
	if strings.TrimSpace(title) == "" {
		return ref
	}
	return title
}

// DescriptionPreview collapses whitespace and bounds discovery text to 240 Unicode characters.
func DescriptionPreview(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	chars := []rune(value)
	if len(chars) > 240 {
		return string(chars[:239]) + "…"
	}
	return value
}

// ImmediateFlightChildren filters existing readable graph/catalog projections without fetching manifests.
func ImmediateFlightChildren(active *Flight, rows []*Flight) []*Flight {
	if active == nil {
		return nil
	}
	byRef := map[string]*Flight{}
	for _, row := range rows {
		if row != nil {
			byRef[row.Name] = row
		}
	}
	out := []*Flight{}
	for _, name := range active.Subflights {
		ref, err := ParseFlightRef(name, active.Namespace)
		if err != nil {
			continue
		}
		if child := byRef[ref.Canonical()]; child != nil {
			out = append(out, child)
		}
	}
	return out
}

// OrientationGuide serves canonical detailed guidance on demand.
func OrientationGuide(topic string) (string, error) {
	var names []string
	switch topic {
	case "operating":
		names = []string{"secret-handling.md", "agent-orient.md"}
	case "authoring":
		names = []string{"authoring.md"}
	case "linking":
		names = []string{"linking.md"}
	case "snapshots":
		names = []string{"snapshot-policy.md"}
	case "tools":
		names = []string{"tool-inventory.md"}
	case "troubleshooting":
		names = []string{"troubleshooting.md"}
	default:
		return "", fmt.Errorf("unknown guide topic %q; use operating, authoring, linking, snapshots, tools, or troubleshooting", topic)
	}
	var b strings.Builder
	if topic == "operating" {
		b.WriteString(OrientationOperatingRules() + "\n")
	}
	for _, name := range names {
		if err := appendCanonical(&b, name); err != nil {
			return "", err
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// partitionOrientationKegs splits a listing into KEGs the active flight covers
// itself and KEGs only a descendant covers.
//
// Membership and the reported role both come from the active flight's own
// cover, never from the row's Flights provenance. AggregateOrientationKegs
// retains every granting flight while pricing the row at the highest effective
// role, so provenance alone cannot say which role this particular call gets.

// Re-price against the active flight instead: otherwise a lower-cap root could
// quote a descendant's higher role or hide a KEG behind a selection it does not
// need.
//
// A nil flight with rows is no-flight full access, where the identity listing
// is already the operational projection. Failed-root recovery passes no rows.
func partitionOrientationKegs(flight *Flight, kegs []OrientationKeg) (usable, viaSubflight []OrientationKeg) {
	if flight == nil {
		return kegs, nil
	}
	for _, k := range kegs {
		capRole, covered := flightCapForKeg(flight, k.Namespace, k.Alias)
		if !covered {
			viaSubflight = append(viaSubflight, k)
			continue
		}
		// Re-price against the active flight so the displayed role is the one
		// this call would actually get.
		k.FlightCap = capRole
		k.Flights = []string{flight.Name}
		usable = append(usable, k)
	}
	return usable, viaSubflight
}

func writeOrientationKegTable(b *strings.Builder, kegs []OrientationKeg, _ string) {
	provenance := false
	for _, row := range kegs {
		provenance = provenance || row.CoverSource != ""
	}
	if provenance {
		b.WriteString("| Title | Reference | Description | Role | Cover source | Distance |\n| --- | --- | --- | --- | --- | --- |\n")
	} else {
		b.WriteString("| Title | Reference | Description | Role |\n| --- | --- | --- | --- |\n")
	}
	for _, k := range kegs {
		fmt.Fprintf(b, "| %s | `%s` | %s | %s |", orientationTableCell(resourceTitle(k.Title, k.Ref)), k.Ref, orientationTableCell(DescriptionPreview(k.Description)), EffectiveOrientationRole(k))
		if provenance {
			fmt.Fprintf(b, " %s | %d |", orientationTableCell(k.CoverSource), k.Distance)
		}
		b.WriteString("\n")
	}
}

func orientationTableCell(value string) string {
	value = html.EscapeString(strings.TrimSpace(value))

	value = strings.ReplaceAll(value, "\\", "\\\\")
	for _, ch := range []string{"*", "_", "[", "]", "`", "#"} {
		value = strings.ReplaceAll(value, ch, "\\"+ch)
	}
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r\n", "<br>")
	value = strings.ReplaceAll(value, "\n", "<br>")
	if value == "" {
		return "—"
	}
	return value
}

func appendCanonical(b *strings.Builder, name string) error {
	raw, err := fs.ReadFile(integrations.IntegrationsFS, "content/"+name)
	if err != nil {
		return fmt.Errorf("orient: canonical %s: %w", name, err)
	}
	b.Write(raw)
	if n := len(raw); n == 0 || raw[n-1] != '\n' {
		b.WriteByte('\n')
	}
	return nil
}
