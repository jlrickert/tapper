package tapper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// HubListOptions selects a saved Hub; omission uses the active Hub.
type HubListOptions struct {
	Hub string
}

// HubListKegs lists identity-accessible KEGs on the selected Hub.
func (t *Tap) HubListKegs(ctx context.Context, opts HubListOptions) ([]string, error) {
	name, entry, err := t.ConfigService.SelectedHub(opts.Hub)
	if err != nil {
		return nil, err
	}
	return t.listHubKegs(ctx, name, entry)
}

// allHubNames returns only the active Hub for remote discovery.
func (t *Tap) allHubNames(cfg *Config) []string { return dedupeStrings([]string{cfg.resolveHubName()}) }

func (t *Tap) savedHubNames(cfg *Config) []string {
	hubs := cfg.Hubs()
	if len(hubs) == 0 {
		return dedupeStrings([]string{cfg.resolveHubName()})
	}
	names := make([]string, 0, len(hubs))
	for n := range hubs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// listHubKegs returns the kegs on a single remote hub as "@namespace/keg".
func (t *Tap) listHubKegs(ctx context.Context, name string, entry HubEntry) ([]string, error) {

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
		// Explicit configuration owns credential selection, even when missing.
		return t.Runtime.Get(target.TokenEnv)
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
	hub := cfg.resolveHubName()
	userHubs := map[string]struct{}{}
	if userCfg, _ := t.ConfigService.UserConfig(); userCfg != nil {
		for name := range userCfg.Hubs() {
			userHubs[name] = struct{}{}
		}
	}
	names := t.savedHubNames(cfg)
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
			IsDefault: name == hub,
			Source:    source,
		})
	}
	return out, nil
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
		return c.SetHub(name, HubEntry{URL: url, TokenEnv: strings.TrimSpace(opts.TokenEnv)})
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

// HubSetDefault sets hub. Unlike the hubs map, hub is allowed in
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
		return c.SetHubName(ctx, name)
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
