package tapper

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

// localHubKey returns the map key for this machine's built-in local hub: the
// sanitized machine hostname, falling back to LocalHubName when the hostname is
// unavailable. Keying by hostname keeps a config that travels between machines
// unambiguous, while the reserved @local namespace stays the portable handle
// for references. The hostname is read from the runtime environment first (so
// it is deterministic under a sandboxed runtime and overridable in CI) and from
// the OS otherwise.
func localHubKey(rt *toolkit.Runtime) string {
	host := strings.TrimSpace(rt.Env().Get("HOSTNAME"))
	if host == "" {
		if h, err := os.Hostname(); err == nil {
			host = h
		}
	}
	if name := sanitizeHubName(host); name != "" {
		return name
	}
	return LocalHubName
}

// Bootstrap deployment kinds. Each maps onto an existing hub kind/shape — there
// is no new config field, only a guided way to pick one.
const (
	// BootstrapKindLocal sets up only the built-in local filesystem hub.
	BootstrapKindLocal = "local"
	// BootstrapKindCloud targets the compiled-in atlas remote hub.
	BootstrapKindCloud = "cloud"
	// BootstrapKindEnterprise registers a user-supplied remote HTTP endpoint.
	BootstrapKindEnterprise = "enterprise"
)

// BootstrapOptions configures Tap.Bootstrap, the first-run onboarding that
// materializes or refreshes the user-level tapper config around a deployment
// kind.
type BootstrapOptions struct {
	// Kind selects the deployment: local | cloud | enterprise. Empty defaults
	// to cloud (atlas is the compiled-in default hub).
	Kind string
	// Endpoint is the hub base URL; required when Kind == enterprise, ignored
	// otherwise. A bare host is upgraded to https://.
	Endpoint string
	// HubName overrides the hub key written for an enterprise endpoint. Empty
	// derives it from the endpoint host (see deriveHubName).
	HubName string
	// Namespace overrides the fallback namespace. Empty auto-derives it from
	// the OS user, then LocalHubName.
	Namespace string
}

// BootstrapResult reports what Bootstrap wrote so the CLI can phrase its
// output, drive an optional login, and surface non-fatal config warnings.
type BootstrapResult struct {
	Path      string          // user config path written
	Created   bool            // true when a fresh file was created, false on update
	Kind      string          // normalized deployment kind
	Hub       string          // hub name written as fallbackHub
	HubURL    string          // login/display URL for cloud/enterprise; "" for local
	Namespace string          // resolved fallback namespace
	Warnings  []ConfigWarning // semantic warnings from ValidateConfig
}

