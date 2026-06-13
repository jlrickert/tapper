package keg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Config returns the keg's configuration.
func (k *LocalKeg) Config(ctx context.Context) (*Config, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to retrieve config: %w", err)
	}

	return k.Repo.ReadConfig(ctx)
}

// UpdateConfig reads the keg config, applies the provided mutation function,
// and writes the result back to the repository. This is the preferred way to
// modify keg configuration to ensure updates are atomically persisted.
func (k *LocalKeg) UpdateConfig(ctx context.Context, f func(*Config)) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("unable to update config: %w", err)
	}

	k.configMu.Lock()
	defer k.configMu.Unlock()

	// Read config directly from the repository to allow InitKeg to create it when
	// the keg is not yet fully initiated.
	cfg, err := k.Repo.ReadConfig(ctx)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			cfg = NewConfig()
		} else {
			return fmt.Errorf("failed to read config: %w", err)
		}
	}
	f(cfg)
	if err := k.Repo.WriteConfig(ctx, cfg); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	return nil
}

// SetConfig parses and writes keg configuration from raw bytes.
// Prefer UpdateConfig for most use cases as it handles read-modify-write atomically.
func (k *LocalKeg) SetConfig(ctx context.Context, data []byte) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("unable to set config: %w", err)
	}
	cfg, err := ParseKegConfigStrict(data)
	if err != nil {
		return fmt.Errorf("unable to parse config: %w", err)
	}
	if err := k.Repo.WriteConfig(ctx, cfg); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	return nil
}

func (k *LocalKeg) touchConfigUpdated(ctx context.Context, at time.Time) error {
	if at.IsZero() {
		at = k.Runtime.Clock().Now()
	}
	updated := at.Format(time.RFC3339)

	if fsRepo, ok := k.Repo.(*FsRepo); ok {
		return fsRepoTouchConfigUpdated(fsRepo, updated)
	}

	return k.UpdateConfig(ctx, func(cfg *Config) {
		cfg.Updated = updated
	})
}

func fsRepoTouchConfigUpdated(repo *FsRepo, updated string) error {
	configPath, raw, err := fsRepoReadRawConfig(repo)
	if err != nil {
		return err
	}

	patched, err := patchConfigUpdatedField(raw, updated)
	if err != nil {
		return fmt.Errorf("failed to patch config timestamp: %w", err)
	}

	if bytes.Equal(raw, patched) {
		return nil
	}
	if err := repo.runtime.AtomicWriteFile(configPath, patched, 0o644); err != nil {
		return NewBackendError(repo.Name(), "WriteConfig", 0, err, false)
	}
	return nil
}

func fsRepoReadRawConfig(repo *FsRepo) (string, []byte, error) {
	candidates := []string{"keg", "keg.yaml", "keg.yml"}
	for _, candidate := range candidates {
		path := filepath.Join(repo.Root, candidate)
		if _, err := repo.runtime.Stat(path, false); err == nil {
			b, readErr := repo.runtime.ReadFile(path)
			if readErr != nil {
				return "", nil, NewBackendError(repo.Name(), "ReadConfig", 0, readErr, false)
			}
			return path, b, nil
		}
	}
	return "", nil, ErrNotExist
}

func patchConfigUpdatedField(raw []byte, updated string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config root must be a mapping")
	}

	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		if key.Kind == yaml.ScalarNode && key.Value == "updated" {
			val := root.Content[i+1]
			val.Kind = yaml.ScalarNode
			val.Tag = "!!str"
			val.Style = 0
			val.Value = updated
			return yaml.Marshal(&doc)
		}
	}

	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "updated"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: updated},
	)
	return yaml.Marshal(&doc)
}
