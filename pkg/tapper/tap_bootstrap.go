package tapper

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// Bootstrap deployment kinds. Each maps onto an existing hub kind/shape — there
// is no new config field, only a guided way to pick one.
const (
	// BootstrapKindCloud targets the compiled-in atlas remote hub.
	BootstrapKindCloud = "cloud"
	// BootstrapKindEnterprise registers a user-supplied remote HTTP endpoint.
	BootstrapKindEnterprise = "enterprise"
)

// BootstrapOptions configures Tap.Bootstrap, the first-run onboarding that
// materializes or refreshes the user-level tapper config around a deployment
// kind.
type BootstrapOptions struct {
	// Kind selects the deployment: cloud | enterprise. Empty defaults
	// to cloud (atlas is the compiled-in default hub).
	Kind string
	// Endpoint is the hub base URL; required when Kind == enterprise, ignored
	// otherwise. A bare host is upgraded to https://.
	Endpoint string
	// HubName overrides the hub key written for an enterprise endpoint. Empty
	// derives it from the endpoint host (see deriveHubName).
	HubName string
	// Namespace selects the namespace for guided KEG creation.
	Namespace string
}

// BootstrapResult reports what Bootstrap wrote so the CLI can phrase its
// output, drive an optional login, and surface non-fatal config warnings.
type BootstrapResult struct {
	Path      string          // user config path written
	Created   bool            // true when a fresh file was created, false on update
	Kind      string          // normalized deployment kind
	Hub       string          // hub name written as hub
	HubURL    string          // login/display URL
	Namespace string          // transient creation namespace
	Warnings  []ConfigWarning // semantic warnings from ValidateConfig
}

// Bootstrap creates or refreshes the selected user Hub and its connection.
// Existing extension fields and directory mappings survive a re-run.
func (t *Tap) Bootstrap(ctx context.Context, opts BootstrapOptions) (*BootstrapResult, error) {
	kind := strings.TrimSpace(strings.ToLower(opts.Kind))
	if kind == "" {
		kind = BootstrapKindCloud
	}
	switch kind {
	case BootstrapKindCloud, BootstrapKindEnterprise:
	default:
		return nil, fmt.Errorf("unknown bootstrap kind %q (expected cloud or enterprise)", opts.Kind)
	}

	// Namespace is transient input for the guided KEG creation step.
	namespace := strings.TrimSpace(opts.Namespace)
	path := t.PathService.UserConfig()

	// Load the existing user config so a re-run is idempotent; only a genuine
	// "no file yet" is treated as a fresh bootstrap.
	var (
		cfg     *Config
		created bool
	)
	existing, err := t.ConfigService.ReadUserConfigFile()
	switch {
	case err == nil:
		cfg = existing
	case errors.Is(err, keg.ErrNotExist):
		// Start minimal rather than from DefaultUserConfig: the per-kind branch
		// below adds exactly the selected remote hub.
		cfg = &Config{data: &configDTO{KegMap: []KegMapEntry{}}}
		created = true
	default:
		return nil, fmt.Errorf("unable to load user config: %w", err)
	}

	// Resolve the kind-specific hub: its config entry, the hub name,
	// and the URL the CLI uses for an optional login.
	var (
		hubName string
		hubURL  string
	)
	switch kind {
	case BootstrapKindCloud:
		hubName = DefaultHubName
		hubURL = DefaultHubURL
		if _, ok := cfg.Hubs()[hubName]; !ok {
			if err := cfg.SetHub(hubName, HubEntry{URL: DefaultHubURL, TokenEnv: DefaultHubTokenEnv}); err != nil {
				return nil, err
			}
		}

	case BootstrapKindEnterprise:
		raw := strings.TrimSpace(opts.Endpoint)
		if raw == "" {
			return nil, fmt.Errorf("enterprise bootstrap requires an endpoint URL")
		}
		normalized := hubURLWithScheme(raw)
		parsed, perr := url.Parse(normalized)
		if perr != nil || parsed.Host == "" {
			return nil, fmt.Errorf("invalid enterprise endpoint %q", opts.Endpoint)
		}
		hubURL = normalized
		hubName = strings.TrimSpace(opts.HubName)
		if hubName == "" {
			hubName = deriveHubName(parsed.Host)
		}
		// Avoid clobbering an unrelated hub of the same derived name: only reuse
		// the slot when it already points at this URL, else suffix it.
		hubName = uniqueHubName(cfg, hubName, hubURL)
		if err := cfg.SetHub(hubName, HubEntry{URL: hubURL}); err != nil {
			return nil, err
		}
	}

	if err := cfg.SetHubName(context.Background(), hubName); err != nil {
		return nil, err
	}

	warnings := ValidateConfig(cfg)

	if err := cfg.Write(t.Runtime, path); err != nil {
		return nil, err
	}
	// The snapshot predates this write; drop it so nothing in this process
	// reads back a value we just replaced.
	t.ConfigService.Reload()

	return &BootstrapResult{
		Path:      path,
		Created:   created,
		Kind:      kind,
		Hub:       hubName,
		HubURL:    hubURL,
		Namespace: namespace,
		Warnings:  warnings,
	}, nil
}

