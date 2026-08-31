package tapper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/schemas"
	"gopkg.in/yaml.v3"
)

// KegSettingsOptions configures behavior for Tap.KegSettings.
type KegSettingsOptions struct {
	KegTargetOptions

	// Kegs requests one or more canonical @namespace/keg references. It is
	// mutually exclusive with Keg and is available only in minimal mode.
	Kegs []string

	// Minimal strips large sections (tags, entities, indexes) from the output,
	// returning only core config fields. Useful for MCP tools where response
	// size must stay small.
	Minimal bool
}

// KegSettings displays the keg metadata (keg file contents).
func (t *Tap) KegSettings(ctx context.Context, opts KegSettingsOptions) (string, error) {
	if opts.Keg != "" && opts.Kegs != nil {
		return "", fmt.Errorf("keg and kegs are mutually exclusive")
	}
	if opts.Kegs != nil {
		if len(opts.Kegs) == 0 || len(opts.Kegs) > 100 {
			return "", fmt.Errorf("kegs must contain 1 to 100 canonical references")
		}
		if !opts.Minimal {
			if len(opts.Kegs) != 1 {
				return "", fmt.Errorf("minimal=false requires exactly one keg")
			}
			opts.Keg = opts.Kegs[0]
			opts.Kegs = nil
		} else {
			return t.kegSettingsBatch(ctx, opts)
		}
	}

	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}

	if opts.Minimal {
		return t.kegSettingsMinimal(ctx, k)
	}

	cfg, err := k.Settings(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to read keg settings: %w", err)
	}

	// Settings transports retain the original document so extensions and
	// unknown fields survive a remote read/edit/write cycle.
	return string(cfg.Raw()), nil
}

// kegSettingsMinimal returns a compact keg settings with only core fields.
func (t *Tap) kegSettingsMinimal(ctx context.Context, k keg.Keg) (string, error) {
	cfg, err := k.Settings(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to read keg settings: %w", err)
	}

	type minimalConfig struct {
		Kegv         string `yaml:"kegv,omitempty"`
		Title        string `yaml:"title,omitempty"`
		Summary      string `yaml:"summary,omitempty"`
		Updated      string `yaml:"updated,omitempty"`
		Instructions string `yaml:"instructions,omitempty"`
	}

	out := minimalConfig{
		Kegv:         cfg.Kegv,
		Title:        cfg.Title,
		Summary:      cfg.Summary,
		Updated:      cfg.Updated,
		Instructions: cfg.Instructions,
	}

	b, err := yaml.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("unable to marshal minimal config: %w", err)
	}
	return string(b), nil
}

type minimalKegSettings struct {
	Keg          string `yaml:"keg"`
	Title        string `yaml:"title,omitempty"`
	Summary      string `yaml:"summary,omitempty"`
	Updated      string `yaml:"updated,omitempty"`
	Instructions string `yaml:"instructions,omitempty"`
}

func (t *Tap) kegSettingsBatch(ctx context.Context, opts KegSettingsOptions) (string, error) {
	refs := make([]string, 0, len(opts.Kegs))
	seen := make(map[string]struct{}, len(opts.Kegs))
	for _, raw := range opts.Kegs {
		namespace, alias, ok := parseCanonicalKegSelection(raw)
		if !ok {
			return "", fmt.Errorf("invalid canonical keg reference %q", raw)
		}
		ref := "@" + namespace + "/" + alias
		if _, duplicate := seen[ref]; duplicate {
			return "", fmt.Errorf("duplicate keg reference %q", ref)
		}
		seen[ref] = struct{}{}
		if opts.FlightContext != nil {
			if _, covered := flightCapForKeg(opts.FlightContext, namespace, alias); !covered {
				return "", fmt.Errorf("keg %q is not available in flight %q", ref, opts.FlightContext.Name)
			}
		}
		refs = append(refs, ref)
	}

	details := make([]minimalKegSettings, 0, len(refs))
	for _, ref := range refs {
		detail, err := t.readMinimalKegSettings(ctx, opts, ref)
		if err != nil {
			// Do not serialize until every ordinary settings read succeeds, so
			// callers never receive a partial batch.
			return "", err
		}
		details = append(details, detail)
	}
	return marshalMinimalKegSettings(refs, details)
}

