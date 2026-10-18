// Package tapper — hub authentication store.
//
// AuthStore is the on-disk credentials cache used by the hub-auth flow.
// It maps a canonical hub URL to an access token and
// token metadata. The store is intentionally minimal — it does not know
// how to refresh tokens, talk to a hub, or check expiry. Those concerns
// live in the caller so the store can stay time-agnostic (no clock
// dependency) and therefore trivially testable under a frozen clock.
//
// File layout: YAML at <StateRoot>/auth.yaml, mode 0600, parent dir 0700
// when we create it. Writes go through rt.AtomicWriteFile so a crash or
// concurrent reader never sees a half-written credential file.
package tapper

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"gopkg.in/yaml.v3"
)

// AuthEntry is a single hub's cached credential. Plain value type —
// callers hold copies returned from Get, and pass values into Set.
//
// Fields match the typical OAuth 2.0 shape so we can store whatever a hub
// returns without transformation. TokenType / ExpiresAt / Scope are all
// optional: a hub that only returns a bare bearer token will serialize
// as a single access_token field.
type AuthEntry struct {
	AccessToken string    `yaml:"access_token"`
	TokenType   string    `yaml:"token_type,omitempty"`
	ExpiresAt   time.Time `yaml:"expires_at,omitempty"`
	Scope       string    `yaml:"scope,omitempty"`

	// RefreshToken, ClientID, and TokenEndpoint are populated by the OAuth2
	// device-login flow so the CLI can silently renew an expired AccessToken
	// without a re-login (see RefreshHubToken). All three are empty for a
	// pasted `thub_` API token, which never expires and cannot be refreshed.
	RefreshToken  string `yaml:"refresh_token,omitempty"`
	ClientID      string `yaml:"client_id,omitempty"`
	TokenEndpoint string `yaml:"token_endpoint,omitempty"`

	// AccountKey is a random, local-only name for the login this entry
	// holds. It is minted at login and kept across token refreshes, so state
	// that belongs to the login (the telemetry spool) stays put while its
	// credentials rotate. It is never sent to a hub.
	AccountKey string `yaml:"account_key,omitempty"`
}

// NewAuthAccountKey mints a fresh AuthEntry.AccountKey.
func NewAuthAccountKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// carryAuthAccountKey keeps prev's AccountKey on next when next has none, so
// replacing an entry's credentials never renames the login.
func carryAuthAccountKey(next, prev *AuthEntry) {
	if next != nil && prev != nil && next.AccountKey == "" {
		next.AccountKey = prev.AccountKey
	}
}

// SaveAuthLogin stores entry as the login for hubKey in the auth store at
// storePath, replacing any earlier login for that hub. It holds the auth store
// lock while it reads and saves, so a refresh running in another tap process
// neither loses the write nor overwrites it with the earlier login's tokens.
func SaveAuthLogin(ctx context.Context, rt *toolkit.Runtime, storePath, hubKey string, entry AuthEntry) error {
	unlock, err := lockAuthStore(ctx, rt, storePath, authStoreLockWait)
	if err != nil {
		return err
	}
	defer unlock()
	store, err := LoadAuthStore(ctx, rt, storePath)
	if err != nil {
		return err
	}
	store.Set(hubKey, entry)
	return store.Save(ctx, rt, storePath)
}

// ensureAuthAccountKey returns the AccountKey of the stored entry for hubKey,
// minting and saving one when the entry predates account keys. It holds the
// auth store lock while it reads, mints, and saves, so two processes naming
// the same login at once agree on one key, and neither writes back a token
// pair that a concurrent refresh has already rotated.
func ensureAuthAccountKey(ctx context.Context, rt *toolkit.Runtime, storePath, hubKey string) (string, error) {
	unlock, err := lockAuthStore(ctx, rt, storePath, authStoreLockWait)
	if err != nil {
		return "", err
	}
	defer unlock()
	return ensureAuthAccountKeyLocked(ctx, rt, storePath, hubKey)
}

// ensureAuthAccountKeyLocked is ensureAuthAccountKey for a caller that already
// holds the auth store lock, such as a refresh.
func ensureAuthAccountKeyLocked(ctx context.Context, rt *toolkit.Runtime, storePath, hubKey string) (string, error) {
	store, err := LoadAuthStore(ctx, rt, storePath)
	if err != nil {
		return "", err
	}
	entry, ok := store.Get(hubKey)
	if !ok {
		return "", fmt.Errorf("auth store: no login for %s", hubKey)
	}
	if entry.AccountKey != "" {
		return entry.AccountKey, nil
	}
	entry.AccountKey = NewAuthAccountKey()
	store.Set(hubKey, *entry)
	if err := store.Save(ctx, rt, storePath); err != nil {
		return "", err
	}
	return entry.AccountKey, nil
}

// authStoreDTO is the on-disk shape. Private so callers can't couple to
// the field layout — the opaque AuthStore is the public API. Mirrors the
// Config/configDTO split in config.go.
type authStoreDTO struct {
	// Hubs is keyed by canonical hub URL string. go-yaml v3 sorts map
	// keys alphabetically on marshal, which gives us deterministic
	// output for free; the round-trip test pins that guarantee.
	Hubs map[string]*AuthEntry `yaml:"hubs,omitempty"`
}

// AuthStore is the opaque wrapper exposing getters/setters over an
// authStoreDTO. All methods are nil-safe on the receiver: a (*AuthStore)(nil)
// reads as an empty store and writes are no-ops. This lets callers skip
// the "did Load return something?" dance when the file didn't exist.
//
// mu guards data: the token resolver reads the store from concurrent hub
// requests while a refresh may be writing a rotated entry.
type AuthStore struct {
	mu   sync.RWMutex
	data *authStoreDTO
}

