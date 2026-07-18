package keg

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// ListEntriesOptions configures the server-owned listing projection.
type ListEntriesOptions struct {
	Query string `json:"query,omitempty"`
}

// ListEntriesResult contains every value needed by list and tags without
// requiring callers to assemble multiple primitive reads.
type ListEntriesResult struct {
	Query        string           `json:"query,omitempty"`
	Entries      []NodeIndexEntry `json:"entries"`
	Tags         []string         `json:"tags"`
	IndexedCount int              `json:"indexed_count"`
	NodeCount    int              `json:"node_count"`
}

type ReadNodesOptions struct {
	NodeIDs []NodeId `json:"node_ids,omitempty"`
	Query   string   `json:"query,omitempty"`
	Touch   bool     `json:"touch,omitempty"`
}

type RelatedDirection string

const (
	RelatedLinks     RelatedDirection = "links"
	RelatedBacklinks RelatedDirection = "backlinks"
)

type RelatedNodesOptions struct {
	NodeIDs   []NodeId         `json:"node_ids"`
	Direction RelatedDirection `json:"direction"`
}

type GraphNode struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Lead  string   `json:"lead,omitempty"`
	Tags  []string `json:"tags"`
}

type GraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

type GraphView struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

type KegInspection struct {
	Config  *Config     `json:"config"`
	Summary *KegSummary `json:"summary"`
}

type DoctorIssue struct {
	Level   string `json:"level" yaml:"level"`
	Kind    string `json:"kind" yaml:"kind"`
	NodeID  string `json:"node_id,omitempty" yaml:"node_id,omitempty"`
	Message string `json:"message" yaml:"message"`
}

type RemoveNodesOptions struct {
	NodeIDs []NodeId `json:"node_ids,omitempty"`
	Query   string   `json:"query,omitempty"`
}

type RemovedNode struct {
	ID        NodeId   `json:"id"`
	Rewritten []NodeId `json:"rewritten"`
}

type RemoveNodesResult struct {
	Removed []RemovedNode `json:"removed"`
	Failure *BatchFailure `json:"failure,omitempty"`
}

type BatchFailure struct {
	NodeID  NodeId `json:"node_id"`
	Code    string `json:"code"`
	Status  int    `json:"status"`
	Message string `json:"message"`
}

func newBatchFailure(id NodeId, err error) *BatchFailure {
	code, status := RemoteErrorCode(err)
	return &BatchFailure{NodeID: id, Code: code, Status: status, Message: err.Error()}
}

func (f *BatchFailure) Err() error {
	if f == nil {
		return nil
	}
	return RemoteErrorFromCode(f.Code, f.Status, f.Message)
}

type ValidateNodesOptions struct {
	NodeIDs []NodeId `json:"node_ids,omitempty"`
}

type CreateResult struct {
	ID         NodeId                  `json:"id"`
	Validation *SchemaValidationResult `json:"validation,omitempty"`
}

type NodeOpenOptions struct {
	ID        NodeId    `json:"id"`
	Touch     bool      `json:"touch,omitempty"`
	LockToken LockToken `json:"lock_token,omitempty"`
}

type NodeUpdateOptions struct {
	ID           NodeId    `json:"id"`
	Content      []byte    `json:"content"`
	Meta         []byte    `json:"meta,omitempty"`
	HasMeta      bool      `json:"has_meta,omitempty"`
	LockToken    LockToken `json:"lock_token,omitempty"`
	ExpectedHash string    `json:"expected_hash,omitempty"`
}

type NodeUpdateResult struct {
	Validation *SchemaValidationResult `json:"validation,omitempty"`
	Hash       string                  `json:"hash"`
}

type NodeRedirect struct {
	ID           NodeId `json:"id"`
	Target       string `json:"target"`
	Title        string `json:"title,omitempty"`
	TargetID     NodeId `json:"target_id"`
	ExpectedHash string `json:"expected_hash,omitempty"`
}

