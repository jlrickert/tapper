package tapper

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
)

// ListSortType controls the ordering of listed nodes.
type ListSortType string

const (
	SortByDefault  ListSortType = ""         // default: same as SortByID
	SortByID       ListSortType = "id"       // ascending node ID
	SortByUpdated  ListSortType = "updated"  // ascending by last-updated timestamp
	SortByCreated  ListSortType = "created"  // ascending by creation timestamp
	SortByAccessed ListSortType = "accessed" // ascending by last-accessed timestamp
)

type ListOptions struct {
	KegTargetOptions

	// Query is an optional boolean expression that filters nodes. Supports both
	// plain tag names ("golang") and key=value attribute predicates
	// ("entity=plan"). When empty, all nodes are listed.
	Query string

	// Format is the output template. Legacy verbs %i (id), %t (title),
	// %d (updated), %c (created), %a (accessed) remain supported, and %%
	// renders a literal percent. Named selectors use %{...}: a bare word
	// names a metadata key (%{type}), a leading dot names a statistics field
	// (%{.accessCount}), and %{tags} is the tag list. Selectors other than
	// id, title, and the three dates cost one read per node.
	Format string

	IdOnly bool

	Reverse bool

	// Sort selects the sort order. Empty string means sort by node ID (default).
	Sort ListSortType

	// Limit caps the number of results returned. 0 means no limit.
	Limit int

	// Offset skips the first N results before applying limit. Must be >= 0.
	Offset int
}

type BacklinksOptions struct {
	KegTargetOptions

	// NodeIDs are the target nodes to inspect incoming links for.
	// Results from all node IDs are merged and deduplicated.
	NodeIDs []string

	// Format is the output template. Legacy verbs %i (id), %t (title),
	// %d (updated), %c (created), %a (accessed) remain supported, and %%
	// renders a literal percent. Named selectors use %{...}: a bare word
	// names a metadata key (%{type}), a leading dot names a statistics field
	// (%{.accessCount}), and %{tags} is the tag list. Selectors other than
	// id, title, and the three dates cost one read per node.
	Format string

	IdOnly bool

	Reverse bool

	// Limit caps the number of results returned. 0 means no limit.
	Limit int

	// Offset skips the first N results before applying limit. Must be >= 0.
	Offset int
}

type LinksOptions struct {
	KegTargetOptions

	// NodeIDs are the source nodes to inspect outgoing links for.
	// Results from all node IDs are merged and deduplicated.
	NodeIDs []string

	// Format is the output template. Legacy verbs %i (id), %t (title),
	// %d (updated), %c (created), %a (accessed) remain supported, and %%
	// renders a literal percent. Named selectors use %{...}: a bare word
	// names a metadata key (%{type}), a leading dot names a statistics field
	// (%{.accessCount}), and %{tags} is the tag list. Selectors other than
	// id, title, and the three dates cost one read per node.
	Format string

	IdOnly bool

	Reverse bool

	// Limit caps the number of results returned. 0 means no limit.
	Limit int

	// Offset skips the first N results before applying limit. Must be >= 0.
	Offset int
}

type GrepOptions struct {
	KegTargetOptions

	// Query is the regex pattern used to search nodes.
	Query string

	// Format is the output template. Legacy verbs %i (id), %t (title),
	// %d (updated), %c (created), %a (accessed) remain supported, and %%
	// renders a literal percent. Named selectors use %{...}: a bare word
	// names a metadata key (%{type}), a leading dot names a statistics field
	// (%{.accessCount}), and %{tags} is the tag list. Selectors other than
	// id, title, and the three dates cost one read per node.
	Format string

	IdOnly bool

	Reverse bool

	// IgnoreCase enables case-insensitive regex matching.
	IgnoreCase bool

	// MaxLines caps the number of matched lines returned per node.
	// 0 means unlimited. When > 0, only the first MaxLines matching lines
	// are included per node.
	MaxLines int

	// Limit caps the number of results returned. 0 means no limit.
	Limit int

	// Offset skips the first N results before applying limit. Must be >= 0.
	Offset int
}