// SetKeg sets the user config's keg to ref and persists it. It
// is the post-login step of `tap bootstrap`: once the user picks a keg (from the
// hub's list or by typing one), plain `tap` commands resolve it without
// per-invocation flags. It writes the FALLBACK slot (the global-user convention)
// rather than keg, so a project's keg or a kegMap path rule still
// overrides it. ref is a keg reference — a bare name, @namespace/name, keg:...,
// or a path — stored verbatim and resolved later by ResolveRef. A blank ref is a
// no-op.
func (t *Tap) SetKeg(ctx context.Context, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	cfg, err := t.ConfigService.ReadUserConfigFile()
	if err != nil {
		if !errors.Is(err, keg.ErrNotExist) {
			return fmt.Errorf("unable to load user config: %w", err)
		}
		cfg = &Config{data: &configDTO{}}
	}
	if err := cfg.SetKeg(ref); err != nil {
		return err
	}
	if err := cfg.Write(t.Runtime, t.PathService.UserConfig()); err != nil {
		return err
	}
	// The snapshot predates this write; drop it so nothing in this process
	// reads back a value we just replaced.
	t.ConfigService.Reload()
	return nil
}

// SetBootstrapFlight validates ref, canonicalizes it, and persists it as the
// user-level flight baseline. Project config, TAP_FLIGHT, and an explicit
// --flight flag remain higher-precedence overrides. A blank ref is a no-op.
func (t *Tap) SetBootstrapFlight(ctx context.Context, ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	flight, err := t.GetFlight(ctx, GetFlightOptions{Name: ref})
	if err != nil {
		return fmt.Errorf("invalid bootstrap flight %q: %w", ref, err)
	}
	canonical := strings.TrimSpace(flight.Name)
	if canonical == "" {
		return fmt.Errorf("invalid bootstrap flight %q: resolved flight has no canonical reference", ref)
	}
	cfg, err := t.ConfigService.ReadUserConfigFile()
	if err != nil {
		if !errors.Is(err, keg.ErrNotExist) {
			return fmt.Errorf("unable to load user config: %w", err)
		}
		cfg = &Config{data: &configDTO{}}
	}
	if err := cfg.SetFlight(canonical); err != nil {
		return err
	}
	if err := cfg.Write(t.Runtime, t.PathService.UserConfig()); err != nil {
		return err
	}
	// The snapshot predates this write; drop it so nothing in this process
	// reads back a value we just replaced.
	t.ConfigService.Reload()
	return nil
}

// deriveHubName turns an endpoint host into a short hub key: it drops a leading
// service label (keg./api./www.), takes the first remaining DNS label, and
// sanitizes it to lowercase [a-z0-9-]. "keg.acme.com" -> "acme",
// "acme.com" -> "acme". Falls back to "enterprise" when nothing usable remains.
func deriveHubName(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h // drop port
	}
	labels := strings.Split(host, ".")
	// Strip one leading service-style label so keg.acme.com surfaces "acme".
	if len(labels) > 1 {
		switch labels[0] {
		case "keg", "api", "www":
			labels = labels[1:]
		}
	}
	name := ""
	if len(labels) > 0 {
		name = labels[0]
	}
	name = sanitizeHubName(name)
	if name == "" {
		return BootstrapKindEnterprise
	}
	return name
}

// sanitizeHubName lowercases and reduces s to [a-z0-9-], collapsing any other
// run to a single hyphen and trimming leading/trailing hyphens.
func sanitizeHubName(s string) string {
	var b strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen && b.Len() > 0 {
				b.WriteRune('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// uniqueHubName returns base when it is free or already points at url; otherwise
// it appends -2, -3, … until it finds a slot that is free or already maps to
// url. This keeps an enterprise bootstrap from silently overwriting a different
// hub that happens to share the derived name.
func uniqueHubName(cfg *Config, base, url string) string {
	hubs := cfg.Hubs()
	candidate := base
	for i := 2; ; i++ {
		entry, taken := hubs[candidate]
		if !taken || strings.TrimSpace(entry.URL) == strings.TrimSpace(url) {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", base, i)
	}
}
