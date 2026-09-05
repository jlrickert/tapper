package keg

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

// Init initializes a new keg by creating the settings file, zero node with default
// content, and updating the dex. It returns an error if the keg already exists.
// Init is idempotent in the sense that it checks for existing kegs first.
func (k *LocalKeg) Init(ctx context.Context) error {
	return k.withKegWrite(ctx, k.init)
}

func (k *LocalKeg) init(ctx context.Context) error {
	if k == nil || k.Repo == nil {
		return fmt.Errorf("no repository configured")
	}

	// refuse to init when a keg already exists
	exists, err := RepoContainsKeg(ctx, k.Repo)
	if err != nil {
		return fmt.Errorf("failed to check keg existence: %w", err)
	}
	if exists {
		return fmt.Errorf("keg already exists: %w", ErrExist)
	}

	// Ensure we have a settings file. UpdateSettings must be allowed to write the
	// repo-level settings even when the keg is not fully initiated.
	cfg := NewSettings()
	if err := k.Repo.WriteSettings(ctx, cfg); err != nil {
		return fmt.Errorf("failed to write settings: %w", err)
	}

	// Create the zero node as a special case during InitKeg. We do this here so
	// Create can continue to require an initiated keg.
	now := k.Runtime.Clock().Now()

	rawContent := RawZeroNodeContent
	zeroContent, _ := ParseContent(k.Runtime, []byte(rawContent), MarkdownContentFilename)

	m := NewMeta(ctx, now)
	stats := NewStats(now)
	// no attrs to apply for the zero node; leave as empty map
	_ = m.SetAttrs(ctx, nil)
	nodeData := &NodeData{ID: NodeId{ID: 0}, Content: zeroContent, Meta: m, Stats: stats}
	_ = nodeData.updateMeta(ctx, k.Runtime, &now)
	nodeData.Stats.EnsureTimes(now)

	id := NodeId{ID: 0}

	if err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		if err := k.Repo.WriteContent(lockCtx, id, []byte(rawContent)); err != nil {
			return fmt.Errorf("InitKeg: write content to backend %s: %w", k.Repo.Name(), err)
		}
		if err := k.Repo.WriteMeta(lockCtx, id, []byte(m.ToYAML())); err != nil {
			return fmt.Errorf("InitKeg: write meta to backend %s: %w", k.Repo.Name(), err)
		}
		if err := k.Repo.WriteStats(lockCtx, id, stats); err != nil {
			return fmt.Errorf("InitKeg: write stats to backend %s: %w", k.Repo.Name(), err)
		}
		return nil
	}); err != nil {
		return err
	}

	nodeData.ID = id
	if err := k.writeNodeToDex(ctx, nodeData, now); err != nil {
		return fmt.Errorf("failed to index zero node: %w", err)
	}
	if err := k.refreshSnapshotGeneratedIndexes(ctx); err != nil {
		return fmt.Errorf("failed to refresh snapshot indexes: %w", err)
	}

	k.kegExistsVerified.Store(true)
	return nil
}

// CreateOptions specifies parameters for creating a new node
type CreateOptions struct {
	// Schema is the explicitly selected schema for this write.
	Schema string
	// Body is the raw markdown content; its H1 is the node's title. When
	// empty, a placeholder heading is generated from the allocated node id.
	Body []byte
	// Meta is the node's complete metadata document.
	Meta []byte
}

// Create creates a new node: allocates an ID, parses content, generates metadata,
// and indexes the node in the dex. The node is immediately persisted to the repository.
// If Body is empty, a placeholder heading is generated from the allocated node id.
func (k *LocalKeg) Create(ctx context.Context, opts *CreateOptions) (CreateResult, error) {
	if opts == nil {
		opts = &CreateOptions{}
	}
	results, err := k.CreateNodes(ctx, []NodeCreate{{Key: "node", Schema: opts.Schema, Body: opts.Body, Meta: opts.Meta}})
	if len(results) == 0 {
		return CreateResult{}, err
	}
	return CreateResult{ID: results[0].ID, Validation: results[0].Validation}, err
}