type ReplaceNodesWithRedirectsResult struct {
	Replaced []NodeId      `json:"replaced"`
	Failure  *BatchFailure `json:"failure,omitempty"`
}

type DexArtifacts struct {
	Indexes map[string][]byte `json:"indexes"`
}

func (k *LocalKeg) ListEntries(ctx context.Context, opts ListEntriesOptions) (*ListEntriesResult, error) {
	dex, err := k.Dex(ctx)
	if err != nil {
		return nil, err
	}
	entries := dex.Nodes(ctx)
	indexed := len(entries)
	if q := strings.TrimSpace(opts.Query); q != "" {
		entries, err = k.Query(ctx, QueryOptions{Expr: q})
		if err != nil {
			return nil, err
		}
	}
	ids, err := k.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	tags := dex.TagList(ctx)
	sort.Strings(tags)
	return &ListEntriesResult{Query: strings.TrimSpace(opts.Query), Entries: entries, Tags: tags, IndexedCount: indexed, NodeCount: len(ids)}, nil
}

func (k *LocalKeg) ReadNodes(ctx context.Context, opts ReadNodesOptions) ([]NodeView, error) {
	ids := slices.Clone(opts.NodeIDs)
	if q := strings.TrimSpace(opts.Query); q != "" {
		if len(ids) != 0 {
			return nil, fmt.Errorf("node ids and query are mutually exclusive: %w", ErrInvalid)
		}
		entries, err := k.Query(ctx, QueryOptions{Expr: q})
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			id, parseErr := ParseNode(entry.ID)
			if parseErr == nil && id != nil {
				ids = append(ids, *id)
			}
		}
	}
	views := make([]NodeView, 0, len(ids))
	for _, id := range ids {
		view, err := k.ReadNode(ctx, id)
		if err != nil {
			if errors.Is(err, ErrNotExist) {
				return nil, fmt.Errorf("node %s not found: %w", id.Path(), err)
			}
			return nil, err
		}
		if opts.Touch {
			if err := k.Touch(ctx, id); err != nil {
				return nil, err
			}
		}
		views = append(views, *view)
	}
	return views, nil
}

func (k *LocalKeg) RelatedNodes(ctx context.Context, opts RelatedNodesOptions) ([]NodeIndexEntry, error) {
	if len(opts.NodeIDs) == 0 {
		return nil, fmt.Errorf("at least one node id is required: %w", ErrInvalid)
	}
	dex, err := k.Dex(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	for _, id := range opts.NodeIDs {
		exists, err := k.NodeExists(ctx, id)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("node %s: %w", id.Path(), ErrNotExist)
		}
		var related []NodeId
		switch opts.Direction {
		case RelatedLinks:
			related, _ = dex.Links(ctx, id)
		case RelatedBacklinks:
			related, _ = dex.Backlinks(ctx, id)
		default:
			return nil, fmt.Errorf("invalid related direction %q: %w", opts.Direction, ErrInvalid)
		}
		for _, rel := range related {
			seen[rel.Path()] = struct{}{}
		}
	}
	entriesByID := map[string]NodeIndexEntry{}
	for _, entry := range dex.Nodes(ctx) {
		entriesByID[entry.ID] = entry
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { a, _ := ParseNode(ids[i]); b, _ := ParseNode(ids[j]); return a.Lt(*b) })
	out := make([]NodeIndexEntry, 0, len(ids))
	for _, id := range ids {
		if entry, ok := entriesByID[id]; ok {
			out = append(out, entry)
		} else {
			out = append(out, NodeIndexEntry{ID: id})
		}
	}
	return out, nil
}

