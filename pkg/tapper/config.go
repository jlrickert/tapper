package tapper

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/jlrickert/tapper/pkg/internal"
	"gopkg.in/yaml.v3"
)

// Simple types and helpers to read/write the small tapper repo-local and user
// config formats described in the docs. This file intentionally provides a
// focused, testable subset: read precedence (env, git config, .tapper/local.yaml,
// user aliases) and atomic write helper for repo-local file.

// KegTarget describes the repo-local "keg" hint shape.
type KegTarget struct {
	Alias       string `yaml:"alias,omitempty"`
	URL         string `yaml:"url,omitempty"`
	Path        string `yaml:"path,omitempty"`
	PreferLocal bool   `yaml:"prefer_local,omitempty"`
}

// LocalFile is the structure for .tapper/local.yaml (repo-local visible override).
type LocalFile struct {
	Updated string     `yaml:"updated,omitempty"`
	Keg     *KegTarget `yaml:"keg,omitempty"`
	Note    string     `yaml:"note,omitempty"`
}

// AliasesFile is the user-level aliases file (~/.config/tapper/aliases.yaml).
type AliasesFile struct {
	Updated string               `yaml:"updated,omitempty"`
	Aliases map[string]KegTarget `yaml:"aliases,omitempty"`
}

// ResolveResult describes the resolved keg target and where it came from.
type ResolveResult struct {
	// Value is the textual value chosen (could be an alias, url, or path).
	Value string
	// Source describes which precedence source produced the value:
	// "env", "git-config", "repo-local", "project-keg", "user-alias", ""
	Source string
	// ResolvedTarget parsed/expanded target metadata when available (from repo-local or alias).
	Target *KegTarget
}

// ResolveKegTargetForRepo resolves which KEG to use for a repository following the
// precedence rules described in the docs.
// Precedence (highest -> lowest):
// 1. KEG_CURRENT env var (returned as-is)
// 2. git local config key `tap.keg` (if repoRoot is inside a git repo)
// 3. repo-local file: <repoRoot>/.tapper/local.yaml
// 4. project keg file (docs/keg or ./keg) - we return path if present
// 5. user aliases (~/.config/tapper/aliases.yaml) - resolves alias -> url/path
// If nothing is found an empty ResolveResult is returned (no error).
func ResolveKegTargetForRepo(repoRoot string) (ResolveResult, error) {
	// 1) env
	if v := os.Getenv("KEG_CURRENT"); v != "" {
		return ResolveResult{Value: v, Source: "env"}, nil
	}

	// 2) git config --local tap.keg
	if repoRoot != "" {
		if val, err := gitLocalConfigGet(repoRoot, "tap.keg"); err == nil && val != "" {
			res := ResolveResult{Value: val, Source: "git-config"}
			err := res.expandEnv()
			return res, err
		}
	}

	// 3) repo-local .tapper/local.yaml
	if repoRoot != "" {
		localPath := filepath.Join(repoRoot, DefaultRepoTapperDir, "local.yaml")
		if _, err := os.Stat(localPath); err == nil {
			lf, err := ReadLocalFile(localPath)
			if err == nil && lf != nil && lf.Keg != nil {
				// prefer Path if present, else URL, else Alias (return textual form)
				if lf.Keg.Path != "" {
					return ResolveResult{Value: lf.Keg.Path, Source: "repo-local", Target: lf.Keg}, nil
				}
				if lf.Keg.URL != "" {
					if err := validateNoCredentials(lf.Keg.URL); err != nil {
						return ResolveResult{}, fmt.Errorf("repo-local keg.url invalid: %w", err)
					}
					return ResolveResult{Value: lf.Keg.URL, Source: "repo-local", Target: lf.Keg}, nil
				}
				if lf.Keg.Alias != "" {
					return ResolveResult{Value: lf.Keg.Alias, Source: "repo-local", Target: lf.Keg}, nil
				}
			}
		}
	}

	// 4) project keg file (docs/keg or ./keg)
	if repoRoot != "" {
		candidates := []string{
			filepath.Join(repoRoot, "docs", "keg"),
			filepath.Join(repoRoot, "keg"),
			filepath.Join(repoRoot, "docs", "keg.yaml"),
			filepath.Join(repoRoot, "keg.yaml"),
		}
		for _, p := range candidates {
			if _, err := os.Stat(p); err == nil {
				// return the path to the keg file as the resolution (tooling can ReadConfig)
				return ResolveResult{Value: p, Source: "project-keg"}, nil
			}
		}
	}

	// 5) user aliases file
	if af, err := ReadUserAliases(ConfigAppName); err == nil && af != nil {
		// If repoRoot provided maybe there's a developer local mapping in git config
		// already checked. Here we can't know which alias to prefer; return empty unless caller asks for a named alias.
		// But a common use is that git-config or repo-local provided a token resolved earlier.
		// We return no implicit alias.
		_ = af
	}

	// nothing found
	return ResolveResult{}, nil
}