type TagsOptions struct {
	KegTargetOptions

	// Query is an optional boolean expression that filters nodes. Supports both
	// plain tag names ("golang") and key=value attribute predicates
	// ("entity=plan"). When empty, all tags are listed.
	Query string

	// Format is the output template. Legacy verbs %i (id), %t (title),
	// %d (updated), %c (created), %a (accessed) remain supported, and %%
	// renders a literal percent. Named selectors use %{...}: a bare word
	// names a metadata key (%{type}), a leading dot names a statistics field
	// (%{.accessCount}), and %{tags} is the tag list. Selectors other than
	// id, title, and the three dates cost one read per node.
	Format string

	IdOnly bool

	Reverse bool

	// Limit caps the number of results returned. 0 means no limit.
	Limit int

	// Offset skips the first N results before applying limit. Must be >= 0.
	Offset int
}

type grepMatch struct {
	entry keg.NodeIndexEntry
	lines []string
}

func (t *Tap) List(ctx context.Context, opts ListOptions) ([]string, error) {
	if opts.Offset < 0 {
		return []string{}, fmt.Errorf("offset must be >= 0, got %d", opts.Offset)
	}

	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return []string{}, fmt.Errorf("unable to open keg: %w", err)
	}

	sortSelector, err := listSortSelector(opts.Sort)
	if err != nil {
		return []string{}, err
	}
	compiled, err := compileListFormat(t.resolveListFormat(ctx, k, opts.Format))
	if err != nil {
		return []string{}, err
	}

	// Ask the server for the finished page. It filters, orders, pages, and
	// resolves the requested fields in one round trip, so displaying metadata
	// costs the same as displaying a title.
	view, err := k.ListView(ctx, keg.ListViewOptions{
		Query:  opts.Query,
		Fields: compiled.selectorTexts(opts.IdOnly),
		Sort:   sortSelector,
		Limit:  opts.Limit,
		Offset: opts.Offset,
	})
	switch {
	case err == nil:
		t.warnStaleIndex(view.IndexedCount, view.NodeCount)
		return renderListView(compiled, view.Rows, renderOptions{
			Format: opts.Format, IdOnly: opts.IdOnly, Reverse: opts.Reverse,
		}), nil
	case errors.Is(err, keg.ErrListViewUnsupported):
		// Hub predates the endpoint; assemble the listing here instead.
	case strings.TrimSpace(opts.Query) != "":
		return []string{}, fmt.Errorf("invalid query expression: %w", err)
	default:
		return []string{}, fmt.Errorf("unable to list keg: %w", err)
	}

	return t.listClientSide(ctx, k, opts, compiled)
}

// resolveListFormat picks the format for a listing: an explicit --format wins,
// then the keg's own listFields, then the built-in default.
//
// Reading the keg's preference means a keg whose nodes are distinguished by
// type or subkind shows those columns without every caller having to know it.
// The lookup is best-effort — a keg with no config, or an unreadable one, falls
// through to the default rather than failing the listing.
func (t *Tap) resolveListFormat(ctx context.Context, k keg.Keg, explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	cfg, err := k.Config(ctx)
	if err != nil || cfg == nil || len(cfg.ListFields) == 0 {
		return explicit
	}
	return formatFromFieldSelectors(cfg.ListFields)
}

// formatFromFieldSelectors renders a selector list as a tab-separated format
// string, so keg configuration and --format share one language.
func formatFromFieldSelectors(fields []string) string {
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		parts = append(parts, "%{"+field+"}")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\t")
}

// listSortSelector maps the CLI sort names onto field selectors.
func listSortSelector(sort ListSortType) (string, error) {
	switch sort {
	case SortByDefault, SortByID:
		return "id", nil
	case SortByUpdated:
		return ".updated", nil
	case SortByCreated:
		return ".created", nil
	case SortByAccessed:
		return ".accessed", nil
	}
	return "", fmt.Errorf("unknown sort type: %q", sort)
}

