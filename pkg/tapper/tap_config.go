package tapper

import (
	"context"
	"fmt"
	"os"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

type ConfigOptions struct {
	// Project indicates whether to display project config
	Project bool

	// User indicates whether to display user config
	User bool

	// Template prints out a templated. Combine with either project or user
	// flag. Defaults to using --user flag
	Template bool
}

// Config displays the merged or project configuration.
func (t *Tap) Config(opts ConfigOptions) (string, error) {
	var cfg *Config
	if opts.Template {
		if opts.Project {
			cfg := DefaultProjectConfig("", "")
			data, err := cfg.ToYAML()
			return string(data), err
		}
		cfg := DefaultUserConfig("", "")
		data, err := cfg.ToYAML()
		return string(data), err
	}
	if opts.Project {
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
}

// ConfigEdit opens the configuration file in the default editor.
func (t *Tap) ConfigEdit(ctx context.Context, opts ConfigEditOptions) error {
	var configPath string
	if opts.ConfigPath != "" {
		configPath = opts.ConfigPath
	} else if opts.Project {
		configPath = t.PathService.ProjectConfig()
	} else {
		configPath = t.PathService.UserConfig()
	}

	// If config doesn't exist, create a default one
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		var cfg *Config
		if opts.Project {
			cfg = DefaultProjectConfig("project", "kegs")
		} else {
			cfg = DefaultUserConfig("public", "~/Documents/kegs")
		}
		if err := cfg.Write(t.Runtime, configPath); err != nil {
			return fmt.Errorf("unable to create default config: %w", err)
		}
	}

	err := toolkit.Edit(ctx, t.Runtime, configPath)
	return err
}