func (k *LocalKeg) create(ctx context.Context, opts *CreateOptions) (CreateResult, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return CreateResult{}, fmt.Errorf("failed to create node: %w", err)
	}

	if opts == nil {
		opts = &CreateOptions{}
	}

	now := k.Runtime.Clock().Now()

	// Reserve next ID
	id, err := k.Repo.Next(ctx)
	if err != nil {
		return CreateResult{}, fmt.Errorf("failed to allocate node id: %w", err)
	}

	// The default heading embeds the freshly-allocated id when no title/body.
	nodeData, err := k.buildNodeData(ctx, opts, now, fmt.Sprintf("NodeId %s", id.Path()))
	if err != nil {
		return CreateResult{}, err
	}
	nodeData.ID = id

	validation, err := k.validateNodeData(ctx, id, nodeData)
	if err != nil {
		_ = k.Repo.DeleteNode(ctx, id)
		return CreateResult{ID: id, Validation: validation}, err
	}
	if err := k.enforceSchemaValidationResult(ctx, schemaWriteCreate, validation); err != nil {
		_ = k.Repo.DeleteNode(ctx, id)
		return CreateResult{ID: id, Validation: validation}, err
	}
	if validation != nil && validation.Valid {
		validation = nil
	}

	// Persist content and metadata atomically for this node.
	if err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		if err := k.Repo.WriteContent(lockCtx, id, []byte(nodeData.Content.Body)); err != nil {
			return fmt.Errorf("create: write content to backend %s: %w", k.Repo.Name(), err)
		}
		if err := k.Repo.WriteMeta(lockCtx, id, []byte(nodeData.Meta.ToYAML())); err != nil {
			return fmt.Errorf("create: write meta to backend %s: %w", k.Repo.Name(), err)
		}
		if err := k.Repo.WriteStats(lockCtx, id, nodeData.Stats); err != nil {
			return fmt.Errorf("create: write stats to backend %s: %w", k.Repo.Name(), err)
		}
		return nil
	}); err != nil {
		return CreateResult{ID: id, Validation: validation}, err
	}

	if err := k.writeNodeToDex(ctx, nodeData, now); err != nil {
		return CreateResult{ID: id, Validation: validation}, err
	}
	if err := k.refreshDirtyIndex(ctx); err != nil {
		return CreateResult{ID: id, Validation: validation}, fmt.Errorf("failed to refresh dirty index: %w", err)
	}
	return CreateResult{ID: id, Validation: validation}, nil
}

// buildNodeData assembles the content/meta/stats for a new node from opts. It
// delegates to the package-level buildCreateNodeData; see that function for
// the fallbackHeading contract.
func (k *LocalKeg) buildNodeData(ctx context.Context, opts *CreateOptions, now time.Time, fallbackHeading string) (*NodeData, error) {
	return buildCreateNodeData(ctx, k.Runtime, opts, now, fallbackHeading)
}

// buildCreateNodeData assembles the content/meta/stats for a new node from
// opts. The node id is not part of the result (callers set it once known)
// except via fallbackHeading, the H1 used when opts carries no Body — local
// creates pass "NodeId <id>" there; remote creates, where the id is assigned
// by the hub, pass a generic heading.
func buildCreateNodeData(ctx context.Context, rt *toolkit.Runtime, opts *CreateOptions, now time.Time, fallbackHeading string) (*NodeData, error) {
	rawContent := opts.Body
	if len(rawContent) == 0 {
		rawContent = []byte(fmt.Sprintf("# %s\n", fallbackHeading))
	}
	if err := RejectFrontmatter(rawContent); err != nil {
		return nil, err
	}

	content, err := ParseContent(rt, rawContent, MarkdownContentFilename)
	if err != nil {
		return nil, fmt.Errorf("invalid content: %w", err)
	}

	m := NewMeta(ctx, now)
	if len(bytes.TrimSpace(opts.Meta)) > 0 {
		m, err = ParseMeta(ctx, opts.Meta)
		if err != nil {
			return nil, fmt.Errorf("invalid metadata: %s: %w", err, ErrInvalid)
		}
	}

	stats := NewStats(now)
	nodeData := &NodeData{Content: content, Meta: m, Stats: stats}
	_ = nodeData.updateMeta(ctx, rt, &now)
	nodeData.Stats.EnsureTimes(now)
	return nodeData, nil
}

// RejectFrontmatter refuses content that opens with a YAML frontmatter
// delimiter. A node is built from two separate inputs — content, the markdown
// body opening with its H1 title, and meta, the complete metadata document — so
// a frontmatter block is a second, silent way to write metadata. The failure
// mode is severe: a body that legitimately begins with a horizontal rule would
// either consume the following lines as metadata or fail deep in the parser
// with an unrelated message.
//
// This lives here rather than in the tool layer so every writer reaches the
// same rule: the REST handlers, the MCP tools, the web UI, and the tap CLI all
// pass through create and update below.
func RejectFrontmatter(content []byte) error {
	trimmed := bytes.TrimPrefix(content, []byte("\xef\xbb\xbf"))
	if !bytes.HasPrefix(trimmed, []byte("---\n")) && !bytes.HasPrefix(trimmed, []byte("---\r\n")) {
		return nil
	}
	return fmt.Errorf("content must not start with a YAML frontmatter block; send metadata in the meta field instead: %w", ErrInvalid)
}