func (k *LocalKeg) Graph(ctx context.Context) (*GraphView, error) {
	dex, err := k.Dex(ctx)
	if err != nil {
		return nil, err
	}
	entries := dex.Nodes(ctx)
	view := &GraphView{Nodes: []GraphNode{}, Edges: []GraphEdge{}}
	for _, entry := range entries {
		n := GraphNode{ID: entry.ID, Title: entry.Title, Tags: []string{}}
		id, parseErr := ParseNode(entry.ID)
		if parseErr == nil && id != nil {
			if data, readErr := k.getNodeBestEffort(ctx, *id); data != nil {
				n.Tags = slices.Clone(data.Meta.Tags())
				if data.Content != nil {
					n.Lead = data.Content.Lead
				}
				_ = readErr
			}
			if links, ok := dex.Links(ctx, *id); ok {
				for _, dst := range links {
					view.Edges = append(view.Edges, GraphEdge{Source: id.Path(), Target: dst.Path(), Type: "link"})
				}
			}
			if backlinks, ok := dex.Backlinks(ctx, *id); ok {
				for _, source := range backlinks {
					view.Edges = append(view.Edges, GraphEdge{Source: id.Path(), Target: source.Path(), Type: "backlink"})
				}
			}
		}
		sort.Strings(n.Tags)
		view.Nodes = append(view.Nodes, n)
	}
	sort.Slice(view.Edges, func(i, j int) bool {
		a, b := view.Edges[i], view.Edges[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		return a.Type < b.Type
	})
	return view, nil
}

func (k *LocalKeg) Inspect(ctx context.Context) (*KegInspection, error) {
	cfg, err := k.Config(ctx)
	if err != nil {
		return nil, err
	}
	summary, err := k.Summary(ctx)
	if err != nil {
		return nil, err
	}
	return &KegInspection{Config: cfg, Summary: summary}, nil
}

func (k *LocalKeg) Doctor(ctx context.Context) ([]DoctorIssue, error) {
	cfg, err := k.Config(ctx)
	if err != nil {
		return nil, err
	}
	issues := []DoctorIssue{}
	if cfg.Kegv == "" {
		issues = append(issues, DoctorIssue{Level: "warning", Kind: "config", Message: "kegv version field is missing"})
	} else if cfg.Kegv != ConfigV1VersionString && cfg.Kegv != ConfigV2VersionString {
		issues = append(issues, DoctorIssue{Level: "warning", Kind: "config", Message: fmt.Sprintf("unrecognized kegv version %q", cfg.Kegv)})
	}
	ids, err := k.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	exists := map[int]struct{}{}
	for _, id := range ids {
		exists[id.ID] = struct{}{}
	}
	for _, id := range ids {
		path := id.Path()
		content, contentErr := k.GetContent(ctx, id)
		if contentErr != nil {
			issues = append(issues, DoctorIssue{Level: "error", Kind: "content", NodeID: path, Message: fmt.Sprintf("unable to read content: %v", contentErr)})
		} else if len(content) == 0 {
			issues = append(issues, DoctorIssue{Level: "warning", Kind: "content", NodeID: path, Message: "content is empty"})
		} else if parsed, parseErr := ParseContent(k.Runtime, content, MarkdownContentFilename); parseErr != nil {
			issues = append(issues, DoctorIssue{Level: "error", Kind: "content", NodeID: path, Message: fmt.Sprintf("unable to parse content: %v", parseErr)})
		} else {
			if parsed.Title == "" {
				issues = append(issues, DoctorIssue{Level: "warning", Kind: "content", NodeID: path, Message: "content has no title (H1 heading)"})
			}
			if parsed.Lead == "" {
				issues = append(issues, DoctorIssue{Level: "warning", Kind: "content", NodeID: path, Message: "content has no lead paragraph"})
			}
			for _, link := range parsed.Links {
				if _, ok := exists[link.ID]; !ok {
					issues = append(issues, DoctorIssue{Level: "error", Kind: "broken-link", NodeID: path, Message: fmt.Sprintf("broken link to node %s", link.Path())})
				}
			}
		}
		rawMeta, metaErr := k.GetMetaRaw(ctx, id)
		if metaErr == nil {
			if _, e := ParseMeta(ctx, rawMeta); e != nil {
				issues = append(issues, DoctorIssue{Level: "error", Kind: "meta", NodeID: path, Message: fmt.Sprintf("unable to parse metadata: %v", e)})
			}
		} else if !errors.Is(metaErr, ErrNotExist) {
			issues = append(issues, DoctorIssue{Level: "error", Kind: "meta", NodeID: path, Message: fmt.Sprintf("unable to read metadata: %v", metaErr)})
		}
		stats, statsErr := k.GetStats(ctx, id)
		if statsErr == nil {
			if stats.Updated().IsZero() {
				issues = append(issues, DoctorIssue{Level: "warning", Kind: "timestamp", NodeID: path, Message: "zero updated timestamp"})
			}
			if stats.Created().IsZero() {
				issues = append(issues, DoctorIssue{Level: "warning", Kind: "timestamp", NodeID: path, Message: "zero created timestamp"})
			}
		} else if !errors.Is(statsErr, ErrNotExist) {
			issues = append(issues, DoctorIssue{Level: "error", Kind: "stats", NodeID: path, Message: fmt.Sprintf("unable to read stats: %v", statsErr)})
		}
		validation, validationErr := k.ValidateNode(ctx, id)
		if validationErr != nil && !errors.Is(validationErr, ErrNotSupported) {
			issues = append(issues, DoctorIssue{Level: "error", Kind: "schema", NodeID: path, Message: fmt.Sprintf("unable to validate schema: %v", validationErr)})
		} else if validation != nil && !validation.Valid {
			for _, issue := range validation.Issues {
				message := issue.Message
				if issue.Field != "" {
					message = issue.Field + ": " + message
				}
				issues = append(issues, DoctorIssue{Level: "error", Kind: "schema", NodeID: path, Message: message})
			}
		}
	}
	return issues, nil
}

func (k *LocalKeg) RemoveNodes(ctx context.Context, opts RemoveNodesOptions) (RemoveNodesResult, error) {
	seen := map[string]NodeId{}
	for _, id := range opts.NodeIDs {
		seen[id.Path()] = id
	}
	if q := strings.TrimSpace(opts.Query); q != "" {
		entries, err := k.Query(ctx, QueryOptions{Expr: q})
		if err != nil {
			return RemoveNodesResult{}, err
		}
		for _, entry := range entries {
			if id, e := ParseNode(entry.ID); e == nil && id != nil {
				seen[id.Path()] = *id
			}
		}
	}
	if len(seen) == 0 {
		return RemoveNodesResult{}, fmt.Errorf("at least one node id is required: %w", ErrInvalid)
	}
	ids := make([]NodeId, 0, len(seen))
	for _, id := range seen {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b NodeId) int { return a.Compare(b) })
	result := RemoveNodesResult{Removed: []RemovedNode{}}
	for _, id := range ids {
		rewritten, err := k.Remove(ctx, id)
		if err != nil {
			result.Failure = newBatchFailure(id, err)
			return result, nil
		}
		result.Removed = append(result.Removed, RemovedNode{ID: id, Rewritten: rewritten})
	}
	return result, nil
}

