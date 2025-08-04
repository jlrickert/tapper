package keg

// Package config provides versioned configuration management for the KEG application.
// It supports loading, parsing, converting, and accessing configuration data with
// environment variable expansion and version migration.

import (
	"fmt"
	"os"
	"reflect"

	"gopkg.in/yaml.v3"
)

const (
	ConfigV1VersionString = "2023-01"
	ConfigV2VersionString = "2025-07"
)

// ConfigV1 represents the initial version of the KEG configuration specification.
type ConfigV1 struct {
	// Kegv is the version of the specification.
	Kegv string `yaml:"kegv"`

	// Updated indicates when the keg was last indexed.
	Updated string `yaml:"updated,omitempty"`

	// Title is the title of the KEG worklog or project.
	Title string `yaml:"title,omitempty"`

	// URL is the main URL where the KEG can be found.
	URL string `yaml:"url,omitempty"`

	// Creator is the URL or identifier of the creator of the KEG.
	Creator string `yaml:"creator,omitempty"`

	// State indicates the current state of the KEG (e.g., living, archived).
	State string `yaml:"state,omitempty"`

	// Summary provides a brief description or summary of the KEG content.
	Summary string `yaml:"summary,omitempty"`

	// Indexes is a list of index entries that link to related files or nodes.
	Indexes []IndexEntry `yaml:"indexes,omitempty"`
}

// ConfigV1 represents the initial version of the KEG configuration specification.
type ConfigV2 struct {
	// Kegv is the version of the specification.
	Kegv string `yaml:"kegv"`

	// Updated indicates when the keg was last indexed.
	Updated string `yaml:"updated,omitempty"`

	// Title is the title of the KEG worklog or project.
	Title string `yaml:"title,omitempty"`

	// URL is the main URL where the KEG can be found.
	URL string `yaml:"url,omitempty"`

	// Creator is the URL or identifier of the creator of the KEG.
	Creator string `yaml:"creator,omitempty"`

	// State indicates the current state of the KEG (e.g., living, archived).
	State string `yaml:"state,omitempty"`

	// Summary provides a brief description or summary of the KEG content.
	Summary string `yaml:"summary,omitempty"`

	// Links holds a list of LinkEntry objects representing related links or references in the configuration.
	Links []LinkEntry `yaml:"links,omitempty"`

	// Indexes is a list of index entries that link to related files or nodes.
	Indexes []IndexEntry `yaml:"indexes,omitempty"`
}

type LinkEntry struct {
	Alias string `json:"alias"` // Alias for the link
	URL   string `json:"url"`   // URL of the link
}

type IndexEntry struct {
	File    string `yaml:"file"`
	Summary string `yaml:"summary"`
}

// Since there is no version 2 yet, ConfigV1 is the latest version
type Config = ConfigV2

func (c *ConfigV1) toV2() ConfigV2 {
	return ConfigV2{
		Kegv:    ConfigV2VersionString,
		Updated: c.Updated,
		Title:   c.Title,
		URL:     c.URL,
		Creator: c.Creator,
		State:   c.State,
		Summary: c.Summary,
		Indexes: c.Indexes,
		Links:   nil, // No links in v1, so empty slice or nil
	}
}

// ParseConfigData parses raw YAML config data into the latest Config version.
func ParseConfigData(data []byte) (Config, error) {
	var configV2 ConfigV2

	// Detect version by unmarshaling into a generic map
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return configV2, err
	}

	// Check for "kegv" version field
	version, ok := raw["kegv"].(string)
	if !ok {
		return configV2, fmt.Errorf("missing or invalid kegv version field")
	}

	switch version {
	case ConfigV1VersionString:
		var configV1 ConfigV1
		if err := yaml.Unmarshal(data, &configV1); err != nil {
			return configV2, err
		}
		return configV1.toV2(), nil
	case ConfigV2VersionString:
		if err := yaml.Unmarshal(data, &configV2); err != nil {
			return configV2, err
		}
	default:
		return configV2, fmt.Errorf("unsupported config version: %s", version)
	}

	return configV2, nil
}

// expandEnvRecursively recursively expands environment variables in strings and string slices.
func expandEnvRecursively(v reflect.Value) {
	if !v.IsValid() {
		return
	}

	switch v.Kind() {
	case reflect.Ptr:
		if !v.IsNil() {
			expandEnvRecursively(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if field.CanSet() {
				expandEnvRecursively(field)
			}
		}
	case reflect.String:
		if v.CanSet() {
			expanded := os.ExpandEnv(v.String())
			v.SetString(expanded)
		}
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.String {
			for i := 0; i < v.Len(); i++ {
				elem := v.Index(i)
				if elem.CanSet() {
					expanded := os.ExpandEnv(elem.String())
					elem.SetString(expanded)
				}
			}
		}
	}
}

// ExpandEnv expands environment variables in the Config fields.
func (c *Config) ExpandEnv() {
	expandEnvRecursively(reflect.ValueOf(c).Elem())

	// Additionally, expand environment variables in Links URLs if present
	for i := range c.Links {
		c.Links[i].URL = os.ExpandEnv(c.Links[i].URL)
	}
}
