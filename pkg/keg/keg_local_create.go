package keg

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

// Init initializes a new keg by creating the config file, zero node with default
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

	// Ensure we have a config file. UpdateConfig must be allowed to write the
	// repo-level config even when the keg is not fully initiated.
	cfg := NewConfig()
	if err := k.Repo.WriteConfig(ctx, cfg); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
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

// Next reserves and returns the next available node ID from the repository.
func (k *LocalKeg) Next(ctx context.Context) (NodeId, error) {
	return withKegWriteValue(ctx, k, func(ctx context.Context) (NodeId, error) {
		return k.Repo.Next(ctx)
	})
}

// CreateOptions specifies parameters for creating a new node
type CreateOptions struct {
	// Title is the human-readable title for the node
	Title string
	// Lead is a one-line summary
	Lead string
	// Tags are searchable labels for the node
	Tags []string
	// Body is the raw markdown content; if empty, default content is generated from Title/Lead
	Body []byte
	// Attrs are arbitrary key-value attributes attached to the node
	Attrs map[string]any
}

// Create creates a new node: allocates an ID, parses content, generates metadata,
// and indexes the node in the dex. The node is immediately persisted to the repository.
// If Body is empty, default markdown content is generated from Title and Lead.
func (k *LocalKeg) Create(ctx context.Context, opts *CreateOptions) (CreateResult, error) {
	return withKegWriteValue(ctx, k, func(ctx context.Context) (CreateResult, error) {
		return k.create(ctx, opts)
	})
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
// except via fallbackHeading, the H1 used when opts carries neither a Body nor
// a Title — local creates pass "NodeId <id>" there; remote creates, where the
// id is assigned by the hub, pass a generic heading.
func buildCreateNodeData(ctx context.Context, rt *toolkit.Runtime, opts *CreateOptions, now time.Time, fallbackHeading string) (*NodeData, error) {
	var rawContent []byte
	if len(opts.Body) > 0 {
		rawContent = opts.Body
	} else {
		b := strings.Builder{}
		if opts.Title != "" {
			b.WriteString(fmt.Sprintf("# %s\n", opts.Title))
		} else {
			b.WriteString(fmt.Sprintf("# %s\n", fallbackHeading))
		}
		if opts.Lead != "" {
			b.WriteString(fmt.Sprintf("\n%s\n", opts.Lead))
		}
		rawContent = []byte(b.String())
	}

	content, err := ParseContent(rt, rawContent, MarkdownContentFilename)
	if err != nil {
		return nil, fmt.Errorf("invalid content: %w", err)
	}
	m := NewMeta(ctx, now)
	if len(opts.Attrs) > 0 {
		m.SetAttrs(ctx, opts.Attrs)
	}
	stats := NewStats(now)
	if len(opts.Tags) > 0 {
		m.SetTags(opts.Tags)
	}
	nodeData := &NodeData{Content: content, Meta: m, Stats: stats}
	_ = nodeData.updateMeta(ctx, rt, &now)
	nodeData.Stats.EnsureTimes(now)
	return nodeData, nil
}