func (k *LocalKeg) ValidateNodes(ctx context.Context, opts ValidateNodesOptions) ([]SchemaValidationResult, error) {
	ids := slices.Clone(opts.NodeIDs)
	var err error
	if len(ids) == 0 {
		ids, err = k.ListNodes(ctx)
		if err != nil {
			return nil, err
		}
	}
	out := make([]SchemaValidationResult, 0, len(ids))
	for _, id := range ids {
		r, e := k.ValidateNode(ctx, id)
		if e != nil {
			return nil, e
		}
		out = append(out, *r)
	}
	return out, nil
}

func (k *LocalKeg) CreateSchema(ctx context.Context, typeName string, data []byte) error {
	store, ok := repoSchemas(k.Repo)
	if !ok {
		return ErrNotSupported
	}
	if _, err := validateSchemaDefinitionForType(typeName, data); err != nil {
		return err
	}
	if err := store.CreateSchema(ctx, typeName, data); err != nil {
		if errors.Is(err, ErrExist) {
			return fmt.Errorf("schema %q: %w", typeName, ErrExist)
		}
		return err
	}
	return nil
}

func (k *LocalKeg) OpenNode(ctx context.Context, opts NodeOpenOptions) (*NodeView, error) {
	var view *NodeView
	err := k.withNodeLock(ctx, opts.ID, func(lockCtx context.Context) error {
		exists, err := k.nodeExistsWithContent(lockCtx, opts.ID)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("node %s: %w", opts.ID.Path(), ErrNotExist)
		}
		if err := k.validateAggregateLock(lockCtx, opts.ID, opts.LockToken); err != nil {
			return err
		}
		if opts.Touch {
			if err := k.Touch(lockCtx, opts.ID); err != nil {
				return err
			}
		}
		view, err = k.ReadNode(lockCtx, opts.ID)
		return err
	})
	return view, err
}