// ReadLocalFile reads and parses a .tapper/local.yaml file into LocalFile.
func ReadLocalFile(path string) (*LocalFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lf LocalFile
	if err := yaml.Unmarshal(b, &lf); err != nil {
		return nil, err
	}
	return &lf, nil
}

// WriteLocalFile writes LocalFile to repoRoot/.tapper/local.yaml atomically.
// It will create the .tapper directory if needed.
func WriteLocalFile(repoRoot string, lf *LocalFile) (string, error) {
	if repoRoot == "" {
		return "", fmt.Errorf("repoRoot required")
	}
	dir := filepath.Join(repoRoot, DefaultRepoTapperDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	outPath := filepath.Join(dir, "local.yaml")
	// ensure updated timestamp if not provided
	if lf.Updated == "" {
		lf.Updated = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := yaml.Marshal(lf)
	if err != nil {
		return "", err
	}
	// atomic write using temp file in same dir
	tmp := outPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return "", err
	}
	// best-effort fsync not implemented here (os.Rename is atomic on POSIX)
	if err := os.Rename(tmp, outPath); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return outPath, nil
}

// ReadUserAliases reads the user aliases file following XDG conventions:
// $XDG_CONFIG_HOME/tapper/aliases.yaml or ~/.config/tapper/aliases.yaml
func ReadUserAliases(appName string) (*AliasesFile, error) {
	cfgDir, err := internal.GetConfigDir(appName)
	path := filepath.Join(cfgDir, "aliases.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var af AliasesFile
	if err := yaml.Unmarshal(b, &af); err != nil {
		return nil, err
	}
	return &af, nil
}

// ResolveAlias looks up an alias in the provided AliasesFile and returns a resolved
// textual value (prefer path, then url). It validates URL credentials.
func ResolveAlias(af *AliasesFile, alias string) (string, *KegTarget, error) {
	if af == nil {
		return "", nil, fmt.Errorf("aliases file nil")
	}
	kt, ok := af.Aliases[alias]
	if !ok {
		// try case-insensitive lookup
		for k, v := range af.Aliases {
			if k == alias {
				kt = v
				ok = true
				break
			}
		}
	}
	if !ok {
		return "", nil, fmt.Errorf("alias %q not found", alias)
	}
	// prefer Path, then URL, then alias name
	if kt.Path != "" {
		return kt.Path, &kt, nil
	}
	if kt.URL != "" {
		if err := validateNoCredentials(kt.URL); err != nil {
			return "", nil, err
		}
		return kt.URL, &kt, nil
	}
	// fallback: return alias as textual
	return kt.Alias, &kt, nil
}

// gitLocalConfigGet attempts to run `git -C repoRoot config --local --get key`.
// If git isn't present or command fails it returns an error.
func gitLocalConfigGet(repoRoot, key string) (string, error) {
	// check git exists
	if _, err := exec.LookPath("git"); err != nil {
		return "", err
	}
	cmd := exec.Command("git", "-C", repoRoot, "config", "--local", "--get", key)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(out.Bytes())), nil
}

// validateNoCredentials ensures the provided URL string does not include embedded credentials.
func validateNoCredentials(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		// if not a URL (e.g., file path), treat as okay
		return nil
	}
	if u.User != nil {
		return ErrCredentialInURL
	}
	return nil
}

// expandEnv expands environment variables in the resolved target fields.
//
// It replaces any ${VAR} or $VAR occurrences in Target.URL, Target.Path and
// Target.Alias using os.ExpandEnv. If the receiver or Target is nil this is a
// no-op. The function returns an error to match callers that expect an error
// return, but currently it always returns nil.
func (res *ResolveResult) expandEnv() error {
	if res == nil || res.Target == nil {
		return nil
	}
	res.Target.URL = os.ExpandEnv(res.Target.URL)
	res.Target.Path = os.ExpandEnv(res.Target.Path)
	res.Target.Alias = os.ExpandEnv(res.Target.Alias)
	return nil
}
