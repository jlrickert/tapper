package keg

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

// ListViewOptions configures a fully server-resolved listing page.
//
// Filtering, ordering, and paging all happen before field projection, so a
// listing that displays metadata reads only the rows it returns rather than
// every node in the keg.
type ListViewOptions struct {
	// Query is an optional boolean query expression filtering the nodes.
	Query string `json:"query,omitempty"`

	// TitleContains further narrows the result to titles containing this
	// text, case-insensitively. It is a plain substring rather than an
	// expression, for the common "I half-remember the name" search, and it
	// costs nothing because titles are already carried by the index. Applying
	// it here rather than in the caller keeps paging and TotalMatches correct.
	TitleContains string `json:"title_contains,omitempty"`

	// Fields are field selectors to resolve per row, in the vocabulary of
	// ParseFieldSelector ("type", ".omega", "tags"). Intrinsics and index
	// timestamps cost nothing; other selectors are read per returned row.
	Fields []string `json:"fields,omitempty"`

	// Sort is the field selector to order by. Empty orders by node id.
	Sort string `json:"sort,omitempty"`

	// Desc reverses the sort order.
	Desc bool `json:"desc,omitempty"`

	// Limit caps the returned rows. 0 means no limit.
	Limit int `json:"limit,omitempty"`

	// Offset skips the first N matching rows before applying Limit.
	Offset int `json:"offset,omitempty"`
}

// ListViewRow is one resolved listing row: its index entry plus the values of
// the requested field selectors, keyed by selector text.
type ListViewRow struct {
	Entry  NodeIndexEntry    `json:"entry"`
	Fields map[string]string `json:"fields,omitempty"`
}