func (k *LocalKeg) UpdateNode(ctx context.Context, opts NodeUpdateOptions) (*NodeUpdateResult, error) {
	var result *NodeUpdateResult
	err := k.withNodeLock(ctx, opts.ID, func(lockCtx context.Context) error {
		existing, err := k.ReadNode(lockCtx, opts.ID)
		if err != nil {
			return err
		}
		if err := k.validateAggregateLock(lockCtx, opts.ID, opts.LockToken); err != nil {
			return err
		}
		currentHash := ""
		if existing.Stats != nil {
			currentHash = existing.Stats.Hash()
		}
		if opts.ExpectedHash != "" && opts.ExpectedHash != currentHash {
			return fmt.Errorf("node %s changed: expected hash %q, got %q: %w", opts.ID.Path(), opts.ExpectedHash, currentHash, ErrConflict)
		}

		content, err := ParseContent(k.Runtime, opts.Content, MarkdownContentFilename)
		if err != nil {
			return fmt.Errorf("invalid content: %w", err)
		}
		metaBytes := existing.Meta
		if opts.HasMeta {
			metaBytes = opts.Meta
		}
		meta, err := ParseMeta(lockCtx, metaBytes)
		if err != nil {
			return fmt.Errorf("invalid metadata: %w", err)
		}
		stats, err := cloneNodeStats(lockCtx, existing.Stats)
		if err != nil {
			return err
		}
		if stats == nil {
			stats = &NodeStats{}
		}
		now := k.Runtime.Clock().Now()
		proposed := &NodeData{ID: opts.ID, Content: content, Meta: meta, Stats: stats}
		if err := proposed.updateMeta(lockCtx, k.Runtime, &now); err != nil {
			return fmt.Errorf("failed to derive node state: %w", err)
		}
		validation, err := k.validateNodeData(lockCtx, opts.ID, proposed)
		if err != nil && !errors.Is(err, ErrNotSupported) {
			return err
		}
		if errors.Is(err, ErrNotSupported) {
			validation = nil
		} else if err := k.enforceSchemaValidationResult(lockCtx, schemaWriteUpdate, validation); err != nil {
			return err
		}

		backup, err := k.captureAggregateBackup(lockCtx, existing)
		if err != nil {
			return err
		}
		write := func() error {
			if err := k.Repo.WriteMeta(lockCtx, opts.ID, []byte(meta.ToYAML())); err != nil {
				return fmt.Errorf("write metadata: %w", err)
			}
			if err := k.Repo.WriteContent(lockCtx, opts.ID, opts.Content); err != nil {
				return fmt.Errorf("write content: %w", err)
			}
			if err := k.Repo.WriteStats(lockCtx, opts.ID, stats); err != nil {
				return fmt.Errorf("write stats: %w", err)
			}
			if err := k.writeNodeToDex(lockCtx, proposed, now); err != nil {
				return err
			}
			return k.refreshDirtyIndex(lockCtx)
		}
		if err := write(); err != nil {
			return errors.Join(err, k.restoreAggregateBackup(lockCtx, backup))
		}
		result = &NodeUpdateResult{Validation: validation, Hash: stats.Hash()}
		return nil
	})
	return result, err
}