func (t *Tap) readMinimalKegSettings(ctx context.Context, opts KegSettingsOptions, ref string) (minimalKegSettings, error) {
	targetOpts := opts.KegTargetOptions
	targetOpts.Keg = ref
	k, err := t.resolveKeg(ctx, targetOpts)
	if err != nil {
		return minimalKegSettings{}, fmt.Errorf("unable to open keg %q: %w", ref, err)
	}
	cfg, err := k.Settings(ctx)
	if err != nil {
		return minimalKegSettings{}, fmt.Errorf("unable to read keg settings %q: %w", ref, err)
	}
	return minimalKegSettings{
		Keg:          ref,
		Title:        cfg.Title,
		Summary:      cfg.Summary,
		Updated:      cfg.Updated,
		Instructions: cfg.Instructions,
	}, nil
}

func marshalMinimalKegSettings(refs []string, details []minimalKegSettings) (string, error) {
	if len(details) != len(refs) {
		return "", fmt.Errorf("settings response length does not match request")
	}
	out := make([]minimalKegSettings, len(refs))
	for i, ref := range refs {
		if details[i].Keg != ref {
			return "", fmt.Errorf("settings response does not preserve request order")
		}
		out[i] = minimalKegSettings{
			Keg:          ref,
			Title:        details[i].Title,
			Summary:      details[i].Summary,
			Updated:      details[i].Updated,
			Instructions: details[i].Instructions,
		}
	}
	b, err := yaml.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("unable to marshal minimal config: %w", err)
	}
	return string(b), nil
}

func parseCanonicalKegSelection(raw string) (namespace, alias string, ok bool) {
	if raw != strings.TrimSpace(raw) || !strings.HasPrefix(raw, "@") {
		return "", "", false
	}
	namespace, alias, ok = splitKegRef(raw)
	if !ok || strings.Contains(alias, "/") {
		return "", "", false
	}
	if ValidateNamespace(namespace) != nil || ValidateKegAlias(alias) != nil {
		return "", "", false
	}
	return namespace, alias, true
}

// InfoOptions configures behavior for Tap.Info.
type InfoOptions struct {
	KegTargetOptions

	// JSON renders the diagnostics as JSON instead of YAML.
	JSON bool

	// Debug includes working-directory and backend resolution diagnostics.
	Debug bool
}

// resolvedIdentity captures the hub/namespace/keg trio and active flight a
// selector resolves to. It is derived from the config resolution chain (not
// the opened backend) so it works identically for local and remote kegs.
type resolvedIdentity struct {
	Hub       string `yaml:"hub,omitempty" json:"hub,omitempty"`
	HubKind   string `yaml:"hub_kind,omitempty" json:"hub_kind,omitempty"`
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Keg       string `yaml:"keg,omitempty" json:"keg,omitempty"`
	Ref       string `yaml:"ref,omitempty" json:"ref,omitempty"`
	Flight    string `yaml:"flight,omitempty" json:"flight,omitempty"`
}

// resolveIdentity derives the resolved hub/namespace/keg identity and active
// flight. Best-effort: a missing config or unresolved namespace leaves the
// corresponding fields blank rather than erroring.
func (t *Tap) resolveIdentity(opts KegTargetOptions) resolvedIdentity {
	var id resolvedIdentity
	cfg, err := t.ConfigService.Config()
	if err != nil || cfg == nil {
		return id
	}

	// Determine the selector and which slot won, mirroring KegService.resolvePath.
	selector := strings.TrimSpace(opts.Keg)
	switch {
	case selector != "":
	default:
		if v := strings.TrimSpace(cfg.DefaultKeg()); v != "" {
			selector = v
		} else if v := strings.TrimSpace(cfg.LookupAlias(t.Runtime, t.Root)); v != "" {
			selector = v
		} else if v := strings.TrimSpace(cfg.FallbackKeg()); v != "" {
			selector = v
		}
	}

	if selector != "" {
		ref, oErr := applyRefOverrides(parseKegRef(selector), opts.Namespace, opts.Hub, selector)
		if oErr == nil {
			if ref.Name != "" {
				id.Keg = ref.Name
				// Infer namespace + hub through the shared chain so a bare name
				// displays as its fully qualified remote reference. Best-effort: if
				// it cannot resolve (for example, a hub with no namespace), fall back
				// to the bare name and leave namespace/hub blank.
				if ns, hub, entry, rErr := cfg.resolveNamespaceHub(ref.Namespace, ref.Hub); rErr == nil {
					id.Namespace = ns
					id.Hub = hub
					id.HubKind = entry.Kind
					if id.HubKind == "" {
						id.HubKind = HubKindRemote
					}
					if ref.Name != "" {
						id.Ref = "@" + ns + "/" + ref.Name
					}
				} else if ref.Name != "" {
					id.Ref = ref.Name
				}
			}
		}
	}

	id.Flight = strings.TrimSpace(opts.Flight)
	if id.Flight == "" {
		id.Flight = strings.TrimSpace(cfg.Flight())
	}
	return id
}

