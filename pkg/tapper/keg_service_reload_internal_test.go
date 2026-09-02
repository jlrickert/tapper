package tapper

import (
	"path/filepath"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/keg"
)

// TestReloadAuthStore_PicksUpCredentialsWrittenAfterFirstLoad covers the half of
// tapper#87 that makes the guidance true. The credential store is loaded once
// per process, so a `tap auth login` run in another shell was invisible to a
// long-lived MCP server and reorienting could not clear a 401.
func TestReloadAuthStore_PicksUpCredentialsWrittenAfterFirstLoad(t *testing.T) {
	fx := sandbox.NewSandbox(t, &sandbox.Options{
		Home: filepath.FromSlash("/home/testuser"),
		User: "testuser",
	})
	if err := fx.Setwd("/home/testuser"); err != nil {
		t.Fatalf("setwd: %v", err)
	}
	tap, err := NewTap(TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	if err != nil {
		t.Fatalf("new tap: %v", err)
	}

	const hubURL = "https://atlas.foldwise.ai"
	target := &keg.Target{HubURL: hubURL, Url: hubURL + "/api/v1/@ns/kegs/example"}

	// First resolution happens before any login, caching an empty store.
	if tok := tap.KegService.tokenResolver().ResolveToken(target); tok != "" {
		t.Fatalf("token before login = %q, want empty", tok)
	}

	// Simulate `tap auth login` in a separate process writing the store.
	store := &AuthStore{data: &authStoreDTO{}}
	store.Set(hubURL, AuthEntry{AccessToken: "thub_fresh"})
	if err := store.Save(fx.Context(), fx.Runtime(), tap.PathService.AuthStorePath()); err != nil {
		t.Fatalf("save auth store: %v", err)
	}

	// Without a reload the process keeps its startup view of credentials.
	if tok := tap.KegService.tokenResolver().ResolveToken(target); tok != "" {
		t.Fatalf("token = %q before reload; the cache should still be stale", tok)
	}

	tap.KegService.ReloadAuthStore()

	if tok := tap.KegService.tokenResolver().ResolveToken(target); tok != "thub_fresh" {
		t.Fatalf("token after reload = %q, want %q", tok, "thub_fresh")
	}
}

// TestReloadAuthStore_ClearsResolvedKegCache confirms the reload also drops
// kegs resolved earlier. They captured the old resolver, so leaving them cached
// would keep the stale credential in play despite the store being reloaded.
func TestReloadAuthStore_ClearsResolvedKegCache(t *testing.T) {
	svc := &KegService{kegCache: map[string]keg.Keg{"stale": nil}}
	svc.ReloadAuthStore()
	if len(svc.kegCache) != 0 {
		t.Fatalf("kegCache retained %d entries after reload", len(svc.kegCache))
	}
}