type aggregateBackup struct {
	id      NodeId
	content []byte
	meta    []byte
	stats   *NodeStats
}

func (k *LocalKeg) captureAggregateBackup(ctx context.Context, view *NodeView) (*aggregateBackup, error) {
	stats, err := cloneNodeStats(ctx, view.Stats)
	if err != nil {
		return nil, err
	}
	return &aggregateBackup{id: view.ID, content: cloneBytes(view.Content), meta: cloneBytes(view.Meta), stats: stats}, nil
}

func cloneNodeStats(ctx context.Context, stats *NodeStats) (*NodeStats, error) {
	if stats == nil {
		return nil, nil
	}
	raw, err := stats.ToJSON()
	if err != nil {
		return nil, err
	}
	return ParseStats(ctx, raw)
}

func (k *LocalKeg) restoreAggregateBackup(ctx context.Context, backup *aggregateBackup) error {
	if backup == nil {
		return nil
	}
	var errs []error
	errs = append(errs, k.Repo.WriteContent(ctx, backup.id, backup.content))
	errs = append(errs, k.Repo.WriteMeta(ctx, backup.id, backup.meta))
	errs = append(errs, k.Repo.WriteStats(ctx, backup.id, backup.stats))
	k.InvalidateDex()
	errs = append(errs, k.rebuildDexFromRepo(ctx))
	errs = append(errs, k.refreshDirtyIndex(ctx))
	return errors.Join(errs...)
}

func (k *LocalKeg) ReplaceNodesWithRedirects(ctx context.Context, redirects []NodeRedirect) (ReplaceNodesWithRedirectsResult, error) {
	result := ReplaceNodesWithRedirectsResult{Replaced: []NodeId{}}
	for _, redirect := range redirects {
		err := k.withNodeLock(ctx, redirect.ID, func(lockCtx context.Context) error {
			view, err := k.ReadNode(lockCtx, redirect.ID)
			if err != nil {
				return err
			}
			currentHash := ""
			if view.Stats != nil {
				currentHash = view.Stats.Hash()
			}
			if redirect.ExpectedHash != "" && redirect.ExpectedHash != currentHash {
				return fmt.Errorf("node %s changed before redirect: expected hash %q, got %q: %w", redirect.ID.Path(), redirect.ExpectedHash, currentHash, ErrConflict)
			}
			title := strings.TrimSpace(redirect.Title)
			if title == "" && view.Stats != nil {
				title = strings.TrimSpace(view.Stats.Title())
			}
			if title == "" {
				title = redirect.ID.Path()
			}
			body := fmt.Sprintf("# %s\n\nMoved to [%s/%s](%s/%s).\n", title, redirect.Target, redirect.TargetID.Path(), redirect.Target, redirect.TargetID.Path())
			return k.SetContent(lockCtx, redirect.ID, []byte(body))
		})
		if err != nil {
			result.Failure = newBatchFailure(redirect.ID, err)
			return result, nil
		}
		result.Replaced = append(result.Replaced, redirect.ID)
	}
	return result, nil
}

func (k *LocalKeg) DexArtifacts(ctx context.Context) (*DexArtifacts, error) {
	names, err := k.ListIndexes(ctx)
	if err != nil {
		return nil, err
	}
	out := &DexArtifacts{Indexes: map[string][]byte{}}
	for _, name := range names {
		data, e := k.ReadIndex(ctx, name)
		if e != nil {
			return nil, e
		}
		out.Indexes[name] = data
	}
	return out, nil
}

func (k *LocalKeg) validateAggregateLock(ctx context.Context, id NodeId, token LockToken) error {
	info, err := k.LockStatus(ctx, id)
	if errors.Is(err, ErrNotSupported) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Token != "" && info.Token != token {
		return ErrLockTokenMismatch
	}
	return nil
}
