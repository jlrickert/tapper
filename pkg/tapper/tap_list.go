package tapper

import (
	"context"
	"errors"
	"fmt"
	"github.com/jlrickert/tapper/pkg/tapapi"
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
	// id, title, and the three dates are resolved by the keg for the
	// returned page in one batch.
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
	// id, title, and the three dates are resolved by the keg for the
	// returned page in one batch.
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
	// id, title, and the three dates are resolved by the keg for the
	// returned page in one batch.
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
	// id, title, and the three dates are resolved by the keg for the
	// returned page in one batch.
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
	// id, title, and the three dates are resolved by the keg for the
	// returned page in one batch.
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
	case strings.TrimSpace(opts.Query) != "":
		return []string{}, fmt.Errorf("invalid query expression: %w", err)
	default:
		return []string{}, fmt.Errorf("unable to list keg: %w", err)
	}
	t.warnStaleIndex(view.IndexedCount, view.NodeCount)
	return renderListView(compiled, view.Rows, renderOptions{
		Format: opts.Format, IdOnly: opts.IdOnly, Reverse: opts.Reverse,
	}), nil
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
	cfg, err := k.Settings(ctx)
	if err != nil || cfg == nil || len(cfg.ListFields) == 0 {
		return explicit
	}
	return formatFromFieldSelectors(cfg.ListFields)
}

// formatFromFieldSelectors renders a selector list as a tab-separated format
// string, so keg settings and --format share one language.
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

// resolveAndLookupLinks is the shared body of Backlinks and Links. The whole
// operation — relationship lookup, paging, and the fields the format renders —
// runs through Operations, so a Hub KEG answers it in one request.
func (t *Tap) resolveAndLookupLinks(ctx context.Context, opts relatedListOptions) ([]string, error) {
	if len(opts.NodeIDs) == 0 {
		return []string{}, fmt.Errorf("at least one node ID is required")
	}

	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return []string{}, fmt.Errorf("unable to open keg: %w", err)
	}
	ids := make([]string, 0, len(opts.NodeIDs))
	for _, nodeID := range opts.NodeIDs {
		// Intentionally NOT routed through resolveNodeArg: a cross-keg ref would
		// produce related nodes owned by a different keg, but paging and
		// rendering assume a single owning keg, so links/backlinks stay scoped
		// to the current keg.
		id, err := parseNodeID(nodeID)
		if err != nil {
			return []string{}, err
		}
		ids = append(ids, id.Path())
	}
	compiled, err := compileListFormat(opts.Render.Format)
	if err != nil {
		return nil, err
	}
	related, err := t.operations().Related(ctx, k, opts.Direction, tapapi.RelatedRequest{
		NodeIDs: ids,
		Fields:  compiled.selectorTexts(opts.Render.IdOnly),
		Offset:  opts.Offset,
		Limit:   opts.Limit,
	})
	if err != nil {
		if strings.Contains(err.Error(), "keg not initialized") {
			return []string{}, err
		}
		if errors.Is(err, keg.ErrNotExist) {
			return []string{}, fmt.Errorf("node %s not found in %s", ids[0], describeKeg(k))
		}
		return []string{}, err
	}
	rendered := renderListView(compiled, related.Rows, opts.Render)
	if observer, ok := ctx.Value(relationshipObserverKey{}).(RelationshipResultObserver); ok && observer != nil {
		relationships := []Relationship{}
		seen := map[Relationship]bool{}
		for _, pair := range related.Pairs {
			source, parseErr := keg.ParseNode(pair.From)
			if parseErr != nil || source == nil {
				return nil, fmt.Errorf("invalid relationship source %q: %w", pair.From, keg.ErrInvalid)
			}
			other, parseErr := keg.ParseNodeRef(pair.To)
			if parseErr != nil {
				return nil, parseErr
			}
			target := other.Node
			if other.Form == keg.RefQualified {
				target.Alias = "@" + other.Namespace + "/" + other.KegName
			}
			rel := Relationship{Source: *source, Target: target}
			if opts.Direction == keg.RelatedBacklinks {
				rel.Source, rel.Target = rel.Target, rel.Source
			}
			if !seen[rel] {
				seen[rel] = true
				relationships = append(relationships, rel)
			}
		}
		observer(ctx, k, relationships)
	}
	return rendered, nil
}

func (t *Tap) Grep(ctx context.Context, opts GrepOptions) ([]string, error) {
	if opts.Offset < 0 {
		return []string{}, fmt.Errorf("offset must be >= 0, got %d", opts.Offset)
	}

	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return []string{}, fmt.Errorf("unable to open keg: %w", err)
	}

	formatted := opts.IdOnly || opts.Format != ""
	var compiled compiledFormat
	if formatted {
		if compiled, err = compileListFormat(opts.Format); err != nil {
			return []string{}, err
		}
	}
	// The keg pages the matches and resolves the format's fields itself, so a
	// formatted grep costs one call however many nodes match.
	kegMatches, err := k.Grep(ctx, keg.GrepOptions{
		Pattern:    opts.Query,
		IgnoreCase: opts.IgnoreCase,
		MaxLines:   opts.MaxLines,
		Fields:     compiled.selectorTexts(opts.IdOnly),
		Offset:     opts.Offset,
		Limit:      opts.Limit,
	})
	if err != nil {
		return []string{}, fmt.Errorf("invalid query regex %q: %w", opts.Query, err)
	}
	if formatted {
		rows := make([]keg.ListViewRow, len(kegMatches))
		for i, m := range kegMatches {
			rows[i] = keg.ListViewRow{Entry: m.Entry, Fields: m.Fields}
		}
		return renderListView(compiled, rows, renderOptions{
			Format: opts.Format, IdOnly: opts.IdOnly, Reverse: opts.Reverse,
		}), nil
	}
	matches := make([]grepMatch, 0, len(kegMatches))
	for _, m := range kegMatches {
		matches = append(matches, grepMatch{entry: m.Entry, lines: m.Lines})
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
	compiled, err := compileListFormat(opts.Format)
	if err != nil {
		return []string{}, err
	}
	queryExpr := strings.TrimSpace(opts.Query)
	result, err := t.operations().Tags(ctx, k, tapapi.TagsRequest{
		Query:  queryExpr,
		Fields: compiled.selectorTexts(opts.IdOnly),
		Offset: opts.Offset,
		Limit:  opts.Limit,
	})
	if err != nil {
		if queryExpr != "" {
			return []string{}, fmt.Errorf("invalid query expression: %w", err)
		}
		return []string{}, fmt.Errorf("unable to list entries: %w", err)
	}
	if queryExpr == "" {
		tags := result.Tags
		if tags == nil {
			tags = []string{}
		}
		if opts.Reverse {
			reverseStrings(tags)
		}
		return tags, nil
	}
	return renderListView(compiled, result.Rows, renderOptions{
		Format: opts.Format, IdOnly: opts.IdOnly, Reverse: opts.Reverse,
	}), nil
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
