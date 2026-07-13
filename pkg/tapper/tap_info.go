package tapper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	"gopkg.in/yaml.v3"
)

// InfoOptions configures behavior for Tap.Info.
type InfoOptions struct {
	KegTargetOptions

	// Minimal strips large sections (tags, entities, indexes) from the output,
	// returning only core config fields. Useful for MCP tools where response
	// size must stay small.
	Minimal bool
}

// Info displays the keg metadata (keg.yaml file contents).
func (t *Tap) Info(ctx context.Context, opts InfoOptions) (string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}

	if opts.Minimal {
		return t.infoMinimal(ctx, k)
	}

	// For file-backed kegs, return the raw config contents so unknown sections
	// (for example custom fields and entities) are preserved.
	if k.Target() != nil && k.Target().Scheme() == keg.SchemeFile {
		raw, rawErr := readRawKegConfig(t.Runtime, k.Target().Path())
		if rawErr == nil {
			return string(raw), nil
		}
		if !os.IsNotExist(rawErr) {
			return "", fmt.Errorf("unable to read raw keg config: %w", rawErr)
		}
	}

	cfg, err := k.Config(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to read keg config: %w", err)
	}

	// Convert config to YAML format
	return cfg.String(), nil
}

// infoMinimal returns a compact version of the keg config with only core fields.
func (t *Tap) infoMinimal(ctx context.Context, k keg.Keg) (string, error) {
	cfg, err := k.Config(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to read keg config: %w", err)
	}

	type minimalConfig struct {
		Kegv    string `yaml:"kegv,omitempty"`
		Title   string `yaml:"title,omitempty"`
		Summary string `yaml:"summary,omitempty"`
		Updated string `yaml:"updated,omitempty"`
	}

	out := minimalConfig{
		Kegv:    cfg.Kegv,
		Title:   cfg.Title,
		Summary: cfg.Summary,
		Updated: cfg.Updated,
	}

	b, err := yaml.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("unable to marshal minimal config: %w", err)
	}
	return string(b), nil
}

