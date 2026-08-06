package tapper

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jlrickert/tapper/pkg/integrations"
	"github.com/jlrickert/tapper/pkg/keg"
)

const orientPurpose = "Tapper provides an MCP interface for KEG (Knowledge Exchange Graph) systems. A KEG is a numbered collection of markdown nodes with metadata, links, tags, and snapshot history. Agents operate on a KEG through the `mcp__tapper__*` tools; reading or writing node files directly bypasses indexing, locking, and snapshots."

const orientRulesSummary = "Rules:\n" +
	"- Call `orient` first in every session, before any other tool and before replying. The active flight carries this session's instructions, so until you orient you do not know what the session is for.\n" +
	"- Call `orient` again after any context reset such as a clear or a compact. These instructions were delivered into the conversation and are discarded with it; the connection survives, so nothing re-sends them on its own. If you cannot tell whether you have oriented in the current context, you have not.\n" +
	"- This payload supersedes every earlier copy of itself. An older copy can still be present — the connection's startup instructions are captured once and never refreshed, and a compaction summary may paraphrase a previous orientation — so if anything you remember about the flight, its cover, or its instructions disagrees with what you are reading here, this is current and that is stale. Do not merge them; replace.\n" +
	"- Use the `mcp__tapper__*` tools for every KEG operation; never read or write node files directly.\n" +
	"- The target keg resolves from the working directory unless the `keg` parameter overrides it.\n" +
	"- Take a snapshot before non-trivial edits. Snapshots do not protect against `remove`; preserve content some other way before deletion.\n" +
	"- Node 0 is the keg's placeholder landing node. Leave it alone: it carries no `type` on purpose, it is where links to unwritten content land, and removing it makes the keg read as uninitialized. Write your content in a new node instead.\n" +
	"- Intra-keg links use `[title](../NODEID)`; cross-keg links use `keg:ALIAS/NODEID` through active configuration or fully qualified `keg:@NAMESPACE/ALIAS/NODEID`.\n" +
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
	flightName := t.ActiveFlightName(opts.Flight)
	flight, flightNote := t.resolveOrientFlight(ctx, flightName)
	available, warnings := t.orientKegListing(ctx, flight)
	return BuildOrientationPayload(flight, flightNote, t.ActiveAgentName(), available, warnings)
}

// OrientationForFlight builds orientation from an already-resolved immutable
// flight snapshot. It is the MCP session path: callers resolve selection and
// refresh policy first, then atomically publish the returned payload.
func (t *Tap) OrientationForFlight(ctx context.Context, flight *Flight) (string, []OrientationKeg, []string, error) {
	available, warnings := t.orientKegListing(ctx, flight)
	payload, err := BuildOrientationPayload(flight, "", t.ActiveAgentName(), available, warnings)
	return payload, available, warnings, err
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
}

func (t *Tap) orientKegListing(ctx context.Context, flight *Flight) ([]OrientationKeg, []string) {
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
	seen := map[string]struct{}{}
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
			if capRole, ok := flightCapForKeg(flight, row.Namespace, row.Alias); !ok {
				continue
			} else {
				row.FlightCap = capRole
			}
			if _, dup := seen[row.Ref]; dup {
				continue
			}
			seen[row.Ref] = struct{}{}
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out, warnings
}

