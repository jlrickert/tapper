package tapper

import (
	"context"
	"fmt"
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
	cfg, err := t.ConfigService.Config(true)
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
