package tap

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	std "github.com/jlrickert/go-std/pkg"
	"gopkg.in/yaml.v3"
)

// UserConfig represents the user's tapper configuration. We keep both a
// deserialized Go-friendly view (for quick access) and the original yaml.Node
// for in-place edits so comments and formatting are preserved when writing.
type UserConfig struct {
	DefaultKeg string            `yaml:"defaultKeg,omitempty"`
	KegMap     []KegMapEntry     `yaml:"kegMap"`
	Kegs       map[string]KegUrl `yaml:"kegs"`

	// node holds the original parsed YAML document root (document node). When
	// present we edit it directly to preserve comments and overall structure.
	node *yaml.Node
}

type KegMapEntry struct {
	Alias      string `yaml:"alias,omitempty"`
	PathPrefix string `yaml:"pathPrefix,omitempty"`
	PathRegex  string `yaml:"pathRegex,omitempty"`
}

type KegUrl struct {
	URL      string `yaml:"url,omitempty"`
	Readonly bool   `yaml:"readonly,omitempty"`
	User     string `yaml:"user,omitempty"`
	Password string `yaml:"password,omitempty"`
	Token    string `yaml:"token,omitempty"`
	TokenEnv string `yaml:"tokenEnv,omitempty"`
}

// ResolvePaths expands env vars and tildes in the provided basePath and returns
// an error only on expand failure. This is a helper to normalize inputs used in
// matching.
func ResolvePaths(ctx context.Context, basePath string) error {
	// Use std helpers to ensure same behavior as other code paths.
	_, err := std.ExpandPath(ctx, basePath)
	if err != nil {
		return fmt.Errorf("expand path: %w", err)
	}
	// No stateful changes; caller can use returned expanded path where needed.
	return nil
}

// Clone produces a deep copy of the UserConfig including the underlying
// yaml.Node so callers can safely mutate the clone.
func (uc *UserConfig) Clone(ctx context.Context) *UserConfig {
	if uc == nil {
		return nil
	}
	// Marshal the existing node (preserves comments) and parse into a new one.
	var buf bytes.Buffer
	if uc.node != nil {
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		// encode the whole document node
		_ = enc.Encode(uc.node)
		_ = enc.Close()
	} else {
		// Fall back to struct marshal (loses comments but still clones)
		b, _ := yaml.Marshal(uc)
		buf.Write(b)
	}
	nuc, _ := ParseUserConfig(ctx, buf.Bytes())
	if nuc == nil {
		// As fallback, do a shallow copy
		out := *uc
		return &out
	}
	return nuc
}

// ResolveAlias looks up the keg by alias and returns a parsed KegUrl. Returns
// nil + error when not found or parse fails.
func (uc *UserConfig) ResolveAlias(ctx context.Context, alias string) (*KegTarget, error) {
	if uc == nil {
		return nil, fmt.Errorf("no user config")
	}
	u, ok := uc.Kegs[alias]
	if !ok {
		return nil, fmt.Errorf("keg alias not found: %s", alias)
	}
	return ParseKegTarget(ctx, u.URL)
}

// ResolveKegMap chooses the appropriate keg (via alias) based on the provided
// filesystem path. Regex entries have precedence over pathPrefix. When multiple
// pathPrefix entries match, the longest prefix wins.
func (uc *UserConfig) ResolveKegMap(ctx context.Context, path string) (*KegTarget, error) {
	if uc == nil {
		return nil, fmt.Errorf("no user config")
	}
	// Expand path and make absolute/clean to compare reliably.
	val := std.ExpandEnv(ctx, path)
	abs, err := std.ExpandPath(ctx, val)
	if err != nil {
		// still try with expanded env
		abs = val
	}
	abs = filepath.Clean(abs)

	// First check regex entries (highest precedence)
	for _, m := range uc.KegMap {
		if m.PathRegex == "" {
			continue
		}
		pattern := std.ExpandEnv(ctx, m.PathRegex)
		pattern, _ = std.ExpandPath(ctx, pattern)
		ok, _ := regexp.MatchString(pattern, abs)
		if ok {
			return uc.ResolveAlias(ctx, m.Alias)
		}
	}

	// Collect prefix matches and choose the longest matching prefix.
	type match struct {
		entry KegMapEntry
		len   int
	}
	var matches []match
	for _, m := range uc.KegMap {
		if m.PathPrefix == "" {
			continue
		}
		pref := std.ExpandEnv(ctx, m.PathPrefix)
		pref, _ = std.ExpandPath(ctx, pref)
		pref = filepath.Clean(pref)
		if strings.HasPrefix(abs, pref) {
			matches = append(matches, match{entry: m, len: len(pref)})
		}
	}
	if len(matches) > 0 {
		// choose longest prefix
		sort.Slice(matches, func(i, j int) bool { return matches[i].len > matches[j].len })
		return uc.ResolveAlias(ctx, matches[0].entry.Alias)
	}

	// fallback to defaultKeg if set
	if uc.DefaultKeg != "" {
		return uc.ResolveAlias(ctx, uc.DefaultKeg)
	}

	return nil, fmt.Errorf("no keg map entry matched path: %s", path)
}