func (t *Tap) orientKegsForHub(ctx context.Context, cfg *Config, hubName string, entry HubEntry) ([]OrientationKeg, error) {
	kind := strings.TrimSpace(entry.Kind)
	if kind == "" {
		kind = HubKindRemote
	}
	if kind == HubKindLocal {
		base, err := t.localHubBase(entry)
		if err != nil {
			return nil, err
		}
		refs := t.scanLocalHubKegs(base)
		out := make([]OrientationKeg, 0, len(refs))
		for _, ref := range refs {
			ns, alias, ok := splitKegRef(ref)
			if !ok {
				continue
			}
			out = append(out, OrientationKeg{
				Ref:        ref,
				Namespace:  ns,
				Alias:      alias,
				Role:       string(FlightRoleEditor),
				Source:     hubName,
				Visibility: "local",
			})
			title, summary, _ := t.localKegDiscovery(filepath.Join(base, "@"+ns, alias))
			out[len(out)-1].Title = title
			out[len(out)-1].Summary = summary
		}
		return out, nil
	}

	url := strings.TrimSpace(entry.URL)
	if url == "" {
		return nil, fmt.Errorf("hub has no url configured")
	}
	token := t.hubToken(entry)
	if token == "" {
		return nil, fmt.Errorf("hub has no authenticated session for %s", url)
	}
	discovered, err := DiscoverOrientationKegs(ctx, url, token)
	if err == nil {
		out := make([]OrientationKeg, 0, len(discovered))
		for _, k := range discovered {
			out = append(out, OrientationKeg{
				Ref:        "@" + k.Namespace + "/" + k.Alias,
				Namespace:  k.Namespace,
				Alias:      k.Alias,
				Title:      k.Title,
				Summary:    k.Summary,
				Role:       k.Role,
				Source:     hubName,
				Visibility: k.Visibility,
			})
		}
		return out, nil
	}
	if !errors.Is(err, ErrOrientationUnsupported) {
		return nil, err
	}

	// Compatibility path for older Hubs: retain their catalog listing and
	// read each selected config for title/summary only. Instructions remain
	// suppressed from aggregate orientation.
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
			Role:       k.Role,
			Source:     hubName,
			Visibility: k.Visibility,
		}
		if title, summary, configErr := t.kegDiscovery(ctx, cfg, hubName, k.Namespace, k.Alias); configErr == nil {
			row.Title = title
			row.Summary = summary
		}
		out = append(out, row)
	}
	return out, nil
}

func (t *Tap) kegDiscovery(ctx context.Context, cfg *Config, hubName, namespace, alias string) (string, string, error) {
	if cfg == nil || alias == "" {
		return "", "", nil
	}
	if entry, ok := cfg.Hub(hubName); ok && hubKindOrDefault(entry.Kind) == HubKindLocal {
		base, err := t.localHubBase(entry)
		if err != nil {
			return "", "", err
		}
		return t.localKegDiscovery(filepath.Join(base, "@"+strings.TrimPrefix(namespace, "@"), alias))
	}
	target, err := cfg.ResolveRef(t.Runtime, KegRef{Hub: hubName, Namespace: namespace, Name: alias})
	if err != nil {
		return "", "", err
	}
	var resolver keg.TokenResolver
	if t.KegService != nil {
		resolver = t.KegService.tokenResolver()
	}
	k, err := keg.NewKegFromTarget(ctx, *target, t.Runtime, keg.WithTokenResolver(resolver))
	if err != nil {
		return "", "", err
	}
	cfgDoc, err := k.Config(ctx)
	if err != nil || cfgDoc == nil {
		return "", "", err
	}
	return cfgDoc.Title, cfgDoc.Summary, nil
}

func (t *Tap) localKegDiscovery(dir string) (string, string, error) {
	for _, name := range []string{"keg", "keg.yaml", "keg.yml"} {
		raw, err := t.Runtime.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		cfgDoc, err := keg.ParseKegConfig(raw)
		if err != nil {
			return "", "", err
		}
		return cfgDoc.Title, cfgDoc.Summary, nil
	}
	return "", "", nil
}

