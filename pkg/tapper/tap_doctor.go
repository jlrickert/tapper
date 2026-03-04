package tapper

import (
	"context"
	"errors"
	"fmt"

	"github.com/jlrickert/tapper/pkg/keg"
)

// DoctorOptions configures behavior for Tap.Doctor.
type DoctorOptions struct {
	KegTargetOptions
}

// Issue represents a single problem found during a doctor check.
type Issue struct {
	Level   string // "error" or "warning"
	Kind    string // category: "tag-missing", "entity-missing", "broken-link", etc.
	NodeID  string // "" for keg-level issues
	Message string
}

// Doctor scans the resolved keg and reports health issues.
func (t *Tap) Doctor(ctx context.Context, opts DoctorOptions) ([]Issue, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return nil, fmt.Errorf("unable to open keg: %w", err)
	}

	var issues []Issue

	// 1. Config validation
	cfg, err := k.Config(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to read keg config: %w", err)
	}
	if cfg.Kegv == "" {
		issues = append(issues, Issue{Level: "warning", Kind: "config", Message: "kegv version field is missing"})
	} else if cfg.Kegv != keg.ConfigV1VersionString && cfg.Kegv != keg.ConfigV2VersionString {
		issues = append(issues, Issue{Level: "warning", Kind: "config", Message: fmt.Sprintf("unrecognized kegv version %q", cfg.Kegv)})
	}

	// 2. List all nodes and build existence set
	nodeIDs, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to list nodes: %w", err)
	}
	nodeSet := make(map[int]struct{}, len(nodeIDs))
	for _, id := range nodeIDs {
		nodeSet[id.ID] = struct{}{}
	}

	// 3. Entity validation — config entities reference existing nodes
	for name, entity := range cfg.Entities {
		entityNode := keg.NodeId{ID: entity.ID}
		exists, hasErr := k.Repo.HasNode(ctx, entityNode)
		if hasErr != nil {
			issues = append(issues, Issue{
				Level:   "error",
				Kind:    "entity-missing",
				Message: fmt.Sprintf("entity %q references node %d but check failed: %v", name, entity.ID, hasErr),
			})
		} else if !exists {
			issues = append(issues, Issue{
				Level:   "error",
				Kind:    "entity-missing",
				Message: fmt.Sprintf("entity %q references node %d which does not exist", name, entity.ID),
			})
		}
	}

	// Build reverse map: entity node ID -> entity name for later per-node checks
	entityByNodeID := make(map[int]string, len(cfg.Entities))
	for name, entity := range cfg.Entities {
		entityByNodeID[entity.ID] = name
	}

	// Build tag set from config
	configTags := make(map[string]struct{}, len(cfg.Tags))
	for tag := range cfg.Tags {
		configTags[tag] = struct{}{}
	}

	// 4. Per-node checks
	for _, id := range nodeIDs {
		nodePath := id.Path()

		// Content check
		rawContent, contentErr := k.Repo.ReadContent(ctx, id)
		if contentErr != nil {
			if errors.Is(contentErr, keg.ErrNotExist) {
				issues = append(issues, Issue{Level: "error", Kind: "content", NodeID: nodePath, Message: "missing content (README.md)"})
			} else {
				issues = append(issues, Issue{Level: "error", Kind: "content", NodeID: nodePath, Message: fmt.Sprintf("unable to read content: %v", contentErr)})
			}
		} else if len(rawContent) == 0 {
			issues = append(issues, Issue{Level: "warning", Kind: "content", NodeID: nodePath, Message: "content is empty"})
		} else {
			content, parseErr := keg.ParseContent(k.Runtime, rawContent, keg.MarkdownContentFilename)
			if parseErr != nil {
				issues = append(issues, Issue{Level: "error", Kind: "content", NodeID: nodePath, Message: fmt.Sprintf("unable to parse content: %v", parseErr)})
			} else {
				if content.Title == "" {
					issues = append(issues, Issue{Level: "warning", Kind: "content", NodeID: nodePath, Message: "content has no title (H1 heading)"})
				}
				if content.Lead == "" {
					issues = append(issues, Issue{Level: "warning", Kind: "content", NodeID: nodePath, Message: "content has no lead paragraph"})
				}
				// Broken link check
				for _, link := range content.Links {
					if _, ok := nodeSet[link.ID]; !ok {
						issues = append(issues, Issue{Level: "error", Kind: "broken-link", NodeID: nodePath, Message: fmt.Sprintf("broken link to node %s", link.Path())})
					}
				}
			}
		}

		// Meta check
		rawMeta, metaErr := k.Repo.ReadMeta(ctx, id)
		if metaErr != nil && !errors.Is(metaErr, keg.ErrNotExist) {
			issues = append(issues, Issue{Level: "error", Kind: "meta", NodeID: nodePath, Message: fmt.Sprintf("unable to read metadata: %v", metaErr)})
		} else if metaErr == nil {
			meta, parseErr := keg.ParseMeta(ctx, rawMeta)
			if parseErr != nil {
				issues = append(issues, Issue{Level: "error", Kind: "meta", NodeID: nodePath, Message: fmt.Sprintf("unable to parse metadata: %v", parseErr)})
			} else {
				// Entity attr check: node declares entity not in config
				if entityVal, ok := meta.Get("entity"); ok && entityVal != "" {
					if _, inCfg := cfg.Entities[entityVal]; !inCfg {
						issues = append(issues, Issue{Level: "warning", Kind: "entity-attr", NodeID: nodePath, Message: fmt.Sprintf("entity attribute %q not defined in keg config", entityVal)})
					}
				}

				// Tag check: tags used but not in config
				for _, tag := range meta.Tags() {
					if _, inCfg := configTags[tag]; !inCfg {
						issues = append(issues, Issue{Level: "warning", Kind: "tag-missing", NodeID: nodePath, Message: fmt.Sprintf("tag %q not documented in keg config", tag)})
					}
				}
			}
		}

		// Stats check
		stats, statsErr := k.Repo.ReadStats(ctx, id)
		if statsErr != nil && !errors.Is(statsErr, keg.ErrNotExist) {
			issues = append(issues, Issue{Level: "error", Kind: "stats", NodeID: nodePath, Message: fmt.Sprintf("unable to read stats: %v", statsErr)})
		} else if statsErr == nil {
			if stats.Updated().IsZero() {
				issues = append(issues, Issue{Level: "warning", Kind: "timestamp", NodeID: nodePath, Message: "zero updated timestamp"})
			}
			if stats.Created().IsZero() {
				issues = append(issues, Issue{Level: "warning", Kind: "timestamp", NodeID: nodePath, Message: "zero created timestamp"})
			}
		}
	}

	return issues, nil
}