// ParseAuthStore parses raw YAML bytes into an AuthStore. Pure: no I/O,
// no clock. Empty input (including whitespace-only) is treated as an
// empty store rather than a YAML error — first-run and "user cleared
// the file" look the same to us.
func ParseAuthStore(raw []byte) (*AuthStore, error) {
	store := &AuthStore{data: &authStoreDTO{}}
	if len(raw) == 0 {
		return store, nil
	}
	if err := yaml.Unmarshal(raw, store.data); err != nil {
		return nil, fmt.Errorf("failed to parse auth store yaml: %w", err)
	}
	return store, nil
}

// LoadAuthStore reads the auth store file at path and parses it. A
// missing file is NOT an error: we return an empty store and nil so
// first-run callers can treat "no file" and "empty file" identically.
// Every other read error is wrapped and returned.
func LoadAuthStore(ctx context.Context, rt *toolkit.Runtime, path string) (*AuthStore, error) {
	_ = ctx // reserved for future cancellation; rt.ReadFile is synchronous today
	b, err := rt.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// First-run: no credentials cached yet. Empty store, no
			// error — callers use Get/Hubs to discover emptiness.
			return &AuthStore{data: &authStoreDTO{}}, nil
		}
		return nil, fmt.Errorf("unable to read auth store: %w", err)
	}
	return ParseAuthStore(b)
}

// Get returns a copy of the entry for hubURL and a present flag. We
// return a value (not a pointer into the map) so callers can't mutate
// the stored entry by accident — all edits must go through Set.
func (s *AuthStore) Get(hubURL string) (*AuthEntry, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data == nil || s.data.Hubs == nil {
		return nil, false
	}
	entry, ok := s.data.Hubs[hubURL]
	if !ok || entry == nil {
		return nil, false
	}
	// Copy-by-value so caller mutations don't leak back into the map.
	out := *entry
	return &out, true
}

// Set inserts or overwrites the entry for hubURL. Nil receiver is a
// no-op (matches the "read as empty" contract). We store a copy so
// later caller mutations don't reach into our map.
func (s *AuthStore) Set(hubURL string, entry AuthEntry) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = &authStoreDTO{}
	}
	if s.data.Hubs == nil {
		s.data.Hubs = map[string]*AuthEntry{}
	}
	copied := entry
	s.data.Hubs[hubURL] = &copied
}

// Delete removes the entry for hubURL. Returns whether it existed —
// callers can use this to decide whether to log "logged out" vs "was
// already logged out".
func (s *AuthStore) Delete(hubURL string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil || s.data.Hubs == nil {
		return false
	}
	if _, ok := s.data.Hubs[hubURL]; !ok {
		return false
	}
	delete(s.data.Hubs, hubURL)
	return true
}

// Hubs returns the hub URL keys, sorted, for enumeration. Sorted output
// makes CLI listings stable without callers having to re-sort.
func (s *AuthStore) Hubs() []string {
	if s == nil {
		return []string{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data == nil || s.data.Hubs == nil {
		return []string{}
	}
	keys := make([]string, 0, len(s.data.Hubs))
	for k := range s.data.Hubs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// IsEmpty reports whether the store has no hubs. Used by Save to decide
// whether to delete the file rather than write an empty document.
func (s *AuthStore) IsEmpty() bool {
	if s == nil {
		return true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data == nil || s.data.Hubs == nil {
		return true
	}
	return len(s.data.Hubs) == 0
}

// Save writes the store to path. When the store is empty we remove the
// file instead of writing an empty YAML doc — keeps the filesystem tidy
// after a `tap auth logout` of the last hub and makes "no file" the
// canonical empty state (matches LoadAuthStore's contract). Parent
// directory is created at 0700 if it doesn't exist. If it already
// exists we don't touch its mode — users/admins may have intentionally
// widened it, and tightening on every Save would surprise them.
func (s *AuthStore) Save(ctx context.Context, rt *toolkit.Runtime, path string) error {
	_ = ctx // reserved for cancellation; filesystem ops here are synchronous
	if s == nil {
		return nil
	}

	if s.IsEmpty() {
		// Best-effort remove. A pre-existing absence is the desired
		// post-state, so ErrNotExist is success not failure.
		if err := rt.Remove(path, false); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("unable to remove empty auth store: %w", err)
		}
		return nil
	}

	// Create the parent dir only if it's missing. Stat-first avoids
	// accidentally widening 0700 → 0755 when MkdirAll walks an existing
	// tree with different perms.
	parent := parentDir(path)
	if parent != "" {
		if _, err := rt.Stat(parent, false); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if mkErr := rt.Mkdir(parent, 0o700, true); mkErr != nil {
					return fmt.Errorf("unable to create auth store directory: %w", mkErr)
				}
			} else {
				return fmt.Errorf("unable to stat auth store directory: %w", err)
			}
		}
	}

	s.mu.RLock()
	data, err := yaml.Marshal(s.data)
	s.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("unable to marshal auth store: %w", err)
	}

	// 0600: owner-only read/write. AtomicWriteFile chmods the temp
	// before rename, so mode is re-applied on every Save regardless of
	// any external drift.
	if err := rt.AtomicWriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("unable to write auth store: %w", err)
	}
	return nil
}

// parentDir is a tiny helper kept here so the package doesn't gain a
// path utility module just for one use. Returns "" when there's no
// parent (e.g., a bare filename) so callers can skip the Mkdir step.
func parentDir(path string) string {
	// Equivalent to filepath.Dir, but we want "" instead of "." for the
	// bare-filename case — "." as a Mkdir target would be a no-op that
	// still burns a syscall, and it muddles intent.
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			return path[:i]
		}
	}
	return ""
}