// KegInfoOptions configures behavior for Tap.KegInfo.
type KegInfoOptions struct {
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
	cfg, err := t.ConfigService.Config(true)
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
			if ref.Path != "" {
				id.Ref = ref.Path
			} else {
				id.Keg = ref.Name
				// Infer namespace + hub through the shared chain so a bare name
				// (e.g. "private") displays as "@local/private" on a local hub,
				// matching what the backend actually resolves. Best-effort: if it
				// cannot resolve (e.g. a remote hub with no namespace), fall back
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
	if projectCfg, _ := t.ConfigService.ProjectConfig(true); configFieldGetter(projectCfg, field) != "" {
		return "project"
	}
	if userCfg, _ := t.ConfigService.UserConfig(true); configFieldGetter(userCfg, field) != "" {
		return "user"
	}
	return ""
}

// KegInfo displays diagnostics for a resolved keg.
func (t *Tap) KegInfo(ctx context.Context, opts KegInfoOptions) (string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}
	if _, err := k.Config(ctx); err != nil {
		return "", fmt.Errorf("unable to read keg config: %w", err)
	}

	summary, err := k.Summary(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to list nodes: %w", err)
	}

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
		Ref:       canonicalKegRef(identity.Ref),
		Flight:    identity.Flight,
		NodeCount: summary.NodeCount,
		Files:     capability{Supported: summary.Files.Supported},
		Images:    capability{Supported: summary.Images.Supported},
	}

	// Populate summary from the keg config.
	cfg, cfgErr := k.Config(ctx)
	if cfgErr == nil && cfg.Summary != "" {
		out.Summary = cfg.Summary
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
			if k.Target().Scheme() == keg.SchemeFile {
				path := toolkit.ExpandEnv(t.Runtime, k.Target().Path())
				if expanded, expandErr := toolkit.ExpandPath(t.Runtime, path); expandErr == nil {
					path = expanded
				}
				debug.KegDirectory = filepath.Clean(path)
			} else {
				debug.KegDirectory = k.Target().Path()
			}
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

func readRawKegConfig(rt *toolkit.Runtime, root string) ([]byte, error) {
	_, raw, err := readRawKegConfigWithPath(rt, root)
	return raw, err
}

func readRawKegConfigWithPath(rt *toolkit.Runtime, root string) (string, []byte, error) {
	base := toolkit.ExpandEnv(rt, root)
	if expanded, err := toolkit.ExpandPath(rt, base); err == nil {
		base = expanded
	}

	var firstErr error
	for _, name := range []string{"keg", "keg.yaml", "keg.yml"} {
		path := filepath.Join(base, name)
		if resolved, err := rt.ResolvePath(path, true); err == nil {
			path = resolved
		}

		data, err := rt.ReadFile(path)
		if err == nil {
			return path, data, nil
		}
		if os.IsNotExist(err) {
			continue
		}
		if firstErr == nil {
			firstErr = err
		}
	}

	if firstErr != nil {
		return "", nil, firstErr
	}
	return "", nil, os.ErrNotExist
}

// KegConfigEditOptions configures behavior for Tap.KegConfigEdit.
type KegConfigEditOptions struct {
	KegTargetOptions
	Stream *toolkit.Stream
}

// KegConfigEdit opens the keg configuration file in the default editor.
func (t *Tap) KegConfigEdit(ctx context.Context, opts KegConfigEditOptions) error {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return err
	}

	var (
		configPath  string
		originalRaw []byte
	)
	if k.Target() != nil && k.Target().Scheme() == keg.SchemeFile {
		path, raw, readErr := readRawKegConfigWithPath(t.Runtime, k.Target().Path())
		if readErr != nil {
			return fmt.Errorf("unable to read keg config: %w", readErr)
		}
		configPath = path
		originalRaw = raw
	} else {
		cfg, cfgErr := k.Config(ctx)
		if cfgErr != nil {
			return fmt.Errorf("unable to read keg config: %w", cfgErr)
		}
		originalRaw = []byte(cfg.String())
	}

	saveConfig := func(data []byte) error {
		if configPath != "" {
			resolvedPath, err := t.Runtime.ResolvePath(configPath, true)
			if err != nil {
				return fmt.Errorf("unable to resolve keg config path: %w", err)
			}
			if err := t.Runtime.AtomicWriteFile(resolvedPath, data, 0o644); err != nil {
				return fmt.Errorf("unable to save edited keg config: %w", err)
			}
			return nil
		}
		if err := k.SetConfig(ctx, data); err != nil {
			return fmt.Errorf("unable to save edited keg config: %w", err)
		}
		return nil
	}

	initialRaw := originalRaw
	if opts.Stream != nil && opts.Stream.IsPiped {
		pipedRaw, readErr := io.ReadAll(opts.Stream.In)
		if readErr != nil {
			return fmt.Errorf("unable to read piped input: %w", readErr)
		}
		if len(bytes.TrimSpace(pipedRaw)) > 0 {
			if bytes.Equal(pipedRaw, originalRaw) {
				return nil
			}
			if _, parseErr := keg.ParseKegConfig(pipedRaw); parseErr != nil {
				return fmt.Errorf("keg config from stdin is invalid: %w", parseErr)
			}
			return saveConfig(pipedRaw)
		}
	}

	tempPath, err := newEditorTempFilePath(t.Runtime, "tap-info-", ".yaml")
	if err != nil {
		return fmt.Errorf("unable to create temp file path: %w", err)
	}
	if err := t.Runtime.WriteFile(tempPath, initialRaw, 0o600); err != nil {
		return fmt.Errorf("unable to write temp config file: %w", err)
	}
	defer func() {
		_ = t.Runtime.Remove(tempPath, false)
	}()

	if err := editWithLiveSaves(ctx, t.Runtime, tempPath, nil, func(editedRaw []byte) error {
		if _, err := keg.ParseKegConfig(editedRaw); err != nil {
			return fmt.Errorf("keg config is invalid after editing: %w", err)
		}
		return saveConfig(editedRaw)
	}); err != nil {
		return fmt.Errorf("unable to edit keg config: %w", err)
	}
	return nil
}
