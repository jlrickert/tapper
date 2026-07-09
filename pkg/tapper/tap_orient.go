package tapper

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jlrickert/tapper/pkg/integrations"
	"github.com/jlrickert/tapper/pkg/keg"
)

const orientPurpose = "Tapper is a CLI and MCP server for KEG (Knowledge Exchange Graph) systems. A KEG is a numbered collection of markdown nodes with metadata, links, tags, and snapshot history. Agents operate on a KEG through the `mcp__tapper__*` tools; reading or writing node files directly bypasses indexing, locking, and snapshots."

const orientRulesSummary = "Rules:\n" +
	"- Use the `mcp__tapper__*` tools for every KEG operation; never read or write node files directly.\n" +
	"- The target keg resolves from the working directory unless the `keg` parameter overrides it.\n" +
	"- Take a snapshot before non-trivial edits. Snapshots do not protect against `remove`; preserve content some other way before deletion.\n" +
	"- Intra-keg links use `[title](../NODEID)`; cross-keg links use `keg:ALIAS/NODEID`.\n"

// OrientOptions is the input to Tap.Orient. Every field is optional: a
// zero-valued call returns the KEG system payload with the target keg resolved
// from KegTargetOptions.
type OrientOptions struct {
	KegTargetOptions
}

// Orient returns one deterministic KEG system payload. It is best-effort:
// active-keg, flight, and hub-listing failures do not suppress the core
// orientation document.
func (t *Tap) Orient(ctx context.Context, opts OrientOptions) (string, error) {
	activeKeg := t.resolveActiveKegLabel(ctx, opts.KegTargetOptions)
	flight, flightNote := t.resolveOrientFlight(ctx, opts.Flight)
	available, warnings := t.orientKegListing(ctx, flight)
	return buildOrientPayload(activeKeg, flight, flightNote, available, warnings)
}

func (t *Tap) resolveOrientFlight(ctx context.Context, name string) (*Flight, string) {
	name = strings.TrimSpace(name)
	if name == "" || t == nil || t.FlightService == nil {
		return nil, ""
	}
	flight, err := t.FlightService.GetFlight(ctx, name)
	if err != nil {
		return &Flight{Name: name}, fmt.Sprintf("Flight %q is unavailable: %v", name, err)
	}
	return flight, ""
}

type orientKegInfo struct {
	Ref          string
	Namespace    string
	Alias        string
	Role         string
	Source       string
	Visibility   string
	FlightCap    string
	Instructions string
}

