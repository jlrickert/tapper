package tapper

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

// HubListOptions selects which hub to enumerate. An empty Hub uses the local
// hub.
type HubListOptions struct {
	Hub string
}

// HubListKegs lists the kegs available on a hub, qualified as "@namespace/keg".
// For a local hub it scans <basePath>/@<namespace>/<keg> on disk. Remote hub
// enumeration requires hub API support and is not yet implemented.
func (t *Tap) HubListKegs(_ context.Context, opts HubListOptions) ([]string, error) {
	cfg, err := t.ConfigService.Config(true)
	if err != nil {
		return nil, err
	}
	hubName := strings.TrimSpace(opts.Hub)
	if hubName == "" {
		hubName = cfg.localHubName()
	}
	entry, ok := cfg.Hub(hubName)
	if !ok {
		return nil, fmt.Errorf("hub %q is not configured", hubName)
	}
	kind := strings.TrimSpace(entry.Kind)
	if kind == "" {
		kind = HubKindRemote
	}
	if kind != HubKindLocal {
		return nil, fmt.Errorf("listing kegs on remote hub %q is not yet supported (needs hub API)", hubName)
	}

	base := strings.TrimSpace(entry.BasePath)
	if base == "" {
		root, rootErr := defaultUserKegRoot(t.Runtime)
		if rootErr != nil {
			return nil, rootErr
		}
		base = root
	}
	base = toolkit.ExpandEnv(t.Runtime, base)
	if expanded, expErr := toolkit.ExpandPath(t.Runtime, base); expErr == nil {
		base = expanded
	}
	return t.scanLocalHubKegs(base), nil
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
