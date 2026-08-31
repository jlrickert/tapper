package keg

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Settings returns the keg's configuration.
func (k *LocalKeg) Settings(ctx context.Context) (*Settings, error) {
	return withKegReadValue(ctx, k, k.settings)
}

func (k *LocalKeg) settings(ctx context.Context) (*Settings, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to retrieve settings: %w", err)
	}

	if store, ok := k.Repo.(RepositorySettingsDocuments); ok {
		raw, err := store.ReadSettingsDocument(ctx)
		if err != nil {
			return nil, err
		}
		cfg, err := ParseKegSettings(raw)
		if err != nil {
			return nil, err
		}
		cfg.setDocument(raw, DocumentHash(raw))
		return cfg, nil
	}
	cfg, err := k.Repo.ReadSettings(ctx)
	if err != nil {
		return nil, err
	}
	raw, err := cfg.ToYAML()
	if err == nil {
		cfg.setDocument(raw, DocumentHash(raw))
	}
	return cfg, err
}

// UpdateSettings reads the keg settings, applies the provided mutation function,
// and writes the result back to the repository. This is the preferred way to
// modify keg settings to ensure updates are atomically persisted.
func (k *LocalKeg) UpdateSettings(ctx context.Context, f func(*Settings)) error {
	return k.withKegWrite(ctx, func(ctx context.Context) error { return k.updateSettings(ctx, f) })
}

func (k *LocalKeg) updateSettings(ctx context.Context, f func(*Settings)) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("unable to update settings: %w", err)
	}

	k.settingsMu.Lock()
	defer k.settingsMu.Unlock()

	// Read settings directly from the repository to allow InitKeg to create it when
	// the keg is not yet fully initiated.
	cfg, err := k.Repo.ReadSettings(ctx)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			cfg = NewSettings()
		} else {
			return fmt.Errorf("failed to read settings: %w", err)
		}
	}
	f(cfg)
	if err := k.Repo.WriteSettings(ctx, cfg); err != nil {
		return fmt.Errorf("failed to write settings: %w", err)
	}
	return nil
}

// SetSettings parses and writes keg settings from raw bytes.
// Prefer UpdateSettings for most use cases as it handles read-modify-write atomically.
func (k *LocalKeg) SetSettings(ctx context.Context, data []byte, opts SettingsWriteOptions) error {
	return k.withKegWrite(ctx, func(ctx context.Context) error { return k.setSettings(ctx, data, opts) })
}

func (k *LocalKeg) setSettings(ctx context.Context, data []byte, opts SettingsWriteOptions) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("unable to set settings: %w", err)
	}
	current, err := k.settings(ctx)
	if err != nil {
		return fmt.Errorf("failed to read current settings: %w", err)
	}
	if err := checkExpectedHash("settings", opts.ExpectedHash, current.Hash(), current.Raw()); err != nil {
		return err
	}
	return k.replaceSettings(ctx, data)
}

// replaceSettings is reserved for operations such as archive restore that own
// the complete keg write boundary and intentionally sit outside user edit
// preconditions.
func (k *LocalKeg) replaceSettings(ctx context.Context, data []byte) error {
	cfg, err := ParseKegSettingsStrict(data)
	if err != nil {
		return fmt.Errorf("unable to parse settings: %w", err)
	}
	if store, ok := k.Repo.(RepositorySettingsDocuments); ok {
		if err := store.WriteSettingsDocument(ctx, data); err != nil {
			return fmt.Errorf("failed to write settings: %w", err)
		}
		return nil
	}
	if err := k.Repo.WriteSettings(ctx, cfg); err != nil {
		return fmt.Errorf("failed to write settings: %w", err)
	}
	return nil
}

func (k *LocalKeg) touchSettingsUpdated(ctx context.Context, at time.Time) error {
	if at.IsZero() {
		at = k.Runtime.Clock().Now()
	}
	updated := at.Format(time.RFC3339)

	return k.UpdateSettings(ctx, func(cfg *Settings) {
		cfg.Updated = updated
	})
}

func patchSettingsUpdatedField(raw []byte, updated string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("settings root must be a mapping")
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
