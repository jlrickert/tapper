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

// DoctorConfig validates the tapper configuration (not keg-level) and returns
// issues. This does not require a keg to be resolved.
func (t *Tap) DoctorConfig() []Issue {
	var issues []Issue

	// Report any config load warnings.
	for _, w := range t.ConfigService.LoadWarnings {
		issues = append(issues, Issue{
			Level:   "warning",
			Kind:    "config-load",
			Message: w.Message,
		})
	}

	// Run semantic validation on the merged config.
	cfg, err := t.ConfigService.Config(true)
	if err != nil {
		issues = append(issues, Issue{
			Level:   "error",
			Kind:    "config",
			Message: fmt.Sprintf("unable to load merged config: %v", err),
		})
		return issues
	}

	for _, w := range ValidateConfig(cfg) {
		issues = append(issues, Issue{
			Level:   "warning",
			Kind:    "config-validate",
			Message: fmt.Sprintf("%s: %s", w.Field, w.Message),
		})
	}

	return issues
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
	nodeIDs, err := k.ListNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to list nodes: %w", err)
	}
	nodeSet := make(map[int]struct{}, len(nodeIDs))
	for _, id := range nodeIDs {
		nodeSet[id.ID] = struct{}{}
	}

	// 3. Per-node checks
	for _, id := range nodeIDs {
		nodePath := id.Path()

		// Content check
		rawContent, contentErr := k.GetContent(ctx, id)
		if contentErr != nil {
			if errors.Is(contentErr, keg.ErrNotExist) {
				issues = append(issues, Issue{Level: "error", Kind: "content", NodeID: nodePath, Message: "missing content (README.md)"})
			} else {
				issues = append(issues, Issue{Level: "error", Kind: "content", NodeID: nodePath, Message: fmt.Sprintf("unable to read content: %v", contentErr)})
			}
		} else if len(rawContent) == 0 {
			issues = append(issues, Issue{Level: "warning", Kind: "content", NodeID: nodePath, Message: "content is empty"})
		} else {
			content, parseErr := keg.ParseContent(t.Runtime, rawContent, keg.MarkdownContentFilename)
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
		rawMeta, metaErr := k.GetMetaRaw(ctx, id)
		if metaErr != nil && !errors.Is(metaErr, keg.ErrNotExist) {
			issues = append(issues, Issue{Level: "error", Kind: "meta", NodeID: nodePath, Message: fmt.Sprintf("unable to read metadata: %v", metaErr)})
		} else if metaErr == nil {
			_, parseErr := keg.ParseMeta(ctx, rawMeta)
			if parseErr != nil {
				issues = append(issues, Issue{Level: "error", Kind: "meta", NodeID: nodePath, Message: fmt.Sprintf("unable to parse metadata: %v", parseErr)})
			}
		}

		// Stats check
		stats, statsErr := k.GetStats(ctx, id)
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

		// Schema check
		result, schemaErr := k.ValidateNode(ctx, id)
		if schemaErr != nil && !errors.Is(schemaErr, keg.ErrNotSupported) {
			issues = append(issues, Issue{Level: "error", Kind: "schema", NodeID: nodePath, Message: fmt.Sprintf("unable to validate schema: %v", schemaErr)})
		} else if result != nil && !result.Valid {
			for _, issue := range result.Issues {
				message := issue.Message
				if issue.Field != "" {
					message = fmt.Sprintf("%s: %s", issue.Field, issue.Message)
				}
				issues = append(issues, Issue{Level: "error", Kind: "schema", NodeID: nodePath, Message: message})
			}
		}
	}

	return issues, nil
}