func (t *Tap) warnStaleIndex(indexed, total int) {
	gap := total - indexed
	threshold := max(total/10, 5)
	if gap >= threshold {
		t.Runtime.Logger().Warn(
			"index appears stale: run `tap index rebuild` to fix",
			"indexed", indexed,
			"on_disk", total,
			"missing", gap,
		)
	}
}

// listClientSide reproduces the listing locally for hubs that predate the
// server-resolved endpoint. It is strictly slower — field values cost a read
// per row — so it exists only to keep older deployments working.
func (t *Tap) listClientSide(ctx context.Context, k keg.Keg, opts ListOptions, compiled compiledFormat) ([]string, error) {
	listing, err := k.ListEntries(ctx, keg.ListEntriesOptions{Query: opts.Query})
	if err != nil {
		if strings.TrimSpace(opts.Query) != "" {
			return []string{}, fmt.Errorf("invalid query expression: %w", err)
		}
		return []string{}, fmt.Errorf("unable to list keg: %w", err)
	}

	entries := listing.Entries
	t.warnStaleIndex(listing.IndexedCount, listing.NodeCount)

	if strings.TrimSpace(opts.Query) != "" {
		sortNodeIndexEntries(entries)
	}

	switch opts.Sort {
	case SortByDefault, SortByID:
		// already sorted by ID from dex.Nodes() / sortNodeIndexEntries
	case SortByUpdated:
		sortNodeIndexEntriesByTime(entries, func(e keg.NodeIndexEntry) time.Time { return e.Updated })
	case SortByCreated:
		sortNodeIndexEntriesByTime(entries, func(e keg.NodeIndexEntry) time.Time { return e.Created })
	case SortByAccessed:
		sortNodeIndexEntriesByTime(entries, func(e keg.NodeIndexEntry) time.Time { return e.Accessed })
	}

	entries = applyOffset(entries, opts.Offset)

	if opts.Limit > 0 && len(entries) > opts.Limit {
		entries = entries[:opts.Limit]
	}

	return t.renderNodeEntries(ctx, k, entries, renderOptions{
		Format: opts.Format, IdOnly: opts.IdOnly, Reverse: opts.Reverse,
	})
}

func (t *Tap) Backlinks(ctx context.Context, opts BacklinksOptions) ([]string, error) {
	if opts.Offset < 0 {
		return []string{}, fmt.Errorf("offset must be >= 0, got %d", opts.Offset)
	}
	return t.resolveAndLookupLinks(ctx, relatedListOptions{
		KegTargetOptions: opts.KegTargetOptions,
		NodeIDs:          opts.NodeIDs,
		Render:           renderOptions{Format: opts.Format, IdOnly: opts.IdOnly, Reverse: opts.Reverse},
		Limit:            opts.Limit,
		Offset:           opts.Offset,
		Direction:        keg.RelatedBacklinks,
	})
}

func (t *Tap) Links(ctx context.Context, opts LinksOptions) ([]string, error) {
	if opts.Offset < 0 {
		return []string{}, fmt.Errorf("offset must be >= 0, got %d", opts.Offset)
	}
	return t.resolveAndLookupLinks(ctx, relatedListOptions{
		KegTargetOptions: opts.KegTargetOptions,
		NodeIDs:          opts.NodeIDs,
		Render:           renderOptions{Format: opts.Format, IdOnly: opts.IdOnly, Reverse: opts.Reverse},
		Limit:            opts.Limit,
		Offset:           opts.Offset,
		Direction:        keg.RelatedLinks,
	})
}

// relatedListOptions is the shared input for Backlinks and Links.
type relatedListOptions struct {
	KegTargetOptions

	// NodeIDs are the nodes whose related nodes are looked up.
	NodeIDs []string

	// Render carries the presentation knobs.
	Render renderOptions

	// Limit caps the number of results returned. 0 means no limit.
	Limit int

	// Offset skips the first N results before applying limit.
	Offset int

	// Direction selects incoming or outgoing links.
	Direction keg.RelatedDirection
}