// ParseUserConfig parses raw YAML (string) into a UserConfig while preserving
// the underlying yaml.Node for comment-preserving edits.
func ParseUserConfig(ctx context.Context, raw []byte) (*UserConfig, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("failed to parse user config yaml: %w", err)
	}
	uc := &UserConfig{node: &doc}
	// doc.Content[0] should be the document's root mapping node if present.
	if len(doc.Content) > 0 {
		if err := doc.Content[0].Decode(uc); err != nil {
			// Try decoding the whole doc as a fallback
			if err2 := doc.Decode(uc); err2 != nil {
				// return uc, nil
				return nil, fmt.Errorf("failed to decode config into struct: %w", err)
			}
		}
	} else {
		// empty doc -> zero value config
		uc.KegMap = nil
		uc.Kegs = nil
	}
	return uc, nil
}

// ReadUserConfig reads the YAML file at path and returns a parsed UserConfig.
func ReadUserConfig(ctx context.Context, path string) (*UserConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read user config: %w", err)
	}
	return ParseUserConfig(ctx, b)
}

// WriteUserConfig writes the UserConfig back to path, preserving comments and
// formatting when possible. Uses AtomicWriteFile from std.
func (uc *UserConfig) WriteUserConfig(ctx context.Context, path string) error {
	if uc == nil {
		return fmt.Errorf("no user config")
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)

	// Prefer writing the original node (keeps comments). If absent, write struct.
	if uc.node != nil {
		// Ensure we encode the document node as-is.
		if err := enc.Encode(uc.node); err != nil {
			_ = enc.Close()
			return fmt.Errorf("encode yaml node: %w", err)
		}
	} else {
		if err := enc.Encode(uc); err != nil {
			_ = enc.Close()
			return fmt.Errorf("encode yaml struct: %w", err)
		}
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("close encoder: %w", err)
	}

	// Ensure parent directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdirall %s: %w", dir, err)
	}

	if err := std.AtomicWriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("atomic write: %w", err)
	}
	return nil
}

/*
   Programmatic mutation helpers

   The helpers below edit the in-memory yaml.Node directly when available so
   comments and unrelated nodes remain unchanged. When node is missing we update
   the Go struct and leave the caller to WriteUserConfig (which will marshal
   the struct).
*/

// AddOrUpdateKeg adds a new keg entry or updates the url for an existing alias.
// The change is applied to both the Go view (uc.Kegs) and the underlying yaml.Node
// when present.
func (uc *UserConfig) AddOrUpdateKeg(alias, urlRaw string) {
	if uc == nil {
		return
	}
	// Update struct view
	if uc.Kegs == nil {
		uc.Kegs = map[string]KegUrl{}
	}
	k := uc.Kegs[alias]
	k.URL = urlRaw
	uc.Kegs[alias] = k

	// If no node present, nothing more to do
	if uc.node == nil {
		return
	}

	// Ensure document root mapping exists
	root := uc.node
	if len(root.Content) == 0 {
		// create document mapping node
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = []*yaml.Node{m}
	}
	doc := root.Content[0]
	// find or create "kegs" mapping
	var kegsNode *yaml.Node
	for i := 0; i < len(doc.Content); i += 2 {
		keyNode := doc.Content[i]
		if keyNode.Value == "kegs" {
			kegsNode = doc.Content[i+1]
			break
		}
	}
	if kegsNode == nil {
		// append new mapping entry
		key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "kegs"}
		val := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		doc.Content = append(doc.Content, key, val)
		kegsNode = val
	}
	// ensure kegsNode is mapping
	if kegsNode.Kind != yaml.MappingNode {
		newMap := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		// replace in doc
		for i := 0; i < len(doc.Content); i += 2 {
			if doc.Content[i].Value == "kegs" {
				doc.Content[i+1] = newMap
				break
			}
		}
		kegsNode = newMap
	}

	// find existing alias entry
	found := false
	for i := 0; i < len(kegsNode.Content); i += 2 {
		keyNode := kegsNode.Content[i]
		if keyNode.Value == alias {
			// replace value with mapping containing url key
			newVal := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			urlKey := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "url"}
			urlVal := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: urlRaw}
			newVal.Content = append(newVal.Content, urlKey, urlVal)
			kegsNode.Content[i+1] = newVal
			found = true
			break
		}
	}
	if !found {
		// append new alias mapping
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: alias}
		valMap := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		urlKey := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "url"}
		urlVal := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: urlRaw}
		valMap.Content = append(valMap.Content, urlKey, urlVal)
		kegsNode.Content = append(kegsNode.Content, keyNode, valMap)
	}
}

