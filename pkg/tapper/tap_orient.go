package tapper

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

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
	"- Every successful write returns a new hash and invalidates the one you were holding. Re-read with `cat`, `schema_read`, or `keg_settings` before each guarded write; a hash never covers two writes, so an edit followed by a delete needs two reads.\n" +
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
	flight, flightNote := t.resolveOrientFlight(ctx, flightName)
	available, warnings := t.OrientationKegsForFlight(ctx, flight)
	var authority *OrientationAuthority
	if strings.TrimSpace(flightName) == "" {
		available, warnings = t.IdentityKegCatalog(ctx)
		flightNote = "No flight is configured, so normal identity-authorized full access applies. Pin a least-privilege flight outside MCP and start a new connection to narrow it."
		authority = &OrientationAuthority{FullAccess: true}
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
func (t *Tap) OrientationKegsForFlight(ctx context.Context, flight *Flight) ([]OrientationKeg, []string) {
	rows, warnings := t.IdentityKegCatalog(ctx)
	if flight == nil {
		return nil, warnings
	}
	return ProjectOrientationKegs(flight, rows), warnings
}

// IdentityKegCatalog discovers the identity-authorized KEGs from every
// configured hub without applying flight authority. Each hub is queried at
// most once. Callers must explicitly project these rows through a selected
// flight or use them only for identity search.
func (t *Tap) IdentityKegCatalog(ctx context.Context) ([]OrientationKeg, []string) {
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

func (t *Tap) resolveOrientFlight(ctx context.Context, name string) (*Flight, string) {
	name = strings.TrimSpace(name)
	if name == "" || t == nil || t.FlightService == nil {
		return nil, ""
	}
	flight, err := t.FlightService.GetFlightFresh(ctx, name)
	if err != nil {
		return &Flight{Name: name}, fmt.Sprintf("Flight %q is unavailable: %v", name, err)
	}
	return flight, ""
}

// OrientationKeg is one effective KEG exposed by an orientation context.
type OrientationKeg struct {
	Ref        string
	Namespace  string
	Alias      string
	Title      string
	Summary    string
	Role       string
	Source     string
	Visibility string
	FlightCap  string
	Flights    []string
}

// OrientationAuthority describes the connection-pinned launch root and the flight
// selected from its live transitive graph for this call.
type OrientationAuthority struct {
	Root             *Flight
	Active           *Flight
	Path             []string
	AvailableFlights []string
	Revision         string
	FullAccess       bool
}

func (t *Tap) identityKegCatalog(ctx context.Context) ([]OrientationKeg, []string) {
	if t == nil || t.ConfigService == nil {
		return nil, []string{"KEG listing unavailable: no config service is configured."}
	}
	cfg, loadWarnings, err := t.ConfigService.Load()
	if err != nil {
		return nil, []string{fmt.Sprintf("KEG listing unavailable: %v", err)}
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
	return identity, warnings
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
			Ref:        "@" + k.Namespace + "/" + k.Alias,
			Namespace:  k.Namespace,
			Alias:      k.Alias,
			Title:      k.Title,
			Summary:    k.Summary,
			Role:       k.Role,
			Source:     hubName,
			Visibility: k.Visibility,
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
// It remains part of the orient payload as well. That duplication is
// deliberate: initialization instructions are captured once and are discarded
// by a context reset, so orient has to stay self-contained or a compacted agent
// has no route back to these rules.
func OrientationOperatingRules() string {
	return "# KEG System\n\n" + orientPurpose + "\n\n" + orientRulesSummary
}

// BuildOrientationPayload renders the provider-neutral orientation document
// from one flight snapshot and its effective KEG listing. agent names
// the `tap launch` agent driving the session, or "" when a human is; it is
// reported because it explains where the flight came from and how to change it.
func BuildOrientationPayload(flight *Flight, flightNote, agent string, kegs []OrientationKeg, warnings []string, authority *OrientationAuthority) (string, error) {
	var b strings.Builder
	b.WriteString(OrientationOperatingRules())
	b.WriteString("\n")

	// A graph-wide listing mixes KEGs the active flight covers itself with KEGs
	// only a descendant covers. They are operationally different — the second
	// group needs a flight selection first — so they are rendered apart rather
	// than distinguished only by a column an agent can skim past.
	usable, viaSubflight := partitionOrientationKegs(flight, kegs)

	b.WriteString("## Available KEGs\n\n")
	if len(warnings) > 0 {
		for _, warning := range warnings {
			b.WriteString("- Warning: ")
			b.WriteString(warning)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(usable) == 0 {
		if len(viaSubflight) > 0 {
			// The dispatcher shape: a parent flight that carries instructions and
			// delegates every KEG to a descendant. Saying "no KEGs available"
			// here would be wrong and would stop an agent that should be reading
			// the next section instead.
			b.WriteString("(The active flight covers no KEGs directly. See \"Reachable via subflight\" below.)\n\n")
		} else {
			b.WriteString("(No KEGs are currently available from configured hubs")
			if flight != nil && len(flight.Cover) > 0 {
				b.WriteString(" after applying the active flight cover")
			}
			b.WriteString(".)\n\n")
		}
	} else {
		writeOrientationKegTable(&b, usable, "Flights")
		b.WriteString("\nCall `keg_settings` for the selected KEG or KEGs before operating in them; targeted settings include KEG-level instructions.\n\n")
	}

	if len(viaSubflight) > 0 {
		b.WriteString("## Reachable via subflight\n\n")
		b.WriteString("The active flight does not cover these KEGs, so the KEG tools cannot reach them yet. ")
		b.WriteString("Pass the named flight as the `flight` argument on a tool call to operate in one. ")
		b.WriteString("That selection applies to a single call and never changes this session's pinned root.\n\n")
		writeOrientationKegTable(&b, viaSubflight, "Select flight")
		b.WriteString("\n")
	}

	if flight != nil {
		b.WriteString("## Flight\n\n")
		if flight.Name != "" {
			b.WriteString("Active flight: `")
			b.WriteString(flight.Name)
			b.WriteString("`\n\n")
		}
		if agent != "" {
			b.WriteString("This session is driven by agent `")
			b.WriteString(agent)
			b.WriteString("`. The agent selects only the model and telemetry identity; it cannot ")
			b.WriteString("select or replace the connection-pinned root in `TAP_FLIGHT`. Call ")
			b.WriteString("`orient` without a flight to use that root, or name an authorized ")
			b.WriteString("descendant to work under only that flight's instructions and authority.\n\n")
		}
		if flightNote != "" {
			b.WriteString(flightNote)
			b.WriteString("\n\n")
		}
		if flight.Title != "" {
			b.WriteString(flight.Title)
			b.WriteString("\n\n")
		}
		if flight.Instructions != "" {
			b.WriteString(strings.TrimSpace(flight.Instructions))
			b.WriteString("\n\n")
		} else if flightNote == "" {
			b.WriteString("(No flight-level instructions.)\n\n")
		}
	}
	if flight == nil {
		b.WriteString("## Flight\n\n")
		if authority != nil && authority.FullAccess {
			b.WriteString("No flight was provided. Normal identity-authorized full access applies, so every KEG is available only at the caller's real role; Hub ACLs and namespace membership are never raised or bypassed.\n\n")
			if flightNote != "" {
				b.WriteString(strings.TrimSpace(flightNote))
				b.WriteString("\n\n")
			}
		} else {
			b.WriteString("The explicitly selected flight could not be activated, so this session is in fail-closed recovery and KEG tools are locked. Only `orient`, `session_refresh`, `list_flights`, `flight_show`, `auth_info`, and `keg_search` are available.\n\n")
			if flightNote != "" {
				b.WriteString(strings.TrimSpace(flightNote))
				b.WriteString("\n\n")
			}
			b.WriteString("Repair that exact selection outside MCP, then call `session_refresh` and `orient`. This state never falls back to no-flight full access.\n\n")
		}
	}

	if authority != nil && (authority.Root != nil || authority.FullAccess) {
		active := authority.Active
		if active == nil {
			active = flight
		}
		b.WriteString("## Orientation authority\n\n")
		if authority.Root != nil {
			b.WriteString("Launch root: `" + authority.Root.Name + "`\n\n")
		} else {
			b.WriteString("Launch root: (none; identity-authorized full access)\n\n")
		}
		if active != nil {
			b.WriteString("Selected flight: `" + active.Name + "`\n\n")
		}
		path := authority.Path
		if len(path) == 0 && authority.Root != nil {
			path = []string{authority.Root.Name}
		}
		if len(path) > 0 {
			b.WriteString("Resolved path: `" + strings.Join(path, "` → `") + "`\n\n")
		}
		b.WriteString("Selectable flights:")
		if len(authority.AvailableFlights) == 0 {
			b.WriteString(" (none)\n\n")
		} else {
			b.WriteString("\n\n")
			for _, ref := range authority.AvailableFlights {
				b.WriteString("- `" + ref + "`\n")
			}
			b.WriteString("\n")
		}
		if authority.Revision != "" {
			b.WriteString("Authority revision: `" + authority.Revision + "`\n\n")
		}
		if authority.FullAccess {
			b.WriteString("The absence of a flight is pinned to this MCP connection. Bare calls use normal identity-authorized full access and send no governed-flight state. An explicit `flight` selects exactly one identity-accessible real flight for that call and uses only its cover, capabilities, and instructions. Concurrent callers may select different real flights without changing shared session state. Pin a least-privilege flight outside MCP, then start a new connection to narrow access.\n\n")
		} else {
			b.WriteString("The root reference is pinned to this MCP connection. Every authority-bearing call reloads its live transitive graph. Default `orient` and `keg_list` discovery summarize the root plus accessible descendants; explicitly supplying `flight` discovers exactly that flight. Operational tools still use only the root when `flight` is omitted, while an explicit root or listed descendant uses only that flight's instructions and authority. Descendant cover, capabilities, and instructions are never inherited. Concurrent callers may select different descendants without changing shared session state. Use `keg_search` to find identity-accessible KEGs outside this graph; results grant no operational access.\n\n")
		}
	}

	b.WriteString("## Guidance\n\n")
	for _, name := range []string{"snapshot-policy.md", "secret-handling.md", "agent-orient.md", "tool-inventory.md", "linking.md", "troubleshooting.md"} {
		if err := appendCanonical(&b, name); err != nil {
			return "", err
		}
		b.WriteString("\n")
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

func writeOrientationKegTable(b *strings.Builder, kegs []OrientationKeg, flightsHeading string) {
	fmt.Fprintf(b, "| KEG | Title | Summary | Role | %s | Source |\n", flightsHeading)
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, k := range kegs {
		role := EffectiveOrientationRole(k)
		flights := "none"
		if len(k.Flights) > 0 {
			flights = strings.Join(k.Flights, ", ")
		}
		source := k.Source
		if k.Visibility != "" {
			source += "/" + k.Visibility
		}
		fmt.Fprintf(
			b,
			"| `%s` | %s | %s | %s | %s | %s |\n",
			k.Ref,
			orientationTableCell(k.Title),
			orientationTableCell(k.Summary),
			role,
			orientationTableCell(flights),
			source,
		)
	}
}

func orientationTableCell(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\\", "\\\\")
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
