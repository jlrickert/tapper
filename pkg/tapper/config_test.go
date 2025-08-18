package tapper_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// createTestRepo creates a temporary directory to act as a test repository.
// It registers a cleanup to remove the directory when the test completes and
// returns the created path.
func createTestRepo(t *testing.T, repoName string) string {
	t.Helper()
	tmp, err := os.MkdirTemp("", repoName)
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	return tmp
}

// stubKegRepo creates a minimal KEG project layout in the provided repoPath.
// It creates docs/ and writes a simple docs/keg file so tests can exercise
// project-level keg file discovery.
func stubKegRepo(t *testing.T, repoPath string) {
	t.Helper()

	// create docs/keg file (project keg)
	docsDir := filepath.Join(repoPath, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatalf("failed to mkdir docs: %v", err)
	}
	kegPath := filepath.Join(docsDir, "keg")
	if err := os.WriteFile(kegPath, []byte("updated: 2025-08-14T12:00:00Z\n"), 0o644); err != nil {
		t.Fatalf("failed to write docs/keg: %v", err)
	}
}

// createTestUserConfig writes a temporary XDG-style user config directory
// containing the provided UserConfig. It sets XDG_CONFIG_HOME to point to the
// created directory for the duration of the test and returns the base path.
func createTestUserConfig(t *testing.T, name string, uc *tapper.UserConfig) string {
	t.Helper()
	tmp, err := os.MkdirTemp("", name)
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	xdg := filepath.Join(tmp, "config")
	if err := os.MkdirAll(filepath.Join(xdg, "tapper"), 0o755); err != nil {
		t.Fatalf("failed to create xdg config dir: %v", err)
	}

	if err := uc.WriteUserConfig(filepath.Join(xdg, "tapper", "config.yaml")); err != nil {
		t.Fatalf("failed to write config.yaml: %v", err)
	}

	if err := os.Setenv("XDG_CONFIG_HOME", xdg); err != nil {
		t.Fatalf("failed to set XDG_CONFIG_HOME: %v", err)
	}
	t.Cleanup(func() {
		os.Unsetenv("XDG_CONFIG_HOME")
	})
	_ = os.Unsetenv("KEG_CURRENT")

	return xdg
}

// getInit initializes a git repository at the given root path. If git is not
// available in PATH the test is skipped. This helper is used by tests that
// require a git repo to exercise git-config precedence.
func getInit(t *testing.T, root string) {
	if _, err := exec.LookPath("git"); err != nil {
		t.SkipNow()
	}

	// Initialize git repo and set a git config if git available (even lower precedence)
	cmd := exec.Command("git", "init", "--initial-branch=main")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init failed: %v - %s", err, string(out))
	}
}

// gitSetKegAlias sets the local git config key `tap.keg` in the repository at
// root to the provided alias. If git is not available the test is skipped.
func gitSetKegAlias(t *testing.T, root string, alias string) {
	if _, err := exec.LookPath("git"); err != nil {
		t.SkipNow()
	}

	cfgCmd := exec.Command("git", "-C", root, "config", "--local", "tap.keg", alias)
	if out, err := cfgCmd.CombinedOutput(); err != nil {
		t.Fatalf("git config failed: %v - %s", err, string(out))
	}
}

// TestResolveKegTarget_RepoLocal verifies that a repo-local .tapper/local.yaml
// is read and its KegTarget.Path expands environment variables correctly.
func TestResolveKegTarget_RepoLocal(t *testing.T) {
	repoRoot := createTestRepo(t, "tapper-test-")
	lc := tapper.LocalConfig{
		Updated: "2025-08-14T12:00:00Z",
		Keg: tapper.KegTarget{
			URL:         "https://work.com/keg",
			Path:        "$HOME/kegs/work",
			PreferLocal: true,
		},
	}
	if err := lc.WriteLocalFile(repoRoot); err != nil {
		t.Fatalf("failed to write repo-local .tapper/local.yaml: %v", err)
	}

	res, err := tapper.ResolveKegTargetForRepo(repoRoot)
	if err != nil {
		t.Fatalf("ResolveKegTargetForRepo returned error: %v", err)
	}

	res.ExpandEnv()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("failed to get user home directory: %v", err)
	}

	workPath := filepath.Join(home, "kegs", "work")
	if res.Path != workPath {
		t.Fatalf("expected Target.Path %q, got %q (home=%s)", workPath, res.Path, home)
	}
	if !res.PreferLocal {
		t.Fatalf("expected Target.PreferLocal true")
	}
}

