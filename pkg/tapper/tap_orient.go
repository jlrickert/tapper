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
	"- Use the `mcp__tapper__*` tools for every KEG operation; never read or write node files directly.\n" +
	"- The target keg resolves from the working directory unless the `keg` parameter overrides it.\n" +
	"- Take a snapshot before non-trivial edits. Snapshots do not protect against `remove`; preserve content some other way before deletion.\n" +
	"- Intra-keg links use `[title](../NODEID)`; cross-keg links use `keg:ALIAS/NODEID` through active configuration or fully qualified `keg:@NAMESPACE/ALIAS/NODEID`.\n"

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
	flightName := t.activeFlightName(opts.Flight)
	flight, flightNote := t.resolveOrientFlight(ctx, flightName)
	available, warnings := t.orientKegListing(ctx, flight)
	return BuildOrientationPayload(flight, flightNote, available, warnings)
}

// OrientationForFlight builds orientation from an already-resolved immutable
// flight snapshot. It is the MCP session path: callers resolve selection and
// refresh policy first, then atomically publish the returned payload.
func (t *Tap) OrientationForFlight(ctx context.Context, flight *Flight) (string, []OrientationKeg, []string, error) {
	available, warnings := t.orientKegListing(ctx, flight)
	payload, err := BuildOrientationPayload(flight, "", available, warnings)
	return payload, available, warnings, err
}

func (t *Tap) activeFlightName(explicit string) string {
	if name := strings.TrimSpace(explicit); name != "" {
		return name
	}
	if t == nil || t.ConfigService == nil {
		return ""
	}
	cfg, err := t.ConfigService.Config(true)
	if err != nil || cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Flight())
}

// ActiveFlightName resolves an explicit flight or the persistent project
// default without mutating configuration.
func (t *Tap) ActiveFlightName(explicit string) string {
	return t.activeFlightName(explicit)
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
	cfg, err := t.ConfigService.Config(true)
	if err != nil {
		return nil, []string{fmt.Sprintf("KEG listing unavailable: %v", err)}
	}

	var warnings []string
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
// from one immutable flight snapshot and its effective KEG listing.
func BuildOrientationPayload(flight *Flight, flightNote string, kegs []OrientationKeg, warnings []string) (string, error) {
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

	if flight != nil {
		b.WriteString("## Flight\n\n")
		if flight.Name != "" {
			b.WriteString("Active flight: `")
			b.WriteString(flight.Name)
			b.WriteString("`\n\n")
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
