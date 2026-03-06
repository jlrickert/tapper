package tapper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

type ConfigOptions struct {
	// Project indicates whether to display project config
	Project bool

	// User indicates whether to display user config
	User bool

	// ConfigPath directly selects a config file to display.
	ConfigPath string
}

// Config displays the merged or project configuration.
func (t *Tap) Config(opts ConfigOptions) (string, error) {
	if err := validateConfigSelection(opts.ConfigPath, opts.Project, opts.User); err != nil {
		return "", err
	}

	var cfg *Config
	if opts.ConfigPath != "" {
		raw, err := t.Runtime.ReadFile(opts.ConfigPath)
		if err != nil {
			return "", err
		}
		if _, err := ParseConfig(raw); err != nil {
			return "", err
		}
		return string(raw), nil
	} else if opts.Project {
		lCfg, err := t.ConfigService.ProjectConfig(false)
		if err != nil {
			return "", err
		}
		cfg = lCfg
	} else if opts.User {
		uCfg, err := t.ConfigService.UserConfig(false)
		if err != nil {
			return "", err
		}
		cfg = uCfg
	} else {
		cfg = t.ConfigService.Config(true)
	}

	data, err := cfg.ToYAML()
	if err != nil {
		return "", fmt.Errorf("unable to serialize config: %w", err)
	}

	return string(data), nil
}

// ConfigEditOptions configures behavior for Tap.ConfigEdit.
type ConfigEditOptions struct {
	// Project indicates whether to edit local config instead of user config
	Project bool

	User bool

	ConfigPath string

	Stream *toolkit.Stream
}

type ConfigTemplateOptions struct {
	Project bool
}

func validateConfigSelection(configPath string, project, user bool) error {
	switch {
	case project && user:
		return fmt.Errorf("--user and --project cannot be combined")
	case configPath != "" && (project || user):
		return fmt.Errorf("--config cannot be combined with --user or --project")
	default:
		return nil
	}
}

// ConfigTemplate returns starter YAML for either user or project config.
func (t *Tap) ConfigTemplate(opts ConfigTemplateOptions) (string, error) {
	var cfg *Config
	if opts.Project {
		cfg = DefaultProjectConfig("project", "kegs")
	} else {
		cfg = DefaultUserConfig("pub", defaultUserKegSearchPath(t.Runtime))
	}
	data, err := cfg.ToYAML()
	return string(data), err
}

// ConfigEdit edits the selected tap config file.
//
// If stdin is piped with non-empty content, the piped YAML is validated and
// written directly without opening an editor. Otherwise the file is opened in
// the configured editor.
func (t *Tap) ConfigEdit(ctx context.Context, opts ConfigEditOptions) error {
	if err := validateConfigSelection(opts.ConfigPath, opts.Project, opts.User); err != nil {
		return err
	}

	var configPath string
	if opts.ConfigPath != "" {
		configPath = opts.ConfigPath
	} else if opts.Project {
		configPath = t.PathService.ProjectConfig()
	} else {
		configPath = t.PathService.UserConfig()
	}

	resolvedPath, err := t.Runtime.ResolvePath(configPath, false)
	if err != nil {
		return fmt.Errorf("unable to resolve config path: %w", err)
	}

	// If config doesn't exist, create a default one.
	if _, err := t.Runtime.ReadFile(resolvedPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("unable to inspect config file: %w", err)
		}
		var cfg *Config
		if opts.Project {
			cfg = DefaultProjectConfig("project", "kegs")
		} else {
			cfg = DefaultUserConfig("public", defaultUserKegSearchPath(t.Runtime))
		}
		if err := cfg.Write(t.Runtime, resolvedPath); err != nil {
			return fmt.Errorf("unable to create default config: %w", err)
		}
	}

	originalRaw, err := t.Runtime.ReadFile(resolvedPath)
	if err != nil {
		return fmt.Errorf("unable to read config file: %w", err)
	}

	saveConfig := func(data []byte) error {
		if err := t.Runtime.AtomicWriteFile(resolvedPath, data, 0o644); err != nil {
			return fmt.Errorf("unable to save edited config: %w", err)
		}
		return nil
	}

	if opts.Stream != nil && opts.Stream.IsPiped {
		pipedRaw, readErr := io.ReadAll(opts.Stream.In)
		if readErr != nil {
			return fmt.Errorf("unable to read piped input: %w", readErr)
		}
		if len(bytes.TrimSpace(pipedRaw)) > 0 {
			if bytes.Equal(pipedRaw, originalRaw) {
				return nil
			}
			if _, parseErr := ParseConfig(pipedRaw); parseErr != nil {
				return fmt.Errorf("tap config from stdin is invalid: %w", parseErr)
			}
			return saveConfig(pipedRaw)
		}
	}

	if err := editWithLiveSaves(ctx, t.Runtime, resolvedPath, func(editedRaw []byte) error {
		if _, err := ParseConfig(editedRaw); err != nil {
			return fmt.Errorf("tap config is invalid after editing: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("unable to edit tap config: %w", err)
	}
	return nil
}

func defaultUserKegSearchPath(rt *toolkit.Runtime) string {
	switch runtime.GOOS {
	case "darwin", "linux":
		return "~/Documents/kegs"
	default:
		if rt != nil {
			if home, err := rt.GetHome(); err == nil && strings.TrimSpace(home) != "" {
				return filepath.Join(home, "Documents", "kegs")
			}
		}
		return "~/Documents/kegs"
	}
}