// resolveAndLookupLinks is a shared helper for Backlinks and Links. It resolves
// the keg, validates the node IDs, calls the provided lookup function against the
// dex for each node, merges and deduplicates results, and renders the entries.
func (t *Tap) resolveAndLookupLinks(ctx context.Context, opts relatedListOptions) ([]string, error) {
	if len(opts.NodeIDs) == 0 {
		return []string{}, fmt.Errorf("at least one node ID is required")
	}

	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return []string{}, fmt.Errorf("unable to open keg: %w", err)
	}
	ids := make([]keg.NodeId, 0, len(opts.NodeIDs))
	for _, nodeID := range opts.NodeIDs {
		// Intentionally NOT routed through resolveNodeArg: a cross-keg ref would
		// produce related nodes owned by a different keg, but the dedup key
		// (rel.Path()) and the entry rendering below (dex.GetRef) both assume a
		// single owning dex. Carrying each related node's home keg through dedup,
		// sort, offset/limit, and render is a larger change than this slice
		// covers, so links/backlinks stay scoped to the current keg.
		id, err := parseNodeID(nodeID)
		if err != nil {
			return []string{}, err
		}

		ids = append(ids, id)
	}
	entries, err := k.RelatedNodes(ctx, keg.RelatedNodesOptions{NodeIDs: ids, Direction: opts.Direction})
	if err != nil {
		if strings.Contains(err.Error(), "keg not initialized") {
			return []string{}, err
		}
		if errors.Is(err, keg.ErrNotExist) {
			return []string{}, fmt.Errorf("node %s not found in %s", ids[0].Path(), describeKeg(k))
		}
		return []string{}, err
	}

	entries = applyOffset(entries, opts.Offset)

	if opts.Limit > 0 && len(entries) > opts.Limit {
		entries = entries[:opts.Limit]
	}

	return t.renderNodeEntries(ctx, k, entries, opts.Render)
}

func (t *Tap) Grep(ctx context.Context, opts GrepOptions) ([]string, error) {
	if opts.Offset < 0 {
		return []string{}, fmt.Errorf("offset must be >= 0, got %d", opts.Offset)
	}

	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return []string{}, fmt.Errorf("unable to open keg: %w", err)
	}

	kegMatches, err := k.Grep(ctx, keg.GrepOptions{
		Pattern:    opts.Query,
		IgnoreCase: opts.IgnoreCase,
		MaxLines:   opts.MaxLines,
	})
	if err != nil {
		return []string{}, fmt.Errorf("invalid query regex %q: %w", opts.Query, err)
	}
	matches := make([]grepMatch, 0, len(kegMatches))
	for _, m := range kegMatches {
		matches = append(matches, grepMatch{entry: m.Entry, lines: m.Lines})
	}

	matches = applyOffsetSlice(matches, opts.Offset)

	if opts.Limit > 0 && len(matches) > opts.Limit {
		matches = matches[:opts.Limit]
	}

	matchedEntries := make([]keg.NodeIndexEntry, 0, len(matches))
	for _, match := range matches {
		matchedEntries = append(matchedEntries, match.entry)
	}
	if opts.IdOnly || opts.Format != "" {
		return t.renderNodeEntries(ctx, k, matchedEntries, renderOptions{
			Format: opts.Format, IdOnly: opts.IdOnly, Reverse: opts.Reverse,
		})
	}
	return renderGrepMatches(matches, opts.Reverse), nil
}

