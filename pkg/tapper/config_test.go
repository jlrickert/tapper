package tapper_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
)

func TestResolveKegTarget_RepoLocal(t *testing.T) {
	tmp, err := os.MkdirTemp("", "tapper-test-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	// create .tapper/local.yaml with a keg target
	tapperDir := filepath.Join(tmp, tapper.DefaultRepoTapperDir)
	if err := os.MkdirAll(tapperDir, 0o755); err != nil {
		t.Fatalf("failed to mkdir %s: %v", tapper.DefaultRepoTapperDir, err)
	}

	localYAML := `updated: 2025-08-14T12:00:00Z
keg:
  alias: work
  path: $HOME/kegs/work
  prefer_local: true
note: "persisted override"
`
	localPath := filepath.Join(tapperDir, "local.yaml")
	if err := os.WriteFile(localPath, []byte(localYAML), 0o644); err != nil {
		t.Fatalf("failed to write local.yaml: %v", err)
	}

	res, err := tapper.ResolveKegTargetForRepo(tmp)
	if err != nil {
		t.Fatalf("ResolveKegTargetForRepo returned error: %v", err)
	}

	if res.Source != "repo-local" {
		t.Fatalf("expected Source 'repo-local', got %q", res.Source)
	}
	if res.Target == nil {
		t.Fatalf("expected non-nil Target, got nil")
	}
	if res.Target.Alias != "work" {
		t.Fatalf("expected Target.Alias 'work', got %q", res.Target.Alias)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("failed to get user home directory: %v", err)
	}

	workPath := filepath.Join(home, "kegs", "work")
	if res.Target.Path != workPath {
		t.Fatalf("expected Target.Path %q, got %q (home=%s)", workPath, res.Target.Path, home)
	}
	if !res.Target.PreferLocal {
		t.Fatalf("expected Target.PreferLocal true")
	}
}

func TestResolveKegTarget_Env(t *testing.T) {
	tmp, err := os.MkdirTemp("", "tapper-test-env-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	// Set KEG_CURRENT env var and ensure it's returned as-is.
	const envVal = "env:keg-target"
	if err := os.Setenv("KEG_CURRENT", envVal); err != nil {
		t.Fatalf("failed to set KEG_CURRENT: %v", err)
	}
	defer os.Unsetenv("KEG_CURRENT")

	res, err := tapper.ResolveKegTargetForRepo(tmp)
	if err != nil {
		t.Fatalf("ResolveKegTargetForRepo returned error: %v", err)
	}
	if res.Source != "env" {
		t.Fatalf("expected Source 'env', got %q", res.Source)
	}
	if res.Value != envVal {
		t.Fatalf("expected Value %q, got %q", envVal, res.Value)
	}
}

func TestResolveKegTarget_GitConfig(t *testing.T) {
	// Skip if git is not available.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available; skipping TestResolveKegTarget_GitConfig")
	}

	tmp, err := os.MkdirTemp("", "tapper-test-git-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	// Initialize a git repo and set local config tap.keg
	cmd := exec.Command("git", "init", "--initial-branch=main")
	cmd.Dir = tmp
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v - %s", err, string(out))
	}

	cfgCmd := exec.Command("git", "-C", tmp, "config", "--local", "tap.keg", "git-target")
	if out, err := cfgCmd.CombinedOutput(); err != nil {
		t.Fatalf("git config failed: %v - %s", err, string(out))
	}

	res, err := tapper.ResolveKegTargetForRepo(tmp)
	if err != nil {
		t.Fatalf("ResolveKegTargetForRepo returned error: %v", err)
	}
	if res.Source != "git-config" {
		t.Fatalf("expected Source 'git-config', got %q", res.Source)
	}
	if res.Value != "git-target" {
		t.Fatalf("expected Value 'git-target', got %q", res.Value)
	}
}

func TestResolveKegTarget_ProjectKeg(t *testing.T) {
	tmp, err := os.MkdirTemp("", "tapper-test-projectkeg-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	// create docs/keg file (project keg)
	docsDir := filepath.Join(tmp, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatalf("failed to mkdir docs: %v", err)
	}
	kegPath := filepath.Join(docsDir, "keg")
	if err := os.WriteFile(kegPath, []byte("updated: 2025-08-14T12:00:00Z\n"), 0o644); err != nil {
		t.Fatalf("failed to write docs/keg: %v", err)
	}

	// Ensure no env/git/.tapper to interfere.
	_ = os.Unsetenv("KEG_CURRENT")

	res, err := tapper.ResolveKegTargetForRepo(tmp)
	if err != nil {
		t.Fatalf("ResolveKegTargetForRepo returned error: %v", err)
	}
	if res.Source != "project-keg" {
		t.Fatalf("expected Source 'project-keg', got %q", res.Source)
	}
	// Expect the returned Value to point to the project keg path.
	// Implementation may return an absolute path; compare cleaned suffix.
	if res.Value == "" {
		t.Fatalf("expected non-empty Value for project-keg")
	}
	if filepath.Clean(res.Value) != filepath.Clean(kegPath) {
		t.Fatalf("expected Value %q, got %q", kegPath, res.Value)
	}
}

func TestResolveKegTarget_UserAlias(t *testing.T) {
	tmp, err := os.MkdirTemp("", "tapper-test-useralias-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	// Prepare a user config dir and aliases file via XDG_CONFIG_HOME to isolate test.
	xdg := filepath.Join(tmp, "config")
	if err := os.MkdirAll(filepath.Join(xdg, "tapper"), 0o755); err != nil {
		t.Fatalf("failed to create xdg config dir: %v", err)
	}
	aliasesYAML := `updated: 2025-08-14T12:00:00Z
aliases:
  work:
    url: git@github.com:org/work-keg.git
    prefer_local: true
`
	aliasPath := filepath.Join(xdg, "tapper", "aliases.yaml")
	if err := os.WriteFile(aliasPath, []byte(aliasesYAML), 0o644); err != nil {
		t.Fatalf("failed to write aliases.yaml: %v", err)
	}

	// Point resolver at our XDG_CONFIG_HOME and ensure no higher-priority sources exist.
	if err := os.Setenv("XDG_CONFIG_HOME", xdg); err != nil {
		t.Fatalf("failed to set XDG_CONFIG_HOME: %v", err)
	}
	defer os.Unsetenv("XDG_CONFIG_HOME")
	_ = os.Unsetenv("KEG_CURRENT")

	res, err := tapper.ResolveKegTargetForRepo(tmp)
	if err != nil {
		t.Fatalf("ResolveKegTargetForRepo returned error: %v", err)
	}

	// Expect user-alias to be chosen when no higher-priority source exists.
	if res.Source != "user-alias" {
		t.Fatalf("expected Source 'user-alias', got %q", res.Source)
	}
	if res.Target == nil {
		t.Fatalf("expected non-nil Target for user-alias, got nil")
	}
	// The resolver should expose the alias name and the URL from the aliases file.
	if res.Target.Alias != "work" {
		t.Fatalf("expected Target.Alias 'work', got %q", res.Target.Alias)
	}
	if res.Target.URL == "" {
		t.Fatalf("expected Target.URL to be set for alias 'work'")
	}
}

func TestResolveKegTarget_NoneFound(t *testing.T) {
	tmp, err := os.MkdirTemp("", "tapper-test-none-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

	// Ensure env and XDG config are not interfering.
	_ = os.Unsetenv("KEG_CURRENT")
	_ = os.Unsetenv("XDG_CONFIG_HOME")

	// No KEG_CURRENT, no git config, no .tapper/local.yaml, no docs/keg present.
	res, err := tapper.ResolveKegTargetForRepo(tmp)
	if err != nil {
		t.Fatalf("ResolveKegTargetForRepo returned error: %v", err)
	}

	// When nothing is found, ResolveKegTargetForRepo should return an empty result
	// (no Source set) and no error.
	if res.Source != "" {
		t.Fatalf("expected empty Source when nothing is found, got %q", res.Source)
	}
	if res.Target != nil {
		t.Fatalf("expected nil Target when nothing is found, got %+v", res.Target)
	}
}