// configFieldScope reports which config tier (env|project|user) set a scalar
// field, or "" when only a default/compiled-in value applies.
func (t *Tap) configFieldScope(field string) string {
	if configFieldGetter(t.loadEnvConfig(), field) != "" {
		return "env"
	}
	if projectCfg, _ := t.ConfigService.ProjectConfig(); configFieldGetter(projectCfg, field) != "" {
		return "project"
	}
	if userCfg, _ := t.ConfigService.UserConfig(); configFieldGetter(userCfg, field) != "" {
		return "user"
	}
	return ""
}

// Info displays diagnostics for a resolved keg.
func (t *Tap) Info(ctx context.Context, opts InfoOptions) (string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}
	info, err := k.Info(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to read keg settings: %w", err)
	}
	summary := info.Summary

	type capability struct {
		Supported bool `yaml:"supported" json:"supported"`
	}
	type debugDiagnostics struct {
		WorkingDirectory string `yaml:"working_directory" json:"working_directory"`
		Hub              string `yaml:"hub,omitempty" json:"hub,omitempty"`
		Backend          string `yaml:"backend,omitempty" json:"backend,omitempty"`
		Namespace        string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
		Keg              string `yaml:"keg,omitempty" json:"keg,omitempty"`
		Target           string `yaml:"target,omitempty" json:"target,omitempty"`
		Scheme           string `yaml:"scheme,omitempty" json:"scheme,omitempty"`
		KegDirectory     string `yaml:"keg_directory,omitempty" json:"keg_directory,omitempty"`
	}
	type diagnostics struct {
		Hub       string            `yaml:"hub" json:"hub"`
		Namespace string            `yaml:"namespace" json:"namespace"`
		Keg       string            `yaml:"keg" json:"keg"`
		Ref       string            `yaml:"ref" json:"ref"`
		Flight    string            `yaml:"flight" json:"flight"`
		Summary   string            `yaml:"summary" json:"summary"`
		NodeCount int               `yaml:"node_count" json:"node_count"`
		Files     capability        `yaml:"files" json:"files"`
		Images    capability        `yaml:"images" json:"images"`
		Debug     *debugDiagnostics `yaml:"debug,omitempty" json:"debug,omitempty"`
	}

	identity := t.resolveIdentity(opts.KegTargetOptions)
	out := diagnostics{
		Hub:       identity.Hub,
		Namespace: identity.Namespace,
		Keg:       identity.Keg,
		Ref:       canonicalKegRef(identity.Ref),
		Flight:    identity.Flight,
		NodeCount: summary.NodeCount,
		Files:     capability{Supported: summary.Files.Supported},
		Images:    capability{Supported: summary.Images.Supported},
	}

	// Populate summary from the keg settings.
	if info.Settings != nil && info.Settings.Summary != "" {
		out.Summary = info.Settings.Summary
	}

	if opts.Debug {
		workingDir, err := t.Runtime.Getwd()
		if err != nil {
			return "", fmt.Errorf("unable to get working directory: %w", err)
		}
		debug := &debugDiagnostics{
			WorkingDirectory: workingDir,
			Hub:              identity.Hub,
			Backend:          identity.HubKind,
			Namespace:        identity.Namespace,
			Keg:              identity.Keg,
		}
		if k.Target() != nil {
			debug.Target = k.Target().String()
			debug.Scheme = k.Target().Scheme()
			if debug.Backend == "" {
				debug.Backend = KegBackendLabel(k.Target())
			}
			if out.Ref == "" {
				out.Ref = canonicalKegRef(kegRefLabel(k.Target()))
			}
			debug.KegDirectory = k.Target().Path()
		}
		out.Debug = debug
	} else if out.Ref == "" && k.Target() != nil {
		out.Ref = canonicalKegRef(kegRefLabel(k.Target()))
	}

	if opts.JSON {
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return "", fmt.Errorf("unable to marshal info output: %w", err)
		}
		return string(b) + "\n", nil
	}

	b, err := yaml.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("unable to marshal info output: %w", err)
	}
	return string(b), nil
}

func canonicalKegRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.HasPrefix(ref, "keg:") {
		return ref
	}
	if strings.HasPrefix(ref, "@") {
		return "keg:" + ref
	}
	return ref
}

// KegSettingsEditOptions configures behavior for Tap.KegSettingsEdit.
type KegSettingsEditOptions struct {
	KegTargetOptions
	Stream       *toolkit.Stream
	ExpectedHash string
}

// KegSettingsHash performs the read half of an explicit CLI read-before-write
// flow. Mutation methods never call it implicitly.
func (t *Tap) KegSettingsHash(ctx context.Context, opts KegTargetOptions) (string, error) {
	k, err := t.resolveKegForRole(ctx, opts, FlightRoleViewer)
	if err != nil {
		return "", err
	}
	cfg, err := k.Settings(ctx)
	if err != nil {
		return "", err
	}
	return cfg.Hash(), nil
}

// KegSettingsEdit opens the keg settings file in the default editor.
//
// Replacing the settings document is keg administration, so it requires admin
// on the keg itself and not merely admin on the flight. Asking for editor
// identity access here previously let a flightless session — which has no flight
// authority to check — perform the write with editor access alone.
func (t *Tap) KegSettingsEdit(ctx context.Context, opts KegSettingsEditOptions) error {
	k, err := t.resolveKegForRoles(ctx, opts.KegTargetOptions, FlightRoleAdmin, FlightRoleAdmin)
	if err != nil {
		return err
	}

	cfg, err := k.Settings(ctx)
	if err != nil {
		return fmt.Errorf("unable to read keg settings: %w", err)
	}
	originalRaw := cfg.Raw()
	expectedHash := opts.ExpectedHash
	if (opts.Stream == nil || !opts.Stream.IsPiped) && expectedHash == "" {
		expectedHash = cfg.Hash()
	}

	// The schema modeline is an editor affordance, not part of the document:
	// it names a path that only resolves on the machine that opened the
	// editor, and keg settings are persisted — on a hub, shared. So strip it
	// on the way in, no matter which surface supplied the bytes (editor,
	// piped stdin, or the keg_settings_edit MCP tool).
	saveConfig := func(data []byte) error {
		data = schemas.StripModeline(data)
		if err := k.SetSettings(ctx, data, keg.SettingsWriteOptions{ExpectedHash: expectedHash}); err != nil {
			return fmt.Errorf("unable to save edited keg settings: %w", err)
		}
		expectedHash = keg.DocumentHash(data)
		return nil
	}

	if opts.Stream != nil && opts.Stream.IsPiped {
		pipedRaw, readErr := io.ReadAll(opts.Stream.In)
		if readErr != nil {
			return fmt.Errorf("unable to read piped input: %w", readErr)
		}
		if len(bytes.TrimSpace(pipedRaw)) > 0 {
			// Compare with the modeline stripped: piping back exactly what an
			// editor was shown is a no-op, not an edit.
			if bytes.Equal(schemas.StripModeline(pipedRaw), originalRaw) {
				return nil
			}
			if _, parseErr := keg.ParseKegSettingsStrict(pipedRaw); parseErr != nil {
				return fmt.Errorf("keg settings from stdin is invalid: %w", parseErr)
			}
			return saveConfig(pipedRaw)
		}
	}

	tempPath, err := newEditorTempFilePath(t.Runtime, "tap-info-", ".yaml")
	if err != nil {
		return fmt.Errorf("unable to create temp file path: %w", err)
	}
	// Add the modeline here and nowhere else: it exists so a language server
	// can drive completion and validation in this buffer, and saveConfig
	// strips it again before anything is persisted. Replace rather than
	// prepend so a config written by an older build gets its stale line
	// pointed at this build's schema.
	initialRaw := schemas.ReplaceModeline(originalRaw, schemas.Modeline(t.Runtime, schemas.KegSettings))
	if err := t.Runtime.WriteFile(tempPath, initialRaw, 0o600); err != nil {
		return fmt.Errorf("unable to write temp config file: %w", err)
	}
	defer func() {
		_ = t.Runtime.Remove(tempPath, false)
	}()

	if err := editWithLiveSaves(ctx, t.Runtime, tempPath, nil, func(editedRaw []byte) error {
		if _, err := keg.ParseKegSettingsStrict(editedRaw); err != nil {
			return fmt.Errorf("keg settings is invalid after editing: %w", err)
		}
		return saveConfig(editedRaw)
	}); err != nil {
		return fmt.Errorf("unable to edit keg settings: %w", err)
	}
	return nil
}