func (t *Tap) Tags(ctx context.Context, opts TagsOptions) ([]string, error) {
	if opts.Offset < 0 {
		return []string{}, fmt.Errorf("offset must be >= 0, got %d", opts.Offset)
	}

	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return []string{}, fmt.Errorf("unable to open keg: %w", err)
	}
	listing, err := k.ListEntries(ctx, keg.ListEntriesOptions{Query: opts.Query})
	if err != nil {
		if strings.TrimSpace(opts.Query) != "" {
			return []string{}, fmt.Errorf("invalid query expression: %w", err)
		}
		return []string{}, fmt.Errorf("unable to list entries: %w", err)
	}

	queryExpr := strings.TrimSpace(opts.Query)

	if queryExpr == "" {
		tags := listing.Tags
		sortStringsAsc(tags)
		tags = applyOffsetStrings(tags, opts.Offset)
		if opts.Limit > 0 && len(tags) > opts.Limit {
			tags = tags[:opts.Limit]
		}
		if opts.Reverse {
			reverseStrings(tags)
		}
		return tags, nil
	}

	matchedEntries := listing.Entries
	if len(matchedEntries) == 0 {
		return []string{}, nil
	}
	matchedIDs := make(map[string]struct{}, len(matchedEntries)*2)
	for _, entry := range matchedEntries {
		matchedIDs[entry.ID] = struct{}{}
		if node, parseErr := keg.ParseNode(entry.ID); parseErr == nil && node != nil {
			matchedIDs[node.Path()] = struct{}{}
		}
	}

	entries := matchedEntries
	sortNodeIndexEntries(entries)

	entries = applyOffset(entries, opts.Offset)

	if opts.Limit > 0 && len(entries) > opts.Limit {
		entries = entries[:opts.Limit]
	}

	return t.renderNodeEntries(ctx, k, entries, renderOptions{
		Format: opts.Format, IdOnly: opts.IdOnly, Reverse: opts.Reverse,
	})
}

func grepContentLineMatches(re *regexp.Regexp, raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}

	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	parts := strings.Split(content, "\n")
	lines := make([]string, 0)
	for i, part := range parts {
		line := strings.TrimRight(part, "\r")
		if re.MatchString(line) {
			lines = append(lines, fmt.Sprintf("%d:%s", i+1, line))
		}
	}
	return lines
}

func renderGrepMatches(matches []grepMatch, reverse bool) []string {
	lines := make([]string, 0)

	start := 0
	end := len(matches)
	step := 1
	if reverse {
		start = len(matches) - 1
		end = -1
		step = -1
	}

	first := true
	for i := start; i != end; i += step {
		match := matches[i]
		if !first {
			lines = append(lines, "")
		}
		first = false

		header := strings.TrimSpace(match.entry.Title)
		if header == "" {
			lines = append(lines, match.entry.ID)
		} else {
			lines = append(lines, fmt.Sprintf("%s %s", match.entry.ID, header))
		}
		lines = append(lines, match.lines...)
	}

	return lines
}

// renderOptions carries the presentation knobs shared by every listing surface.
type renderOptions struct {
	Format  string
	IdOnly  bool
	Reverse bool
}

// enrichWarnThreshold is the number of nodes above which a format requiring
// per-node reads warns once, so a slow listing explains itself.
const enrichWarnThreshold = 200