// TestResolveKegTarget_Env ensures that when KEG_CURRENT environment variable
// is set the resolver returns it with Source == "env".
func TestResolveKegTarget_Env(t *testing.T) {
	tmp := createTestRepo(t, "tapper-test-env-")

	// Set KEG_CURRENT env var and ensure it's returned as-is.
	const envVal = "keg-target"
	if err := os.Setenv("KEG_CURRENT", envVal); err != nil {
		t.Fatalf("failed to set KEG_CURRENT: %v", err)
	}
	defer os.Unsetenv("KEG_CURRENT")

	res, err := tapper.ResolveKegTargetForRepo(tmp)
	if err != nil {
		t.Fatalf("ResolveKegTargetForRepo returned error: %v", err)
	}
	if res.Value != envVal {
		t.Fatalf("expected Value %q, got %q", envVal, res.Value)
	}
	if res.Source != "env" {
		t.Fatalf("expected Source 'env', got %q", res.Source)
	}
}

// TestResolveKegTarget_GitConfig verifies that a git local config setting for
// tap.keg is detected and returned as Source "git".
func TestResolveKegTarget_GitConfig(t *testing.T) {
	// Skip if git is not available.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available; skipping TestResolveKegTarget_GitConfig")
	}

	root := createTestRepo(t, "tapper-test-git-")
	getInit(t, root)
	gitSetKegAlias(t, root, "git-target")

	res, err := tapper.ResolveKegTargetForRepo(root)
	if err != nil {
		t.Fatalf("ResolveKegTargetForRepo returned error: %v", err)
	}
	if res.Source != "git" {
		t.Fatalf("expected Source 'git', got %q", res.Source)
	}
	if res.Value != "git-target" {
		t.Fatalf("expected Alias 'git-target', got %q", res.Alias)
	}
}

// TestResolveKegTarget_ProjectKeg checks that a project-level docs/keg file is
// discovered and returned as a Path-based target when no higher-precedence
// sources are present.
func TestResolveKegTarget_ProjectKeg(t *testing.T) {
	repoRoot := createTestRepo(t, "tapper-test-projectkeg-")
	stubKegRepo(t, repoRoot)

	// Ensure no env/git/.tapper to interfere.
	_ = os.Unsetenv("KEG_CURRENT")

	res, err := tapper.ResolveKegTargetForRepo(repoRoot)
	if err != nil {
		t.Fatalf("ResolveKegTargetForRepo returned error: %v", err)
	}
	// Expect the returned Path to point to the project keg path.
	if res.Path == "" {
		t.Fatalf("expected non-empty Path for project-keg")
	}
	kegPath := filepath.Join(repoRoot, "docs", "keg")
	if filepath.Clean(res.Path) != filepath.Clean(kegPath) {
		t.Fatalf("expected Path %q, got %q", kegPath, res.Path)
	}
	if res.Source == "" {
		t.Fatalf("expected Source to be set for project-keg resolution")
	}
}

// TestResolveKegTarget_UserAlias ensures that user-level tapper config aliases
// and mappings are used to resolve a KegTarget alias for a repository.
func TestResolveKegTarget_UserAlias(t *testing.T) {
	tmp := createTestRepo(t, "tapper-test-useralias-")
	createTestUserConfig(
		t,
		"tapper-test-useralias-",
		&tapper.UserConfig{
			Aliases: map[string]tapper.KegTarget{
				"work": {
					URL:         "git@github.com:org/work-keg.git",
					PreferLocal: true,
				},
			},
			Mappings: []tapper.Mapping{
				{
					Match: tapper.MappingMatch{PathRegex: "tapper"},
					Keg:   tapper.KegTarget{Alias: "work"},
				},
			},
		})

	res, err := tapper.ResolveKegTargetForRepo(tmp)
	if err != nil {
		t.Fatalf("ResolveKegTargetForRepo returned error: %v", err)
	}

	// The resolver should expose the alias name from the user config.
	if res.Alias != "work" {
		t.Fatalf("expected Target.Alias 'work', got %q", res.Alias)
	}
}

