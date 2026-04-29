package tapper

import "fmt"

// ListKegs returns available keg aliases from local discovery paths and config.
// When cache is true, cached config values may be used.
func (t *Tap) ListKegs(cache bool) ([]string, error) {
	cfg, err := t.ConfigService.Config(cache)
	if err != nil {
		return nil, fmt.Errorf("failed to list kegs: %w", err)
	}
	var results []string
	if localKegs, err := t.ConfigService.DiscoveredKegAliases(cache); err == nil {
		results = append(results, localKegs...)
	}

	results = append(results, cfg.ListKegs()...)

	kegDirs := make([]string, 0, len(results))
	seenDirs := make(map[string]bool)
	for _, result := range results {
		dir := firstDir(result)
		if !seenDirs[dir] {
			kegDirs = append(kegDirs, dir)
			seenDirs[dir] = true
		}
	}

	return kegDirs, nil
}