func (t *Tap) orientKegListing(ctx context.Context, flight *Flight) ([]orientKegInfo, []string) {
	if t == nil || t.ConfigService == nil {
		return nil, []string{"KEG listing unavailable: no config service is configured."}
	}
	cfg, err := t.ConfigService.Config(true)
	if err != nil {
		return nil, []string{fmt.Sprintf("KEG listing unavailable: %v", err)}
	}

	var warnings []string
	seen := map[string]struct{}{}
	var out []orientKegInfo
	for _, hubName := range t.allHubNames(cfg) {
		entry, ok := cfg.Hub(hubName)
		if !ok {
			continue
		}
		rows, err := t.orientKegsForHub(ctx, hubName, entry)
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
			if row.Instructions == "" {
				if instructions, err := t.kegInstructions(ctx, cfg, hubName, row.Namespace, row.Alias); err == nil {
					row.Instructions = instructions
				}
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

func (t *Tap) orientKegsForHub(ctx context.Context, hubName string, entry HubEntry) ([]orientKegInfo, error) {
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
		out := make([]orientKegInfo, 0, len(refs))
		for _, ref := range refs {
			ns, alias, ok := splitKegRef(ref)
			if !ok {
				continue
			}
			out = append(out, orientKegInfo{
				Ref:        ref,
				Namespace:  ns,
				Alias:      alias,
				Role:       string(FlightRoleEditor),
				Source:     hubName,
				Visibility: "local",
			})
		}
		return out, nil
	}

	url := strings.TrimSpace(entry.URL)
	if url == "" {
		return nil, fmt.Errorf("hub has no url configured")
	}
	token := t.hubToken(entry)
	if token == "" {
		return nil, fmt.Errorf("hub has no auth token (run `tap auth login --hub %s`)", url)
	}
	kegs, err := ListUserKegs(ctx, url, token)
	if err != nil {
		return nil, err
	}
	out := make([]orientKegInfo, 0, len(kegs))
	for _, k := range kegs {
		out = append(out, orientKegInfo{
			Ref:        "@" + k.Namespace + "/" + k.Alias,
			Namespace:  k.Namespace,
			Alias:      k.Alias,
			Role:       k.Role,
			Source:     hubName,
			Visibility: k.Visibility,
		})
	}
	return out, nil
}

func (t *Tap) kegInstructions(ctx context.Context, cfg *Config, hubName, namespace, alias string) (string, error) {
	if cfg == nil || alias == "" {
		return "", nil
	}
	if entry, ok := cfg.Hub(hubName); ok && hubKindOrDefault(entry.Kind) == HubKindLocal {
		base, err := t.localHubBase(entry)
		if err != nil {
			return "", err
		}
		return t.localKegInstructions(filepath.Join(base, "@"+strings.TrimPrefix(namespace, "@"), alias))
	}
	target, err := cfg.ResolveRef(t.Runtime, KegRef{Hub: hubName, Namespace: namespace, Name: alias})
	if err != nil {
		return "", err
	}
	var resolver keg.TokenResolver
	if t.KegService != nil {
		resolver = t.KegService.tokenResolver()
	}
	k, err := keg.NewKegFromTarget(ctx, *target, t.Runtime, keg.WithTokenResolver(resolver))
	if err != nil {
		return "", err
	}
	cfgDoc, err := k.Config(ctx)
	if err != nil || cfgDoc == nil {
		return "", err
	}
	return strings.TrimSpace(cfgDoc.Instructions), nil
}

func (t *Tap) localKegInstructions(dir string) (string, error) {
	for _, name := range []string{"keg", "keg.yaml", "keg.yml"} {
		raw, err := t.Runtime.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		cfgDoc, err := keg.ParseKegConfig(raw)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(cfgDoc.Instructions), nil
	}
	return "", nil
}

func flightCapForKeg(flight *Flight, namespace, alias string) (string, bool) {
	if flight == nil || len(flight.Cover) == 0 {
		return "", true
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

// activeKegLabel is the structured outcome of orient's active-keg resolution.
type activeKegLabel struct {
	Alias      string
	Backend    string
	Unresolved bool
}

func (t *Tap) resolveActiveKegLabel(ctx context.Context, opts KegTargetOptions) activeKegLabel {
	if t == nil || t.KegService == nil {
		return activeKegLabel{Unresolved: true}
	}
	k, err := t.resolveKeg(ctx, opts)
	if err != nil || k == nil || k.Target() == nil {
		if alias := strings.TrimSpace(opts.Keg); alias != "" {
			return activeKegLabel{Alias: alias}
		}
		return activeKegLabel{Unresolved: true}
	}

	label := activeKegLabel{Backend: KegBackendLabel(k.Target())}
	if selector := strings.TrimSpace(opts.Keg); selector != "" {
		label.Alias = selector
	} else {
		label.Alias = kegRefLabel(k.Target())
	}
	return label
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

func buildOrientPayload(active activeKegLabel, flight *Flight, flightNote string, kegs []orientKegInfo, warnings []string) (string, error) {
	var b strings.Builder
	b.WriteString("# KEG System\n\n")
	b.WriteString(orientPurpose)
	b.WriteString("\n\n")
	b.WriteString(orientRulesSummary)
	b.WriteString("\n")

	b.WriteString("## Active KEG\n\n")
	b.WriteString("Active KEG: ")
	b.WriteString(formatActiveKegLine(active))
	b.WriteString("\n\n")

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
		b.WriteString("| KEG | Role | Source | Flight cap |\n")
		b.WriteString("| --- | --- | --- | --- |\n")
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
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", k.Ref, role, source, capRole)
		}
		b.WriteString("\n")
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

	b.WriteString("## KEG Instructions\n\n")
	wroteInstructions := false
	for _, k := range kegs {
		if strings.TrimSpace(k.Instructions) == "" {
			continue
		}
		wroteInstructions = true
		b.WriteString("### `")
		b.WriteString(k.Ref)
		b.WriteString("`\n\n")
		b.WriteString(strings.TrimSpace(k.Instructions))
		b.WriteString("\n\n")
	}
	if !wroteInstructions {
		b.WriteString("(No KEG-level instructions found in the available KEG configs.)\n\n")
	}

	b.WriteString("## Guidance\n\n")
	for _, name := range []string{"linking.md", "snapshot-policy.md", "agent-orient.md", "tool-inventory.md", "troubleshooting.md"} {
		if err := appendCanonical(&b, name); err != nil {
			return "", err
		}
		b.WriteString("\n")
	}

	return b.String(), nil
}

func formatActiveKegLine(active activeKegLabel) string {
	if active.Unresolved {
		return "(none configured; run `tap init` to register one)"
	}
	if active.Alias != "" {
		if active.Backend == "" {
			return "`" + active.Alias + "`"
		}
		return "`" + active.Alias + "` (" + active.Backend + ")"
	}
	if active.Backend != "" {
		return "(" + active.Backend + "; no alias)"
	}
	return "(none configured; run `tap init` to register one)"
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
