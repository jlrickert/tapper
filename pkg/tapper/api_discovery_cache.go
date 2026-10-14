// Package tapper — on-disk cache of Hub API discovery.
//
// Every CLI invocation used to start with GET /api/version before its first
// REST request. apiDiscoveryCache remembers each success under CacheRoot so
// the next invocation within the TTL skips it (see apicontract.Session.Cache).
// Only successes are written, and a stale one is harmless: the Hub still
// validates the contract on every request, and its typed rejection evicts
// the entry.
package tapper

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/apicontract"
)

const apiDiscoveryCacheFile = "api-discovery.json"

// apiDiscoveryCache is an apicontract.DiscoveryCache over one JSON file
// mapping cache keys to the time discovery last succeeded. Every error reads
// as a miss or a skipped write; the cache only ever saves a request.
type apiDiscoveryCache struct {
	rt   *toolkit.Runtime
	path string
	mu   sync.Mutex
}

// NewAPIDiscoveryCache returns the discovery cache under paths' CacheRoot,
// or nil when there is nowhere to keep it.
func NewAPIDiscoveryCache(rt *toolkit.Runtime, paths *PathService) apicontract.DiscoveryCache {
	if rt == nil || paths == nil || paths.AppPaths == nil || paths.CacheRoot == "" {
		return nil
	}
	return &apiDiscoveryCache{rt: rt, path: filepath.Join(paths.CacheRoot, apiDiscoveryCacheFile)}
}

func (c *apiDiscoveryCache) Get(key string) (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	at, ok := c.load()[key]
	return at, ok
}

func (c *apiDiscoveryCache) Put(key string, checked time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries := c.load()
	entries[key] = checked
	c.save(entries)
}

func (c *apiDiscoveryCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries := c.load()
	if _, ok := entries[key]; !ok {
		return
	}
	delete(entries, key)
	c.save(entries)
}

func (c *apiDiscoveryCache) load() map[string]time.Time {
	entries := map[string]time.Time{}
	raw, err := c.rt.ReadFile(c.path)
	if err != nil || len(raw) == 0 {
		return entries
	}
	if json.Unmarshal(raw, &entries) != nil {
		return map[string]time.Time{}
	}
	return entries
}

// save mirrors AuthStore.Save: create the directory 0700 only when missing,
// then replace the file atomically.
func (c *apiDiscoveryCache) save(entries map[string]time.Time) {
	data, err := json.Marshal(entries)
	if err != nil {
		return
	}
	if parent := filepath.Dir(c.path); parent != "" {
		if _, err := c.rt.Stat(parent, false); errors.Is(err, os.ErrNotExist) {
			if err := c.rt.Mkdir(parent, 0o700, true); err != nil {
				return
			}
		}
	}
	_ = c.rt.AtomicWriteFile(c.path, data, 0o600)
}