// renderNodeEntries renders one line per entry using the compiled format.
//
// Formats naming only intrinsics, index timestamps, or legacy verbs — which
// includes the default — perform no additional I/O. A format naming metadata
// or a statistics field costs one read per node for each, so the whole pass is
// wrapped in a single keg read boundary: the boundary is exclusive and
// context-re-entrant, so without this the pass would acquire and release it
// twice per node and block every other process on the keg.
func (t *Tap) renderNodeEntries(
	ctx context.Context,
	k keg.Keg,
	entries []keg.NodeIndexEntry,
	opts renderOptions,
) ([]string, error) {
	if opts.IdOnly {
		return renderNodeIDs(entries, opts.Reverse), nil
	}

	compiled, err := compileListFormat(opts.Format)
	if err != nil {
		return nil, err
	}

	lines := make([]string, 0, len(entries))
	enrich := compiled.needsMeta || compiled.needsStats

	render := func(ctx context.Context) error {
		start, end, step := iterationBounds(len(entries), opts.Reverse)
		for i := start; i != end; i += step {
			if enrich {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			src := nodeFieldSource{entry: entries[i]}
			if enrich {
				t.loadNodeFields(ctx, k, &src, compiled)
			}
			lines = append(lines, expandFormat(compiled, src))
		}
		return nil
	}

	if !enrich {
		if err := render(ctx); err != nil {
			return nil, err
		}
		return lines, nil
	}

	if len(entries) >= enrichWarnThreshold {
		t.Runtime.Logger().Warn(
			"listing format reads per-node metadata",
			"nodes", len(entries),
			"keg", describeKeg(k),
		)
	}
	if err := keg.WithReadBoundary(ctx, k, render); err != nil {
		return nil, err
	}
	return lines, nil
}

// loadNodeFields fetches only what the compiled format actually needs. A read
// failure leaves the value empty rather than failing the listing: the stale
// index path already anticipates nodes that are indexed but unreadable, and one
// broken node must not blank an entire listing.
func (t *Tap) loadNodeFields(ctx context.Context, k keg.Keg, src *nodeFieldSource, compiled compiledFormat) {
	id, err := keg.ParseNode(src.entry.ID)
	if err != nil || id == nil {
		return
	}
	if compiled.needsMeta {
		if meta, err := k.GetMeta(ctx, *id); err == nil {
			src.meta = meta
		}
	}
	if compiled.needsStats {
		if stats, err := k.GetStats(ctx, *id); err == nil {
			src.stats = stats
		}
	}
}

func renderNodeIDs(entries []keg.NodeIndexEntry, reverse bool) []string {
	lines := make([]string, 0, len(entries))
	start, end, step := iterationBounds(len(entries), reverse)
	for i := start; i != end; i += step {
		lines = append(lines, entries[i].ID)
	}
	return lines
}

func iterationBounds(n int, reverse bool) (start, end, step int) {
	if reverse {
		return n - 1, -1, -1
	}
	return 0, n, 1
}

func sortNodeIndexEntriesByTime(entries []keg.NodeIndexEntry, timeFunc func(keg.NodeIndexEntry) time.Time) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0; j-- {
			if !timeFunc(entries[j]).Before(timeFunc(entries[j-1])) {
				break
			}
			entries[j-1], entries[j] = entries[j], entries[j-1]
		}
	}
}

func sortNodeIndexEntries(entries []keg.NodeIndexEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0; j-- {
			if compareNodeEntryID(entries[j-1].ID, entries[j].ID) <= 0 {
				break
			}
			entries[j-1], entries[j] = entries[j], entries[j-1]
		}
	}
}

func sortStringsAsc(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0; j-- {
			if values[j-1] <= values[j] {
				break
			}
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}

func reverseStrings(values []string) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

// applyOffset skips the first n entries. If n >= len(entries), returns empty.
func applyOffset(entries []keg.NodeIndexEntry, n int) []keg.NodeIndexEntry {
	if n <= 0 {
		return entries
	}
	if n >= len(entries) {
		return nil
	}
	return entries[n:]
}

// applyOffsetSlice skips the first n elements of a grepMatch slice.
func applyOffsetSlice(matches []grepMatch, n int) []grepMatch {
	if n <= 0 {
		return matches
	}
	if n >= len(matches) {
		return nil
	}
	return matches[n:]
}

// applyOffsetStrings skips the first n elements of a string slice.
func applyOffsetStrings(values []string, n int) []string {
	if n <= 0 {
		return values
	}
	if n >= len(values) {
		return nil
	}
	return values[n:]
}

func compareNodeEntryID(a, b string) int {
	na, ea := keg.ParseNode(a)
	nb, eb := keg.ParseNode(b)
	if ea == nil && eb == nil && na != nil && nb != nil {
		return na.Compare(*nb)
	}
	if ea == nil && na != nil {
		return -1
	}
	if eb == nil && nb != nil {
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