// TestResolveKegTarget_NoneFound verifies that an empty/unknown repository
// returns an empty KegTarget with no error.
func TestResolveKegTarget_NoneFound(t *testing.T) {
	tmp := createTestRepo(t, "tapper-test-none-")

	// No KEG_CURRENT, no git config, no .tapper/local.yaml, no docs/keg present.
	res, err := tapper.ResolveKegTargetForRepo(tmp)
	if err != nil {
		t.Fatalf("ResolveKegTargetForRepo returned error: %v", err)
	}

	if !res.IsEmpty() {
		t.Fatalf("expected Target to be empty, got %+v", res)
	}
}

// TestResolveKegTarget_Precedence_EnvOverridesAll verifies precedence where the
// KEG_CURRENT environment variable should take priority over repo-local and git config.
func TestResolveKegTarget_Precedence_EnvOverridesAll(t *testing.T) {
	// Skip git steps if git not available; we still verify env precedence.
	projRoot := createTestRepo(t, "tapper-test-precedence-")
	lc := tapper.LocalConfig{
		Updated: "2025-08-14T12:00:00Z",
		Keg: tapper.KegTarget{
			Alias: "repo-local",
		},
	}
	if err := lc.WriteLocalFile(projRoot); err != nil {
		t.Fatalf("failed to write repo-local .tapper/local.yaml: %v", err)
	}

	// Initialize git repo and set a git config if git available (even lower precedence)
	getInit(t, projRoot)
	gitSetKegAlias(t, projRoot, "git-target")

	// Set KEG_CURRENT which should win.
	const envVal = "explicit"
	if err := os.Setenv("KEG_CURRENT", envVal); err != nil {
		t.Fatalf("failed to set KEG_CURRENT: %v", err)
	}
	defer os.Unsetenv("KEG_CURRENT")

	res, err := tapper.ResolveKegTargetForRepo(projRoot)
	if err != nil {
		t.Fatalf("ResolveKegTargetForRepo returned error: %v", err)
	}

	if res.Value != envVal {
		t.Fatalf("expected env Value %q to override others, got %q", envVal, res.Value)
	}
	if res.Source != "env" {
		t.Fatalf("expected Source 'env' when KEG_CURRENT is set, got %q", res.Source)
	}
}

// TestResolveKegTarget_GitOverridesTapper verifies that when both a git-local
// `tap.keg` setting and a repo-local `.tapper/local.yaml` exist, the resolver
// returns the git-local configuration (Source == "git") and the git value.
// This test asserts the resolver's precedence behavior where the git-config
// selection is expected to take precedence in this scenario.
func TestResolveKegTarget_GitOverridesTapper(t *testing.T) {
	tmp := createTestRepo(t, "tapper-test-tapper-vs-git-")

	// Initialize a git repo (helper will skip the test if git isn't present).
	getInit(t, tmp)

	// Set a git local config tap.keg value that we expect to be selected.
	gitSetKegAlias(t, tmp, "git-target")

	// Now create .tapper/local.yaml (repo-local). Although repo-local files
	// might be considered higher precedence in other policies, this test
	// asserts that the git config is selected.
	lc := tapper.LocalConfig{
		Updated: "2025-08-14T12:00:00Z",
		Keg: tapper.KegTarget{
			Alias: "repo-local",
		},
	}
	if err := lc.WriteLocalFile(tmp); err != nil {
		t.Fatalf("failed to write repo-local .tapper/local.yaml: %v", err)
	}

	res, err := tapper.ResolveKegTargetForRepo(tmp)
	if err != nil {
		t.Fatalf("ResolveKegTargetForRepo returned error: %v", err)
	}
	if res.Source != "git" {
		t.Fatalf("expected Source to be 'git', got %q", res.Source)
	}
	// Expect the git-config value to be selected.
	if res.Value != "git-target" {
		t.Fatalf("expected Value to be 'git-target', got %q)", res.Value)
	}
}