// Bootstrap creates or refreshes the user-level config for a chosen deployment
// kind so plain `tap` commands resolve without per-invocation flags. It writes
// the FALLBACK hub (the user/global convention — project config owns the
// high-precedence default* slots) and always ensures the built-in local hub is
// present and that the reserved @local namespace maps to it.
//
// It does not write a global fallbackNamespace or a per-user namespace→hub
// entry: the preferred namespace comes from the resolved hub's own namespace
// field. For local that is @local; for cloud/enterprise it is the logged-in
// user's home namespace, adopted onto the hub after login by
// SetBootstrapNamespace. The only namespace→hub entry written is local→localHub.
//
// It is idempotent: an existing config is loaded and only the fallback hub, the
// local namespace mapping, and the kind's hub entry are touched, so user-defined
// kegs/kegMap survive a re-run untouched.
func (t *Tap) Bootstrap(ctx context.Context, opts BootstrapOptions) (*BootstrapResult, error) {
	kind := strings.TrimSpace(strings.ToLower(opts.Kind))
	if kind == "" {
		kind = BootstrapKindCloud
	}
	switch kind {
	case BootstrapKindLocal, BootstrapKindCloud, BootstrapKindEnterprise:
	default:
		return nil, fmt.Errorf("unknown bootstrap kind %q (expected local, cloud, or enterprise)", opts.Kind)
	}

	// Namespace stored on the kind's hub entry. For a local deployment the home
	// namespace is the reserved @local. For cloud/enterprise the authoritative
	// value is the logged-in user's home namespace, which only the hub knows; it
	// is adopted after login via SetBootstrapNamespace and lives on the hub's own
	// namespace field. Until then it stays empty rather than guessing the OS user
	// — a bogus guess would resolve bare references to the wrong namespace
	// instead of erroring clearly. An explicit opts.Namespace always wins.
	namespace := strings.TrimSpace(opts.Namespace)
	if namespace == "" && kind == BootstrapKindLocal {
		namespace = LocalHubName
	}

	localRoot, err := defaultUserKegRoot(t.Runtime)
	if err != nil {
		return nil, fmt.Errorf("unable to resolve local keg root: %w", err)
	}

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
		// below adds the remote hub it needs, so a `local` bootstrap stays
		// local-only instead of carrying an unsolicited atlas entry. The
		// always-ensure step seeds the built-in local hub.
		cfg = &Config{data: &configDTO{KegMap: []KegMapEntry{}}}
		created = true
	default:
		return nil, fmt.Errorf("unable to load user config: %w", err)
	}

	// The built-in local hub is always available so local kegs work regardless
	// of the chosen deployment. It is keyed by the machine hostname and defaults
	// to the reserved @local namespace; on-disk kegs live at
	// <basePath>/@local/<name>.
	localKey := localHubKey(t.Runtime)
	if _, ok := cfg.Hubs()[localKey]; !ok {
		if err := cfg.SetHub(localKey, HubEntry{Kind: HubKindLocal, DefaultNamespace: LocalHubName, BasePath: localRoot}); err != nil {
			return nil, err
		}
	}
	// Pin the reserved @local namespace to this machine's local hub. This is the
	// only namespace→hub entry bootstrap writes: every other namespace's hub is
	// resolved from the default/fallback hub chain, and the preferred namespace
	// comes from that hub's own namespace field — so no per-user entry is needed.
	if err := cfg.SetNamespace(LocalHubName, NamespaceRef{Hub: localKey}); err != nil {
		return nil, err
	}

	// Resolve the kind-specific hub: its config entry, the fallbackHub name,
	// and the URL the CLI uses for an optional login.
	var (
		hubName string
		hubURL  string
	)
	switch kind {
	case BootstrapKindLocal:
		hubName = localKey

	case BootstrapKindCloud:
		hubName = DefaultHubName
		hubURL = DefaultHubURL
		if _, ok := cfg.Hubs()[hubName]; !ok {
			if err := cfg.SetHub(hubName, HubEntry{Kind: HubKindRemote, DefaultNamespace: namespace, URL: DefaultHubURL, TokenEnv: DefaultHubTokenEnv}); err != nil {
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
		if err := cfg.SetHub(hubName, HubEntry{Kind: HubKindRemote, DefaultNamespace: namespace, URL: hubURL}); err != nil {
			return nil, err
		}
	}

	if err := cfg.SetFallbackHub(hubName); err != nil {
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

// SetBootstrapNamespace adopts namespace as the named hub's default namespace,
// then persists the config. It is the post-login step of `tap bootstrap`: once a
// cloud/enterprise login resolves the authenticated user's home namespace (from
// the hub's whoami probe), the CLI calls this so plain references resolved
// against that hub land in the user's own namespace. The namespace lives on the
// hub entry so each hub carries its own default — there is no global
// fallbackNamespace and no per-user namespace→hub entry to maintain.
//
// It is idempotent and a no-op when namespace is blank, or when hubName is
// unknown/blank (nothing to adopt onto), keeping the call safe in either case.
// The always-present local hub keeps its reserved @local namespace.
func (t *Tap) SetBootstrapNamespace(ctx context.Context, hubName, namespace string) error {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return nil
	}
	cfg, err := t.ConfigService.ReadUserConfigFile()
	if err != nil {
		return fmt.Errorf("unable to load user config: %w", err)
	}
	entry, ok := cfg.Hubs()[hubName]
	if !ok {
		return nil
	}
	entry.DefaultNamespace = namespace
	if err := cfg.SetHub(hubName, entry); err != nil {
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

// SetHubDefaultNamespaceByURL adopts namespace onto the configured hub whose
// URL matches hubURL. It returns the hub name that was updated. When no
// configured hub matches (for example, auth login fell through to the
// compiled-in atlas URL without a user config entry), it is a no-op.
func (t *Tap) SetHubDefaultNamespaceByURL(ctx context.Context, hubURL, namespace string) (string, error) {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return "", nil
	}
	canonical := CanonicalHubURL(hubURLWithScheme(hubURL))
	if canonical == "" {
		return "", nil
	}
	cfg, err := t.ConfigService.ReadUserConfigFile()
	if err != nil {
		if errors.Is(err, keg.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("unable to load user config: %w", err)
	}
	for name, entry := range cfg.Hubs() {
		if strings.TrimSpace(entry.URL) == "" {
			continue
		}
		if CanonicalHubURL(hubURLWithScheme(entry.URL)) != canonical {
			continue
		}
		entry.DefaultNamespace = namespace
		if err := cfg.SetHub(name, entry); err != nil {
			return "", err
		}
		if err := cfg.Write(t.Runtime, t.PathService.UserConfig()); err != nil {
			return "", err
		}
		// The snapshot predates this write; drop it so nothing in this process
		// reads back a value we just replaced.
		t.ConfigService.Reload()
		return name, nil
	}
	return "", nil
}

// SetFallbackKeg sets the user config's fallbackKeg to ref and persists it. It
// is the post-login step of `tap bootstrap`: once the user picks a keg (from the
// hub's list or by typing one), plain `tap` commands resolve it without
// per-invocation flags. It writes the FALLBACK slot (the global-user convention)
// rather than defaultKeg, so a project's defaultKeg or a kegMap path rule still
// overrides it. ref is a keg reference — a bare name, @namespace/name, keg:...,
// or a path — stored verbatim and resolved later by ResolveRef. A blank ref is a
// no-op.
func (t *Tap) SetFallbackKeg(ctx context.Context, ref string) error {
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
	if err := cfg.SetFallbackKeg(ref); err != nil {
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