// ListViewResult is a resolved listing page. TotalMatches counts the rows the
// query selected before Limit and Offset were applied, so callers can page
// without re-running the query.
type ListViewResult struct {
	Query        string        `json:"query,omitempty"`
	Rows         []ListViewRow `json:"rows"`
	Tags         []string      `json:"tags"`
	TotalMatches int           `json:"total_matches"`
	IndexedCount int           `json:"indexed_count"`
	NodeCount    int           `json:"node_count"`
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

type KegInfo struct {
	Settings *Settings   `json:"settings"`
	Summary  *KegSummary `json:"summary"`
}

type DoctorIssue struct {
	Level   string `json:"level" yaml:"level"`
	Kind    string `json:"kind" yaml:"kind"`
	NodeID  string `json:"node_id,omitempty" yaml:"node_id,omitempty"`
	Message string `json:"message" yaml:"message"`
}

type RemoveNodesOptions struct {
	Nodes []NodeRemoveOptions `json:"nodes,omitempty"`
	Query string              `json:"query,omitempty"`
}

type SettingsWriteOptions struct {
	ExpectedHash string `json:"expected_hash,omitempty"`
}

type SchemaWriteOptions struct {
	ExpectedHash string `json:"expected_hash,omitempty"`
}

type NodeMoveOptions struct {
	Source       NodeId `json:"source"`
	Destination  NodeId `json:"destination"`
	ExpectedHash string `json:"expected_hash,omitempty"`
}

type NodeRemoveOptions struct {
	ID           NodeId `json:"id"`
	ExpectedHash string `json:"expected_hash,omitempty"`
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
	NodeID         NodeId `json:"node_id"`
	Code           string `json:"code"`
	Status         int    `json:"status"`
	Message        string `json:"message"`
	CurrentHash    string `json:"current_hash,omitempty"`
	CurrentContent []byte `json:"current_content,omitempty"`
}

func newBatchFailure(id NodeId, err error) *BatchFailure {
	code, status := RemoteErrorCode(err)
	f := &BatchFailure{NodeID: id, Code: code, Status: status, Message: err.Error()}
	var conflict *PreconditionConflictError
	if errors.As(err, &conflict) {
		f.CurrentHash = conflict.CurrentHash
		f.CurrentContent = append([]byte(nil), conflict.CurrentContent...)
	}
	return f
}

func (f *BatchFailure) Err() error {
	if f == nil {
		return nil
	}
	if f.Status == http.StatusPreconditionFailed {
		return &PreconditionConflictError{Resource: f.NodeID.Path(), CurrentHash: f.CurrentHash, CurrentContent: append([]byte(nil), f.CurrentContent...)}
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

const MaxMutationBatchSize = 100

type NodeCreate struct {
	Key    string `json:"key"`
	Schema string `json:"schema,omitempty"`
	// Body is the node's complete markdown content; its H1 is the title. Meta
	// is the node's complete metadata document. These are the only two inputs
	// a node is built from — there is deliberately no second, field-at-a-time
	// way to write a title, lead, tags, or attributes.
	Body []byte `json:"body,omitempty"`
	Meta []byte `json:"meta,omitempty"`
}

type CreateNodeResult struct {
	Key        string                  `json:"key"`
	ID         NodeId                  `json:"id"`
	Hash       string                  `json:"hash"`
	Validation *SchemaValidationResult `json:"validation,omitempty"`
}

type NodeOpenOptions struct {
	ID        NodeId    `json:"id"`
	Touch     bool      `json:"touch,omitempty"`
	LockToken LockToken `json:"lock_token,omitempty"`
}

type NodeUpdateOptions struct {
	ID             NodeId    `json:"id"`
	Schema         string    `json:"schema,omitempty"`
	Content        []byte    `json:"content"`
	HasContent     bool      `json:"has_content,omitempty"`
	Meta           []byte    `json:"meta,omitempty"`
	HasMeta        bool      `json:"has_meta,omitempty"`
	LockToken      LockToken `json:"lock_token,omitempty"`
	ExpectedHash   string    `json:"expected_hash,omitempty"`
	SnapshotBefore bool      `json:"snapshot_before,omitempty"`
}

type NodeUpdateResult struct {
	ID         NodeId                  `json:"id"`
	Validation *SchemaValidationResult `json:"validation,omitempty"`
	Hash       string                  `json:"hash"`
}

type NodeSnapshotRequest struct {
	ID      NodeId `json:"id"`
	Message string `json:"message,omitempty"`
}

type DexArtifacts struct {
	Indexes map[string][]byte `json:"indexes"`
}

func (k *LocalKeg) ListEntries(ctx context.Context, opts ListEntriesOptions) (*ListEntriesResult, error) {
	return withKegReadValue(ctx, k, func(ctx context.Context) (*ListEntriesResult, error) { return k.listEntries(ctx, opts) })
}

func (k *LocalKeg) ListView(ctx context.Context, opts ListViewOptions) (*ListViewResult, error) {
	return withKegReadValue(ctx, k, func(ctx context.Context) (*ListViewResult, error) { return k.listView(ctx, opts) })
}

// listView resolves a whole listing page server-side.
//
// The order of operations is what makes this cheap: filter, sort, then page,
// and only then resolve fields. Projecting after paging means a listing that
// displays metadata reads the rows it returns, not every node in the keg.
func (k *LocalKeg) listView(ctx context.Context, opts ListViewOptions) (*ListViewResult, error) {
	selectors, err := ParseFieldSelectors(opts.Fields)
	if err != nil {
		return nil, err
	}
	sortSel, err := ParseSortSelector(opts.Sort)
	if err != nil {
		return nil, err
	}

	listing, err := k.listEntries(ctx, ListEntriesOptions{Query: opts.Query})
	if err != nil {
		return nil, err
	}
	entries := listing.Entries
	if needle := strings.ToLower(strings.TrimSpace(opts.TitleContains)); needle != "" {
		kept := make([]NodeIndexEntry, 0, len(entries))
		for _, entry := range entries {
			if strings.Contains(strings.ToLower(entry.Title), needle) {
				kept = append(kept, entry)
			}
		}
		entries = kept
	}
	total := len(entries)

	// Sorting by metadata or a non-timestamp stat needs a value for every
	// matching node, not just the returned page, so it is resolved before
	// paging. Intrinsics and index timestamps stay free.
	if sortSel.Kind != FieldUnknown {
		if err := k.sortEntriesBySelector(ctx, entries, sortSel); err != nil {
			return nil, err
		}
	}
	if opts.Desc {
		slices.Reverse(entries)
	}

	if opts.Offset > 0 {
		if opts.Offset >= len(entries) {
			entries = nil
		} else {
			entries = entries[opts.Offset:]
		}
	}
	if opts.Limit > 0 && len(entries) > opts.Limit {
		entries = entries[:opts.Limit]
	}

	rows := make([]ListViewRow, 0, len(entries))
	// Values are loaded for the page as a whole, after paging, so a listing
	// that displays metadata reads one batch rather than one node at a time.
	values := k.loadFieldValues(ctx, entries, selectors)
	for _, entry := range entries {
		row := ListViewRow{Entry: entry}
		if len(selectors) > 0 {
			row.Fields = resolveRowFields(entry, selectors, values)
		}
		rows = append(rows, row)
	}

	return &ListViewResult{
		Query:        strings.TrimSpace(opts.Query),
		Rows:         rows,
		Tags:         listing.Tags,
		TotalMatches: total,
		IndexedCount: listing.IndexedCount,
		NodeCount:    listing.NodeCount,
	}, nil
}

// fieldValues holds the metadata and statistics a set of selectors needs,
// keyed by node id. A missing entry means the node had none, which callers
// render as empty.
type fieldValues struct {
	meta  map[string]*NodeMeta
	stats map[string]*NodeStats
}

// loadFieldValues fetches whatever the selectors require for every entry, in as
// few repository operations as the backend allows.
//
// This is the difference between a listing that costs two operations and one
// that costs two per row. A backend implementing RepositoryBatchRead answers
// the whole set at once; otherwise each node is read individually.
//
// Reads are best-effort throughout: a node that is indexed but unreadable
// contributes no value rather than failing the listing, because listings render
// from an index that is allowed to drift from the repository.
func (k *LocalKeg) loadFieldValues(ctx context.Context, entries []NodeIndexEntry, selectors []FieldSelector) *fieldValues {
	out := &fieldValues{}
	needMeta, needStats := SelectorNeeds(selectors)
	if !needMeta && !needStats || len(entries) == 0 {
		return out
	}

	ids := make([]NodeId, 0, len(entries))
	for _, entry := range entries {
		if id, err := ParseNode(entry.ID); err == nil && id != nil {
			ids = append(ids, *id)
		}
	}
	if len(ids) == 0 {
		return out
	}

	if batch, ok := repositoryBatchRead(k.Repo); ok {
		if needMeta {
			out.meta = make(map[string]*NodeMeta, len(ids))
			if raw, err := batch.ReadMetaBatch(ctx, ids); err == nil {
				for key, data := range raw {
					if meta, perr := ParseMeta(ctx, data); perr == nil {
						out.meta[key] = meta
					}
				}
			}
		}
		if needStats {
			if stats, err := batch.ReadStatsBatch(ctx, ids); err == nil {
				out.stats = stats
			} else {
				out.stats = map[string]*NodeStats{}
			}
		}
		return out
	}

	if needMeta {
		out.meta = make(map[string]*NodeMeta, len(ids))
	}
	if needStats {
		out.stats = make(map[string]*NodeStats, len(ids))
	}
	for _, id := range ids {
		if needMeta {
			if meta, err := k.getMeta(ctx, id); err == nil {
				out.meta[id.Path()] = meta
			}
		}
		if needStats {
			if stats, err := k.getStats(ctx, id); err == nil {
				out.stats[id.Path()] = stats
			}
		}
	}
	return out
}

// nodeKey returns the key an entry's values are stored under, or "" when the
// id cannot be parsed.
func nodeKey(entry NodeIndexEntry) string {
	id, err := ParseNode(entry.ID)
	if err != nil || id == nil {
		return ""
	}
	return id.Path()
}

// resolveRowFields renders one row's selectors from already-loaded values.
func resolveRowFields(entry NodeIndexEntry, selectors []FieldSelector, values *fieldValues) map[string]string {
	key := nodeKey(entry)
	var meta *NodeMeta
	var stats *NodeStats
	if values != nil && key != "" {
		meta = values.meta[key]
		stats = values.stats[key]
	}

	out := make(map[string]string, len(selectors))
	for _, sel := range selectors {
		out[sel.Text] = FieldValue(sel, entry, meta, stats)
	}
	return out
}

// sortEntriesBySelector orders entries in place. Intrinsic and index-timestamp
// selectors sort from values already in memory; a metadata or statistics
// selector needs a key for every entry, which is why callers sort before paging
// and why the values are loaded in one batch.
func (k *LocalKeg) sortEntriesBySelector(ctx context.Context, entries []NodeIndexEntry, sel FieldSelector) error {
	if sel.NeedsMeta() || sel.NeedsStats() {
		selectors := []FieldSelector{sel}
		values := k.loadFieldValues(ctx, entries, selectors)
		keys := make(map[string]string, len(entries))
		for _, entry := range entries {
			keys[entry.ID] = resolveRowFields(entry, selectors, values)[sel.Text]
		}
		slices.SortStableFunc(entries, func(a, b NodeIndexEntry) int {
			return strings.Compare(keys[a.ID], keys[b.ID])
		})
		return nil
	}

	switch sel.Kind {
	case FieldID:
		sortNodeIndexEntriesByID(entries)
	case FieldTitle:
		slices.SortStableFunc(entries, func(a, b NodeIndexEntry) int {
			return strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
		})
	case FieldIndexTime:
		slices.SortStableFunc(entries, func(a, b NodeIndexEntry) int {
			return entryTimeField(a, sel.Key).Compare(entryTimeField(b, sel.Key))
		})
	}
	return nil
}

func (k *LocalKeg) listEntries(ctx context.Context, opts ListEntriesOptions) (*ListEntriesResult, error) {
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
	fn := func(ctx context.Context) ([]NodeView, error) { return k.readNodes(ctx, opts) }
	if opts.Touch {
		return withKegWriteValue(ctx, k, fn)
	}
	return withKegReadValue(ctx, k, fn)
}

func (k *LocalKeg) readNodes(ctx context.Context, opts ReadNodesOptions) ([]NodeView, error) {
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
	var touchBackups []*aggregateBackup
	if opts.Touch {
		touchBackups = make([]*aggregateBackup, 0, len(ids))
		for _, id := range ids {
			view, err := k.ReadNode(ctx, id)
			if err != nil {
				if errors.Is(err, ErrNotExist) {
					return nil, fmt.Errorf("node %s not found: %w", id.Path(), err)
				}
				return nil, err
			}
			backup, err := k.captureAggregateBackup(ctx, view)
			if err != nil {
				return nil, err
			}
			touchBackups = append(touchBackups, backup)
		}
		for _, id := range ids {
			if err := k.Touch(ctx, id); err != nil {
				return nil, errors.Join(err, k.restoreTouchBackups(ctx, touchBackups))
			}
		}
	}

	views := make([]NodeView, 0, len(ids))
	for _, id := range ids {
		view, err := k.ReadNode(ctx, id)
		if err != nil {
			if errors.Is(err, ErrNotExist) {
				err = fmt.Errorf("node %s not found: %w", id.Path(), err)
			}
			if opts.Touch {
				err = errors.Join(err, k.restoreTouchBackups(ctx, touchBackups))
			}
			return nil, err
		}
		views = append(views, *view)
	}
	return views, nil
}

func (k *LocalKeg) RelatedNodes(ctx context.Context, opts RelatedNodesOptions) ([]NodeIndexEntry, error) {
	return withKegReadValue(ctx, k, func(ctx context.Context) ([]NodeIndexEntry, error) { return k.relatedNodes(ctx, opts) })
}

func (k *LocalKeg) relatedNodes(ctx context.Context, opts RelatedNodesOptions) ([]NodeIndexEntry, error) {
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

func (k *LocalKeg) Info(ctx context.Context) (*KegInfo, error) {
	return withKegReadValue(ctx, k, k.info)
}

func (k *LocalKeg) info(ctx context.Context) (*KegInfo, error) {
	cfg, err := k.Settings(ctx)
	if err != nil {
		return nil, err
	}
	summary, err := k.Summary(ctx)
	if err != nil {
		return nil, err
	}
	return &KegInfo{Settings: cfg, Summary: summary}, nil
}

func (k *LocalKeg) Doctor(ctx context.Context) ([]DoctorIssue, error) {
	return withKegReadValue(ctx, k, k.doctor)
}

func (k *LocalKeg) doctor(ctx context.Context) ([]DoctorIssue, error) {
	cfg, err := k.Settings(ctx)
	if err != nil {
		return nil, err
	}
	issues := []DoctorIssue{}
	if cfg.Kegv == "" {
		issues = append(issues, DoctorIssue{Level: "warning", Kind: "settings", Message: "kegv version field is missing"})
	} else if cfg.Kegv != SettingsV1VersionString && cfg.Kegv != SettingsV2VersionString {
		issues = append(issues, DoctorIssue{Level: "warning", Kind: "settings", Message: fmt.Sprintf("unrecognized kegv version %q", cfg.Kegv)})
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
	return withKegWriteValue(ctx, k, func(ctx context.Context) (RemoveNodesResult, error) { return k.removeNodes(ctx, opts) })
}

func (k *LocalKeg) removeNodes(ctx context.Context, opts RemoveNodesOptions) (RemoveNodesResult, error) {
	seen := map[string]NodeRemoveOptions{}
	for _, item := range opts.Nodes {
		seen[item.ID.Path()] = item
	}
	if q := strings.TrimSpace(opts.Query); q != "" {
		entries, err := k.Query(ctx, QueryOptions{Expr: q})
		if err != nil {
			return RemoveNodesResult{}, err
		}
		for _, entry := range entries {
			if id, e := ParseNode(entry.ID); e == nil && id != nil {
				if _, explicit := seen[id.Path()]; !explicit {
					view, readErr := k.ReadNode(ctx, *id)
					if readErr != nil {
						return RemoveNodesResult{}, readErr
					}
					seen[id.Path()] = NodeRemoveOptions{ID: *id, ExpectedHash: view.Hash()}
				}
			}
		}
	}
	if len(seen) == 0 {
		return RemoveNodesResult{}, fmt.Errorf("at least one node id is required: %w", ErrInvalid)
	}
	items := make([]NodeRemoveOptions, 0, len(seen))
	for _, item := range seen {
		items = append(items, item)
	}
	slices.SortFunc(items, func(a, b NodeRemoveOptions) int { return a.ID.Compare(b.ID) })
	result := RemoveNodesResult{Removed: []RemovedNode{}}
	// Preflight every item before the first mutation so a missing or stale
	// token leaves the whole requested set unchanged.
	for _, item := range items {
		view, err := k.ReadNode(ctx, item.ID)
		if err != nil {
			result.Failure = newBatchFailure(item.ID, err)
			return result, nil
		}
		if err := checkExpectedHash("node "+item.ID.Path(), item.ExpectedHash, view.Hash(), nodeRecoveryContent(view)); err != nil {
			result.Failure = newBatchFailure(item.ID, err)
			return result, nil
		}
	}
	for _, item := range items {
		rewritten, err := k.removeUnchecked(ctx, item.ID)
		if err != nil {
			result.Failure = newBatchFailure(item.ID, err)
			return result, nil
		}
		result.Removed = append(result.Removed, RemovedNode{ID: item.ID, Rewritten: rewritten})
	}
	return result, nil
}

func (k *LocalKeg) ValidateNodes(ctx context.Context, opts ValidateNodesOptions) ([]SchemaValidationResult, error) {
	return withKegReadValue(ctx, k, func(ctx context.Context) ([]SchemaValidationResult, error) { return k.validateNodes(ctx, opts) })
}

func (k *LocalKeg) validateNodes(ctx context.Context, opts ValidateNodesOptions) ([]SchemaValidationResult, error) {
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
	return k.withKegWrite(ctx, func(ctx context.Context) error { return k.createSchema(ctx, typeName, data) })
}

func (k *LocalKeg) createSchema(ctx context.Context, typeName string, data []byte) error {
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
	fn := func(ctx context.Context) (*NodeView, error) { return k.openNode(ctx, opts) }
	if opts.Touch {
		return withKegWriteValue(ctx, k, fn)
	}
	return withKegReadValue(ctx, k, fn)
}

func (k *LocalKeg) openNode(ctx context.Context, opts NodeOpenOptions) (*NodeView, error) {
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
		var backup *aggregateBackup
		if opts.Touch {
			before, err := k.ReadNode(lockCtx, opts.ID)
			if err != nil {
				return err
			}
			backup, err = k.captureAggregateBackup(lockCtx, before)
			if err != nil {
				return err
			}
			if err := k.Touch(lockCtx, opts.ID); err != nil {
				return err
			}
		}
		view, err = k.ReadNode(lockCtx, opts.ID)
		if err != nil && backup != nil {
			err = errors.Join(err, k.restoreTouchBackups(lockCtx, []*aggregateBackup{backup}))
		}
		return err
	})
	return view, err
}

func (k *LocalKeg) UpdateNode(ctx context.Context, opts NodeUpdateOptions) (*NodeUpdateResult, error) {
	opts.HasContent = true
	results, err := k.UpdateNodes(ctx, []NodeUpdateOptions{opts})
	if len(results) == 0 {
		return nil, err
	}
	return &results[0], err
}

func (k *LocalKeg) updateNode(ctx context.Context, opts NodeUpdateOptions) (*NodeUpdateResult, error) {
	var result *NodeUpdateResult
	err := k.withNodeLock(ctx, opts.ID, func(lockCtx context.Context) error {
		existing, err := k.ReadNode(lockCtx, opts.ID)
		if err != nil {
			return err
		}
		if err := k.validateAggregateLock(lockCtx, opts.ID, opts.LockToken); err != nil {
			return err
		}
		currentHash := existing.Hash()
		if err := checkExpectedHash("node "+opts.ID.Path(), opts.ExpectedHash, currentHash, nodeRecoveryContent(existing)); err != nil {
			return err
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

func (k *LocalKeg) restoreTouchBackups(ctx context.Context, backups []*aggregateBackup) error {
	var errs []error
	for _, backup := range backups {
		if backup == nil {
			continue
		}
		errs = append(errs, k.Repo.WriteMeta(ctx, backup.id, backup.meta))
		errs = append(errs, k.Repo.WriteStats(ctx, backup.id, backup.stats))
	}
	errs = append(errs, k.refreshDirtyIndex(ctx))
	return errors.Join(errs...)
}

func (k *LocalKeg) DexArtifacts(ctx context.Context) (*DexArtifacts, error) {
	// Snapshot-derived indexes are materialized lazily for repositories created
	// before those artifacts existed, so the complete projection uses the write
	// boundary while it assembles one coherent artifact generation.
	return withKegWriteValue(ctx, k, k.dexArtifacts)
}

func (k *LocalKeg) dexArtifacts(ctx context.Context) (*DexArtifacts, error) {
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
