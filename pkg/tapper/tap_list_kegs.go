package tapper

import "fmt"

// ListKegs returns the configured keg aliases. When cache is true, cached
// config values may be used.
func (t *Tap) ListKegs(cache bool) ([]string, error) {
	cfg, err := t.ConfigService.Config(cache)
	if err != nil {
		return nil, fmt.Errorf("failed to list kegs: %w", err)
	}

	results := cfg.ListKegs()

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
