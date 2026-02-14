package tapper

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jlrickert/cli-toolkit/mylog"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"gopkg.in/yaml.v3"
)

// LocalConfig is the structure for .tapper/local.yaml (repo-local visible override).
type LocalConfig struct {
	// Default keg to use. This is an alias
	DefaultKeg string        `yaml:"defaultKeg,omitEmpty"`
	Keg        kegurl.Target `yaml:"keg,omitempty"`
}

// ReadLocalFile reads and parses a .tapper/local.yaml file into LocalConfig.
func ReadLocalFile(ctx context.Context, path string) (*LocalConfig, error) {
	lg := mylog.LoggerFromContext(ctx)
	b, err := os.ReadFile(path)
	if err != nil {
		lg.Debug("failed to read local config", "path", path, "err", err)
		return nil, fmt.Errorf("failed to read local config: %w", err)
	}
	var lf LocalConfig
	if err := yaml.Unmarshal(b, &lf); err != nil {
		lg.Error("failed to parse local config", "path", "data", string(b), path, "err", err)
		return nil, fmt.Errorf("failed to parse local config: %w", err)
	}
	lg.Info("local config read", "path", path, "config", lf)
	return &lf, nil
}

// WriteLocalFile writes LocalConfig to repoRoot/.tapper/local.yaml atomically.
// It will create the .tapper directory if needed.
func (lf *LocalConfig) WriteLocalFile(ctx context.Context, projectPath string) error {
	lg := mylog.LoggerFromContext(ctx)
	fn := ".tapper/local.yaml"
	path := filepath.Join(projectPath, ".tapper", fn)

	b, err := yaml.Marshal(lf)
	if err != nil {
		lg.Error("failed to marshal local config", "path", path, "err", err)
		return err
	}
	if err := runtimeMust().AtomicWriteFile(path, b, 0o644); err != nil {
		lg.Error("failed to write to local config", "path", path, "err", err)
		return fmt.Errorf("failed to write to local config: %w", err)
	}
	return nil
}
