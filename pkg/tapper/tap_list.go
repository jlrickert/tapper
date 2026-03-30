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

	// Format to use. %i is node id, %d
	// %i is node id
	// %d is date
	// %t is node title
	// %% for literal %
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

	// Format to use. %i is node id
	// %d is date
	// %t is node title
	// %% for literal %
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

	// Format to use. %i is node id
	// %d is date
	// %t is node title
	// %% for literal %
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

	// Format to use. %i is node id
	// %d is date
	// %t is node title
	// %% for literal %
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
	// ("entity=plan"). When non-empty it takes precedence over Tag.
	Query string

	// Tag filters nodes by tag expression. Deprecated: use Query instead.
	// When empty and Query is also empty, all tags are listed.
	Tag string

	// Format to use. %i is node id
	// %d is date
	// %t is node title
	// %% for literal %
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
	dex, err := k.Dex(ctx)
	if err != nil {
		return []string{}, fmt.Errorf("unable to read dex: %w", err)
	}

	entries := dex.Nodes(ctx)

	// Warn when the index appears significantly stale compared to on-disk nodes.
	if onDisk, listErr := k.Repo.ListNodes(ctx); listErr == nil {
		indexed := len(entries)
		total := len(onDisk)
		gap := total - indexed
		threshold := total / 10 // 10%
		if threshold < 5 {
			threshold = 5
		}
		if gap >= threshold {
			t.Runtime.Logger().Warn(
				"index appears stale: run `tap index rebuild` to fix",
				"indexed", indexed,
				"on_disk", total,
				"missing", gap,
			)
		}
	}

	if q := strings.TrimSpace(opts.Query); q != "" {
		matchedIDs, evalErr := evalQueryExpr(ctx, k, dex, entries, q)
		if evalErr != nil {
			return []string{}, fmt.Errorf("invalid query expression: %w", evalErr)
		}
		filtered := make([]keg.NodeIndexEntry, 0, len(matchedIDs))
		entryByID := make(map[string]keg.NodeIndexEntry, len(entries)*2)
		for _, e := range entries {
			entryByID[e.ID] = e
			id, parseErr := keg.ParseNode(e.ID)
			if parseErr == nil && id != nil {
				entryByID[id.Path()] = e
			}
		}
		seen := make(map[string]struct{})
		for nodeID := range matchedIDs {
			if e, ok := entryByID[nodeID]; ok {
				if _, dup := seen[e.ID]; !dup {
					seen[e.ID] = struct{}{}
					filtered = append(filtered, e)
				}
			}
		}
		sortNodeIndexEntries(filtered)
		entries = filtered
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
	default:
		return []string{}, fmt.Errorf("unknown sort type: %q", opts.Sort)
	}

	entries = applyOffset(entries, opts.Offset)

	if opts.Limit > 0 && len(entries) > opts.Limit {
		entries = entries[:opts.Limit]
	}

	return renderNodeEntries(entries, opts.Format, opts.IdOnly, opts.Reverse), nil
}

func (t *Tap) Backlinks(ctx context.Context, opts BacklinksOptions) ([]string, error) {
	if opts.Offset < 0 {
		return []string{}, fmt.Errorf("offset must be >= 0, got %d", opts.Offset)
	}
	return t.resolveAndLookupLinks(ctx, opts.KegTargetOptions, opts.NodeIDs,
		opts.Format, opts.IdOnly, opts.Reverse, opts.Limit, opts.Offset,
		func(d *keg.Dex, id keg.NodeId) ([]keg.NodeId, bool) {
			return d.Backlinks(ctx, id)
		})
}

func (t *Tap) Links(ctx context.Context, opts LinksOptions) ([]string, error) {
	if opts.Offset < 0 {
		return []string{}, fmt.Errorf("offset must be >= 0, got %d", opts.Offset)
	}
	return t.resolveAndLookupLinks(ctx, opts.KegTargetOptions, opts.NodeIDs,
		opts.Format, opts.IdOnly, opts.Reverse, opts.Limit, opts.Offset,
		func(d *keg.Dex, id keg.NodeId) ([]keg.NodeId, bool) {
			return d.Links(ctx, id)
		})
}

// resolveAndLookupLinks is a shared helper for Backlinks and Links. It resolves
// the keg, validates the node IDs, calls the provided lookup function against the
// dex for each node, merges and deduplicates results, and renders the entries.
func (t *Tap) resolveAndLookupLinks(
	ctx context.Context,
	kegOpts KegTargetOptions,
	nodeIDs []string,
	format string,
	idOnly bool,
	reverse bool,
	limit int,
	offset int,
	lookup func(*keg.Dex, keg.NodeId) ([]keg.NodeId, bool),
) ([]string, error) {
	if len(nodeIDs) == 0 {
		return []string{}, fmt.Errorf("at least one node ID is required")
	}

	k, err := t.resolveKeg(ctx, kegOpts)
	if err != nil {
		return []string{}, fmt.Errorf("unable to open keg: %w", err)
	}
	dex, err := k.Dex(ctx)
	if err != nil {
		return []string{}, fmt.Errorf("unable to read dex: %w", err)
	}

	// Collect and deduplicate results across all node IDs.
	seen := make(map[string]struct{})
	var allRelated []keg.NodeId

	for _, nodeID := range nodeIDs {
		id, err := parseNodeID(nodeID)
		if err != nil {
			return []string{}, err
		}

		exists, err := k.Repo.HasNode(ctx, id)
		if err != nil {
			return []string{}, fmt.Errorf("unable to inspect node: %w", err)
		}
		if !exists {
			return []string{}, fmt.Errorf("node %s not found", id.Path())
		}

		related, ok := lookup(dex, id)
		if !ok {
			continue
		}
		for _, rel := range related {
			key := rel.Path()
			if _, dup := seen[key]; !dup {
				seen[key] = struct{}{}
				allRelated = append(allRelated, rel)
			}
		}
	}

	if len(allRelated) == 0 {
		return []string{}, nil
	}

	entries := make([]keg.NodeIndexEntry, 0, len(allRelated))
	for _, rel := range allRelated {
		ref := dex.GetRef(ctx, rel)
		if ref != nil {
			entries = append(entries, *ref)
			continue
		}
		entries = append(entries, keg.NodeIndexEntry{ID: rel.Path()})
	}
	sortNodeIndexEntries(entries)

	entries = applyOffset(entries, offset)

	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}

	return renderNodeEntries(entries, format, idOnly, reverse), nil
}

