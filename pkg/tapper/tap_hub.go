package tapper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

// HubListOptions selects which hub to enumerate. An empty Hub aggregates every
// configured hub the user can reach.
type HubListOptions struct {
	Hub string
}

// HubListKegs lists kegs qualified as "@namespace/keg". With no --hub it
// aggregates across every configured hub: local hubs are scanned on disk at
// <basePath>/@<namespace>/<keg>; remote/readonly hubs are queried via the hub's
// GET /api/v1/kegs, which returns the kegs the authenticated user can reach
// (namespace membership + grants). With an explicit --hub only that hub is
// listed and its errors surface directly; in aggregate mode an unreachable or
// unauthenticated hub is logged and skipped so one bad hub doesn't blank the
// whole listing.
func (t *Tap) HubListKegs(ctx context.Context, opts HubListOptions) ([]string, error) {
	cfg, err := t.ConfigService.Config()
	if err != nil {
		return nil, err
	}

	explicit := strings.TrimSpace(opts.Hub)
	var names []string
	if explicit != "" {
		names = []string{explicit}
	} else {
		names = t.allHubNames(cfg)
	}

	seen := map[string]struct{}{}
	var out []string
	for _, name := range names {
		entry, ok := cfg.Hub(name)
		if !ok {
			if explicit != "" {
				return nil, fmt.Errorf("hub %q is not configured", name)
			}
			continue
		}
		kegs, listErr := t.listHubKegs(ctx, name, entry)
		if listErr != nil {
			if explicit != "" {
				return nil, listErr
			}
			if lg := t.Runtime.Logger(); lg != nil {
				lg.Warn("hub list: skipping hub", "hub", name, "err", listErr)
			}
			continue
		}
		for _, k := range kegs {
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}

// allHubNames returns the names of every hub to enumerate in aggregate mode:
// the configured hubs, or — when none are configured — the built-in local and
// default hubs the config synthesizes.
func (t *Tap) allHubNames(cfg *Config) []string {
	hubs := cfg.Hubs()
	if len(hubs) == 0 {
		return dedupeStrings([]string{cfg.localHubName(), cfg.resolveHubName()})
	}
	names := make([]string, 0, len(hubs))
	for n := range hubs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// listHubKegs returns the kegs on a single hub as "@namespace/keg". Local hubs
// scan the filesystem; remote/readonly hubs query GET /api/v1/kegs with the
// hub's resolved bearer token.
func (t *Tap) listHubKegs(ctx context.Context, name string, entry HubEntry) ([]string, error) {
	kind := strings.TrimSpace(entry.Kind)
	if kind == "" {
		kind = HubKindRemote
	}
	if kind == HubKindLocal {
		base, err := t.localHubBase(entry)
		if err != nil {
			return nil, err
		}
		return t.scanLocalHubKegs(base), nil
	}

	url := strings.TrimSpace(entry.URL)
	if url == "" {
		return nil, fmt.Errorf("hub %q has no url configured", name)
	}
	token := t.hubToken(entry)
	if token == "" {
		return nil, fmt.Errorf("hub %q has no auth token (run `tap auth login --hub %s`)", name, url)
	}
	kegs, err := ListUserKegs(ctx, url, token)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(kegs))
	for _, k := range kegs {
		out = append(out, "@"+k.Namespace+"/"+k.Alias)
	}
	return out, nil
}

// localHubBase resolves a local hub's on-disk base directory, defaulting to the
// platform user keg root when the entry has no basePath, then expanding env
// vars and a leading tilde.
func (t *Tap) localHubBase(entry HubEntry) (string, error) {
	base := strings.TrimSpace(entry.BasePath)
	if base == "" {
		root, err := defaultUserKegRoot(t.Runtime)
		if err != nil {
			return "", err
		}
		base = root
	}
	base = toolkit.ExpandEnv(t.Runtime, base)
	if expanded, err := toolkit.ExpandPath(t.Runtime, base); err == nil {
		base = expanded
	}
	return base, nil
}

// hubToken resolves the bearer token for a configured remote hub. It builds a
// synthetic target from the hub entry and defers to hubTokenForTarget.
func (t *Tap) hubToken(entry HubEntry) string {
	url := hubURLWithScheme(strings.TrimSpace(entry.URL))
	if url == "" {
		return ""
	}
	target := keg.Target{
		Url:      strings.TrimRight(url, "/"),
		HubURL:   url,
		Token:    entry.Token,
		TokenEnv: entry.TokenEnv,
	}
	return t.hubTokenForTarget(&target)
}

// hubTokenForTarget resolves the bearer token for a remote target: TokenEnv
// (environment variable) → inline Token → the AuthStore keyed by the hub URL
// (the token `tap auth login` persists). Returns "" when no credential applies.
func (t *Tap) hubTokenForTarget(target *keg.Target) string {
	if target == nil {
		return ""
	}
	if target.TokenEnv != "" {
		if v := t.Runtime.Get(target.TokenEnv); v != "" {
			return v
		}
	}
	if target.Token != "" {
		return target.Token
	}
	if t.KegService == nil {
		return ""
	}
	resolver := t.KegService.tokenResolver()
	if resolver == nil {
		return ""
	}
	return resolver.ResolveToken(target)
}

// HubInfo describes a configured hub for `tap hub list`.
type HubInfo struct {
	Name      string
	URL       string
	Kind      string
	IsDefault bool
	Source    string // "user" or "built-in"
}

// HubList returns the configured hubs (plus the synthesized built-ins when none
// are configured), marking the default and the config layer each came from. It
// inspects local config only — it does not contact any hub.
func (t *Tap) HubList(_ context.Context) ([]HubInfo, error) {
	cfg, err := t.ConfigService.Config()
	if err != nil {
		return nil, err
	}
	defaultHub := cfg.resolveHubName()
	userHubs := map[string]struct{}{}
	if userCfg, _ := t.ConfigService.UserConfig(); userCfg != nil {
		for name := range userCfg.Hubs() {
			userHubs[name] = struct{}{}
		}
	}
	names := t.allHubNames(cfg)
	out := make([]HubInfo, 0, len(names))
	for _, name := range names {
		entry, ok := cfg.Hub(name)
		if !ok {
			continue
		}
		source := "built-in"
		if _, ok := userHubs[name]; ok {
			source = "user"
		}
		out = append(out, HubInfo{
			Name:      name,
			URL:       strings.TrimSpace(entry.URL),
			Kind:      hubKindOrDefault(entry.Kind),
			IsDefault: name == defaultHub,
			Source:    source,
		})
	}
	return out, nil
}

func hubKindOrDefault(kind string) string {
	if k := strings.TrimSpace(kind); k != "" {
		return k
	}
	return HubKindRemote
}

// HubAddOptions adds a remote hub connection to user config.
type HubAddOptions struct {
	Name     string
	URL      string
	TokenEnv string
}

// HubAdd registers a remote hub connection. Hub entries may only live in USER
// config (the trust boundary strips hubs from project config), so this always
// writes the user config regardless of the working directory.
func (t *Tap) HubAdd(_ context.Context, opts HubAddOptions) error {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return fmt.Errorf("a hub name is required")
	}
	url := strings.TrimSpace(opts.URL)
	if url == "" {
		return fmt.Errorf("a hub url is required (--url)")
	}
	return t.mutateConfigFile(t.PathService.UserConfig(), func(c *Config) error {
		return c.SetHub(name, HubEntry{Kind: HubKindRemote, URL: url, TokenEnv: strings.TrimSpace(opts.TokenEnv)})
	})
}

// HubRemoveOptions removes a hub connection from user config.
type HubRemoveOptions struct {
	Name string
}

// HubRemove deletes a hub connection (user config only) and prunes any namespace
// pin that routed to it.
func (t *Tap) HubRemove(_ context.Context, opts HubRemoveOptions) error {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return fmt.Errorf("a hub name is required")
	}
	var removed bool
	if err := t.mutateConfigFile(t.PathService.UserConfig(), func(c *Config) error {
		ok, derr := c.DeleteHub(name)
		if derr != nil {
			return derr
		}
		removed = ok
		for nsName, ref := range c.Namespaces() {
			if ref.Hub == name {
				c.DeleteNamespace(nsName)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("hub %q is not configured in user config", name)
	}
	return nil
}

// HubSetDefaultOptions sets the default hub. It writes project config by default
// and user config with User=true (mirroring `tap config edit`).
type HubSetDefaultOptions struct {
	Name string
	User bool
}

// HubSetDefault sets defaultHub. Unlike the hubs map, defaultHub is allowed in
// project config, so the default write target is the project config, with
// --user to write the user config instead.
func (t *Tap) HubSetDefault(ctx context.Context, opts HubSetDefaultOptions) error {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		return fmt.Errorf("a hub name is required")
	}
	cfg, err := t.ConfigService.Config()
	if err != nil {
		return err
	}
	if _, ok := cfg.Hub(name); !ok {
		return fmt.Errorf("hub %q is not configured", name)
	}
	path := t.PathService.ProjectConfig()
	if opts.User {
		path = t.PathService.UserConfig()
	}
	return t.mutateConfigFile(path, func(c *Config) error {
		return c.SetDefaultHub(ctx, name)
	})
}

// mutateConfigFile reads a single config file (not the merged walk), applies fn,
// and writes it back, creating a fresh config when the file is absent. Used by
// the hub-connection mutators so a single layer is edited in place rather than
// flattening the merged hierarchy.
func (t *Tap) mutateConfigFile(path string, fn func(*Config) error) error {
	resolved, err := t.Runtime.ResolvePath(path, false)
	if err != nil {
		return fmt.Errorf("unable to resolve config path: %w", err)
	}
	var cfg *Config
	raw, readErr := t.Runtime.ReadFile(resolved)
	switch {
	case readErr == nil:
		c, parseErr := ParseConfig(raw)
		if parseErr != nil {
			return fmt.Errorf("existing config is invalid: %w", parseErr)
		}
		cfg = c
	case errors.Is(readErr, os.ErrNotExist):
		cfg = &Config{data: &configDTO{}}
	default:
		return fmt.Errorf("unable to read config: %w", readErr)
	}
	if err := fn(cfg); err != nil {
		return err
	}
	if err := cfg.Write(t.Runtime, resolved); err != nil {
		return fmt.Errorf("unable to write config: %w", err)
	}
	// The snapshot predates this write; drop it so nothing in this process
	// reads back a value we just replaced.
	t.ConfigService.Reload()
	return nil
}

// dedupeStrings returns s with duplicates removed, preserving first-seen order.
func dedupeStrings(s []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(s))
	for _, v := range s {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// scanLocalHubKegs walks <base>/@<namespace>/<keg> and returns every directory
// that is a keg (carries a keg config file), formatted as "@namespace/keg".
func (t *Tap) scanLocalHubKegs(base string) []string {
	nsEntries, err := t.Runtime.ReadDir(base)
	if err != nil {
		return []string{} // a missing base means no kegs, not an error
	}
	var out []string
	for _, nsE := range nsEntries {
		if !nsE.IsDir() || !strings.HasPrefix(nsE.Name(), "@") {
			continue
		}
		ns := strings.TrimPrefix(nsE.Name(), "@")
		nsDir := filepath.Join(base, nsE.Name())
		kegEntries, readErr := t.Runtime.ReadDir(nsDir)
		if readErr != nil {
			continue
		}
		for _, kE := range kegEntries {
			if kE.IsDir() && t.isKegDir(filepath.Join(nsDir, kE.Name())) {
				out = append(out, "@"+ns+"/"+kE.Name())
			}
		}
	}
	sort.Strings(out)
	return out
}

func (t *Tap) isKegDir(dir string) bool {
	for _, name := range []string{"keg", "keg.yaml", "keg.yml"} {
		if _, err := t.Runtime.Stat(filepath.Join(dir, name), false); err == nil {
			return true
		}
	}
	return false
}