func flightCapForKeg(flight *Flight, namespace, alias string) (string, bool) {
	if flight == nil {
		return "", true
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

// BuildOrientationPayload renders the provider-neutral orientation document
// from one immutable flight snapshot and its effective KEG listing. agent names
// the `tap launch` agent driving the session, or "" when a human is; it is
// reported because it explains where the flight came from and how to change it.
func BuildOrientationPayload(flight *Flight, flightNote, agent string, kegs []OrientationKeg, warnings []string) (string, error) {
	var b strings.Builder
	b.WriteString("# KEG System\n\n")
	b.WriteString(orientPurpose)
	b.WriteString("\n\n")
	b.WriteString(orientRulesSummary)
	b.WriteString("\n")

	b.WriteString("## Available KEGs\n\n")
	if len(warnings) > 0 {
		for _, warning := range warnings {
			b.WriteString("- Warning: ")
			b.WriteString(warning)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(kegs) == 0 {
		b.WriteString("(No KEGs are currently available from configured hubs")
		if flight != nil && len(flight.Cover) > 0 {
			b.WriteString(" after applying the active flight cover")
		}
		b.WriteString(".)\n\n")
	} else {
		b.WriteString("| KEG | Title | Summary | Role | Source | Flight cap |\n")
		b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
		for _, k := range kegs {
			role := k.Role
			if role == "" {
				role = "viewer"
			}
			capRole := k.FlightCap
			if capRole == "" {
				capRole = "none"
			}
			source := k.Source
			if k.Visibility != "" {
				source += "/" + k.Visibility
			}
			fmt.Fprintf(
				&b,
				"| `%s` | %s | %s | %s | %s | %s |\n",
				k.Ref,
				orientationTableCell(k.Title),
				orientationTableCell(k.Summary),
				role,
				source,
				capRole,
			)
		}
		b.WriteString("\nCall `keg_settings` for the selected KEG or KEGs before operating in them; targeted settings include KEG-level instructions.\n\n")
	}

	if flight == nil {
		// Recovery. Say so in the payload itself: the tool list is filtered to
		// the recovery set, so an agent never gets to call a locked tool and
		// see the error explaining why. Without this the only signal is an
		// absence — an empty KEG table — which weaker models do not act on.
		b.WriteString("## Flight\n\n")
		b.WriteString("No flight is selected, so this session is in recovery mode ")
		b.WriteString("and the KEG tools are locked. Only `orient`, `list_flights`, ")
		b.WriteString("`flight_show`, and `auth_info` are available.\n\n")
		b.WriteString("To recover:\n\n")
		b.WriteString("1. Call `list_flights` to see what is available.\n")
		b.WriteString("2. Ask the user to select a flight in Tapper configuration. ")
		b.WriteString("Flights are selected outside MCP; an agent cannot select one itself.\n")
		b.WriteString("3. Call `orient` again on this same connection to pick it up.\n\n")
		if agent != "" {
			b.WriteString("This session was launched as agent `")
			b.WriteString(agent)
			b.WriteString("`, so giving that agent a `flight` in Tapper configuration ")
			b.WriteString("is the most direct fix.\n\n")
		}
	}

	if flight != nil {
		b.WriteString("## Flight\n\n")
		switch {
		case flight.Bootstrap:
			// Never say "active flight" for the synthetic one: a reader who
			// believes a flight was selected will not go set one up, which is
			// the entire point of this mode.
			b.WriteString("No flight is configured, so this session is running on a ")
			b.WriteString("temporary bootstrap flight. Its cover is empty, so every KEG ")
			b.WriteString("tool stays locked; what it grants is the authority to create ")
			b.WriteString("the first flight and the first KEG. Setting this up is the ")
			b.WriteString("session's work — do it before anything else.\n\n")
		case flight.Name != "":
			b.WriteString("Active flight: `")
			b.WriteString(flight.Name)
			b.WriteString("`\n\n")
		}
		if agent != "" {
			// Naming the agent tells the reader where the flight came from and
			// how to move it. Without this the flight looks like a fixed
			// property of the session, and the user is told to edit `flight:`
			// in config — which the agent's own flight silently outranks.
			b.WriteString("This session is driven by agent `")
			b.WriteString(agent)
			b.WriteString("`. Unless `TAP_FLIGHT` or `--flight` overrides it, the flight above ")
			b.WriteString("comes from that agent's `flight` in Tapper configuration: change it ")
			b.WriteString("there and call `orient` again to move this session.\n\n")
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

	b.WriteString("## Guidance\n\n")
	for _, name := range []string{"linking.md", "snapshot-policy.md", "secret-handling.md", "agent-orient.md", "tool-inventory.md", "troubleshooting.md"} {
		if err := appendCanonical(&b, name); err != nil {
			return "", err
		}
		b.WriteString("\n")
	}

	return b.String(), nil
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