// AddOrUpdateKegMapEntry adds or updates a keg map entry (prefix or regex).
// If an entry with the same alias and same path field exists it's updated.
func (uc *UserConfig) AddOrUpdateKegMapEntry(alias, pathPrefix, pathRegex string) {
	if uc == nil {
		return
	}
	// Update struct view: look for existing entry to update, otherwise append.
	updated := false
	for i := range uc.KegMap {
		m := &uc.KegMap[i]
		if m.Alias != alias {
			continue
		}
		// match on provided path field
		if pathPrefix != "" && m.PathPrefix != "" {
			m.PathPrefix = pathPrefix
			m.PathRegex = pathRegex
			updated = true
			break
		}
		if pathRegex != "" && m.PathRegex != "" {
			m.PathRegex = pathRegex
			m.PathPrefix = pathPrefix
			updated = true
			break
		}
	}
	if !updated {
		// append new entry
		uc.KegMap = append(uc.KegMap, KegMapEntry{
			Alias:      alias,
			PathPrefix: pathPrefix,
			PathRegex:  pathRegex,
		})
	}

	// If no node present, return
	if uc.node == nil {
		return
	}

	// Ensure document root mapping exists
	root := uc.node
	if len(root.Content) == 0 {
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = []*yaml.Node{m}
	}
	doc := root.Content[0]

	// find or create "kegMap" sequence node
	var kegMapNode *yaml.Node
	for i := 0; i < len(doc.Content); i += 2 {
		if doc.Content[i].Value == "kegMap" {
			kegMapNode = doc.Content[i+1]
			break
		}
	}
	if kegMapNode == nil {
		// append new sequence entry
		key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "kegMap"}
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		doc.Content = append(doc.Content, key, seq)
		kegMapNode = seq
	}
	// ensure sequence node
	if kegMapNode.Kind != yaml.SequenceNode {
		newSeq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for i := 0; i < len(doc.Content); i += 2 {
			if doc.Content[i].Value == "kegMap" {
				doc.Content[i+1] = newSeq
				break
			}
		}
		kegMapNode = newSeq
	}

	// Try to find existing entry with same alias and same path field, update it.
	found := false
	for _, item := range kegMapNode.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		var entryAlias, existingPrefix, existingRegex string
		for j := 0; j < len(item.Content); j += 2 {
			k := item.Content[j]
			v := item.Content[j+1]
			switch k.Value {
			case "alias":
				entryAlias = v.Value
			case "pathPrefix":
				existingPrefix = v.Value
			case "pathRegex":
				existingRegex = v.Value
			}
		}
		if entryAlias != alias {
			continue
		}
		// match when both alias and same path field exist
		if pathPrefix != "" && existingPrefix != "" {
			// update pathPrefix (or add if missing)
			setOrReplaceMappingKey(item, "pathPrefix", pathPrefix)
			// also update pathRegex key according to provided
			if pathRegex != "" {
				setOrReplaceMappingKey(item, "pathRegex", pathRegex)
			}
			found = true
			break
		}
		if pathRegex != "" && existingRegex != "" {
			setOrReplaceMappingKey(item, "pathRegex", pathRegex)
			if pathPrefix != "" {
				setOrReplaceMappingKey(item, "pathPrefix", pathPrefix)
			}
			found = true
			break
		}
	}
	if !found {
		// append a new mapping entry to the sequence
		newItem := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		aliasKey := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "alias"}
		aliasVal := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: alias}
		newItem.Content = append(newItem.Content, aliasKey, aliasVal)
		if pathPrefix != "" {
			kKey := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "pathPrefix"}
			kVal := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: pathPrefix}
			newItem.Content = append(newItem.Content, kKey, kVal)
		}
		if pathRegex != "" {
			kKey := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "pathRegex"}
			kVal := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: pathRegex}
			newItem.Content = append(newItem.Content, kKey, kVal)
		}
		kegMapNode.Content = append(kegMapNode.Content, newItem)
	}
}

// setOrReplaceMappingKey sets the key to value on the provided mapping node.
// If the key exists its value is replaced, otherwise the key/value are appended.
func setOrReplaceMappingKey(mapNode *yaml.Node, key, val string) {
	if mapNode == nil || mapNode.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < len(mapNode.Content); i += 2 {
		k := mapNode.Content[i]
		if k.Value == key {
			mapNode.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: val}
			return
		}
	}
	// append
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	v := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: val}
	mapNode.Content = append(mapNode.Content, k, v)
}