func (t *Tap) Grep(ctx context.Context, opts GrepOptions) ([]string, error) {
	if opts.Offset < 0 {
		return []string{}, fmt.Errorf("offset must be >= 0, got %d", opts.Offset)
	}

	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return []string{}, fmt.Errorf("unable to open keg: %w", err)
	}
	dex, err := k.Dex(ctx)
	if err != nil {
		return []string{}, fmt.Errorf("unable to read dex: %w", err)
	}

	pattern := opts.Query
	if opts.IgnoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return []string{}, fmt.Errorf("invalid query regex %q: %w", opts.Query, err)
	}

	entries := dex.Nodes(ctx)
	matches := make([]grepMatch, 0)
	for _, entry := range entries {
		id, parseErr := keg.ParseNode(entry.ID)
		if parseErr != nil || id == nil {
			continue
		}

		contentRaw, contentErr := k.Repo.ReadContent(ctx, *id)
		if contentErr != nil {
			if errors.Is(contentErr, keg.ErrNotExist) {
				continue
			}
			return []string{}, fmt.Errorf("unable to read node content: %w", contentErr)
		}
		lineMatches := grepContentLineMatches(re, contentRaw)
		if opts.MaxLines > 0 && len(lineMatches) > opts.MaxLines {
			lineMatches = lineMatches[:opts.MaxLines]
		}
		if len(lineMatches) > 0 {
			matches = append(matches, grepMatch{
				entry: entry,
				lines: lineMatches,
			})
		}
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
		return renderNodeEntries(matchedEntries, opts.Format, opts.IdOnly, opts.Reverse), nil
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
	dex, err := k.Dex(ctx)
	if err != nil {
		return []string{}, fmt.Errorf("unable to read dex: %w", err)
	}

	// Prefer Query over the legacy Tag field.
	queryExpr := strings.TrimSpace(opts.Query)
	if queryExpr == "" {
		queryExpr = strings.TrimSpace(opts.Tag)
	}

	if queryExpr == "" {
		tags := dex.TagList(ctx)
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

	indexEntries := dex.Nodes(ctx)
	entryByID := make(map[string]keg.NodeIndexEntry, len(indexEntries)*2)
	for _, entry := range indexEntries {
		entryByID[entry.ID] = entry
		node, parseErr := keg.ParseNode(entry.ID)
		if parseErr == nil && node != nil {
			entryByID[node.Path()] = entry
		}
	}

	matchedIDs, evalErr := evalQueryExpr(ctx, k, dex, indexEntries, queryExpr)
	if evalErr != nil {
		return []string{}, fmt.Errorf("invalid query expression: %w", evalErr)
	}
	if len(matchedIDs) == 0 {
		return []string{}, nil
	}

	seen := make(map[string]struct{})
	entries := make([]keg.NodeIndexEntry, 0, len(matchedIDs))
	for nodeID := range matchedIDs {
		if entry, ok := entryByID[nodeID]; ok {
			if _, dup := seen[entry.ID]; !dup {
				seen[entry.ID] = struct{}{}
				entries = append(entries, entry)
			}
			continue
		}
		node, parseErr := keg.ParseNode(nodeID)
		if parseErr == nil && node != nil {
			if _, dup := seen[node.Path()]; dup {
				continue
			}
			seen[node.Path()] = struct{}{}
			ref := dex.GetRef(ctx, *node)
			if ref != nil {
				entries = append(entries, *ref)
				continue
			}
		}
		if _, dup := seen[nodeID]; !dup {
			seen[nodeID] = struct{}{}
			entries = append(entries, keg.NodeIndexEntry{ID: nodeID})
		}
	}
	sortNodeIndexEntries(entries)

	entries = applyOffset(entries, opts.Offset)

	if opts.Limit > 0 && len(entries) > opts.Limit {
		entries = entries[:opts.Limit]
	}

	return renderNodeEntries(entries, opts.Format, opts.IdOnly, opts.Reverse), nil
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

func renderNodeEntries(entries []keg.NodeIndexEntry, format string, idOnly bool, reverse bool) []string {
	lines := make([]string, 0)

	start := 0
	end := len(entries)
	step := 1
	if reverse {
		start = len(entries) - 1
		end = -1
		step = -1
	}

	for i := start; i != end; i += step {
		entry := entries[i]
		if idOnly {
			lines = append(lines, entry.ID)
			continue
		}

		lineFormat := format
		if lineFormat == "" {
			lineFormat = "%i\t%d\t%t"
		}

		line := lineFormat
		line = strings.Replace(line, "%i", entry.ID, -1)
		line = strings.Replace(line, "%d", entry.Updated.Format(time.RFC3339), -1)
		line = strings.Replace(line, "%c", entry.Created.Format(time.RFC3339), -1)
		line = strings.Replace(line, "%a", entry.Accessed.Format(time.RFC3339), -1)
		line = strings.Replace(line, "%t", entry.Title, -1)
		lines = append(lines, line)
	}
	return lines
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
