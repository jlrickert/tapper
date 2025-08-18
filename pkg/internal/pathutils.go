package internal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// GetConfigDir returns the default configuration directory path for the given
// appName on the current operating system.
//
// Behavior:
//   - Windows: if the APPDATA environment variable is set, returns
//     APPDATA\<appName>. If APPDATA is not set, an error is returned.
//   - Unix-like systems: if XDG_CONFIG_HOME is set, returns
//     XDG_CONFIG_HOME/<appName>. Otherwise falls back to $HOME/.config/<appName>.
//     If the user's home directory cannot be determined, an error is returned.
//
// Notes:
//   - The returned path is a suggested location and is not created by this
//     function; callers should create the directory if they need it to exist.
//   - appName should be a short directory name (no leading/trailing separators).
//   - This function does not perform validation or sanitization of appName beyond
//     joining it with the platform-specific base directory.
func GetConfigDir(appName string) (string, error) {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, appName), nil
		}
		return "", fmt.Errorf("APPDATA environment variable not set")
	}
	// Unix-like: prefer $XDG_CONFIG_HOME, fall back to ~/.config
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, appName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", appName), nil
}

// GetDataDir returns the default per-user data directory for the given appName.
//
// Behavior by platform:
//
//   - Windows: returns "%LOCALAPPDATA%\<appName>\data" when LOCALAPPDATA is set.
//     If LOCALAPPDATA is unset an error is returned to signal the missing environment.
//
//   - Unix-like (Linux, macOS, etc.): if XDG_DATA_HOME is set, returns
//     "$XDG_DATA_HOME/<appName>". Otherwise falls back to "$HOME/.local/share/<appName>".
//     If the user's home directory cannot be determined an error is returned.
//
// Notes:
//   - This function only resolves a sensible path; it does not create any directories.
//   - appName should be a short name (no path separators). Caller is responsible for
//     validating or sanitizing appName if necessary.
//
// Returns the resolved path or a non-nil error when platform-specific resolution fails.
func GetDataDir(appName string) (string, error) {
	if runtime.GOOS == "windows" {
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, appName, "data"), nil
		}
		return "", fmt.Errorf("LOCALAPPDATA environment variable not set")
	}
	// Unix-like: use $XDG_DATA_HOME or fallback to ~/.local/share
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, appName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", appName), nil
}

// GetStateDir returns the default per-user state directory for the current OS.
// On Windows this delegates to GetDataDir (state stored with the local app data).
// On Unix-like systems it uses $XDG_STATE_HOME when set, otherwise falls back to
// $HOME/.local/state. The returned path is a suggested location and is not
// created by this helper. An error is returned if the user's home directory
// cannot be determined.
func GetStateDir(appName string) (string, error) {
	if runtime.GOOS == "windows" {
		// Use same as data dir on Windows
		return GetDataDir(appName)
	}
	// Unix-like: use $XDG_STATE_HOME or fallback to ~/.local/state
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, appName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", appName), nil
}

// GetCacheDir returns the default per-user cache directory for the current OS.
// On Windows it uses %LOCALAPPDATA%\<appName>\cache when LOCALAPPDATA is set.
// On Unix-like systems it uses $XDG_CACHE_HOME when set, otherwise falls back
// to $HOME/.cache. The returned path is a suggested location and is not created
// by this helper. An error is returned if required environment variables are
// missing on Windows or if the user's home directory cannot be determined.
func GetCacheDir(appName string) (string, error) {
	if runtime.GOOS == "windows" {
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, appName, "cache"), nil
		}
		return "", fmt.Errorf("LOCALAPPDATA environment variable not set")
	}
	// Unix-like: use $XDG_CACHE_HOME or fallback to ~/.cache
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, appName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", appName), nil
}

// GetProjectRepo is the non-context convenience wrapper for GetProjectRepoContext.
// It calls the context-aware implementation with context.Background().
func GetProjectRepo() (string, error) {
	return GetProjectRepoContext(context.Background())
}

// It accepts a context for the git probe so callers can control timeouts and
// cancellation.
func GetProjectRepoContext(ctx context.Context) (string, error) {
	// Prefer asking the git binary for the repository root when available.
	// This is the most accurate method (handles worktrees, submodules, etc.).
	// Use the provided context so callers can cancel or set a deadline.
	if gitPath, err := exec.LookPath("git"); err == nil {
		// Use a short timeout derived from the provided context if no deadline is set.
		// If ctx already has a deadline, use it as-is.
		cctx := ctx
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			cctx, cancel = context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
		}

		// git rev-parse --show-toplevel prints the absolute path to the repo root.
		cmd := exec.CommandContext(cctx, gitPath, "rev-parse", "--show-toplevel")
		out, err := cmd.Output()
		if err == nil {
			if p := strings.TrimSpace(string(out)); p != "" {
				return p, nil
			}
			// If git returned empty output for some reason, fall through to FS fallback.
		}
		// If git failed (non-zero exit), don't treat it as a hard error here —
		// fall back to the filesystem-based detection below.
	}

	// Fallback: walk up from the current working directory looking for a .git entry.
	// This handles repositories where git isn't on PATH or when we don't want to
	// invoke external commands.
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	cur := wd
	for {
		gitPath := filepath.Join(cur, ".git")
		if _, statErr := os.Stat(gitPath); statErr == nil {
			// Found .git (could be a directory or a file pointing to a worktree).
			return cur, nil
		} else if !os.IsNotExist(statErr) {
			// Unexpected FS error; surface it.
			return "", statErr
		}

		parent := filepath.Dir(cur)
		// Reached filesystem root without finding .git.
		if parent == cur {
			return "", nil
		}
		cur = parent
	}
}
