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

type ListOptions struct {
	KegTargetOptions

	// Format to use. %i is node id, %d
	// %i is node id
	// %d is date
	// %t is node title
	// %% for literal %
	Format string

	IdOnly bool

	Reverse bool
}

type BacklinksOptions struct {
	KegTargetOptions

	// NodeID is the target node to inspect incoming links for.
	NodeID string

	// Format to use. %i is node id
	// %d is date
	// %t is node title
	// %% for literal %
	Format string

	IdOnly bool

	Reverse bool
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
}

type TagsOptions struct {
	KegTargetOptions

	// Tag filters nodes by tag. When empty, all tags are listed.
	Tag string

	// Format to use. %i is node id
	// %d is date
	// %t is node title
	// %% for literal %
	Format string

	IdOnly bool

	Reverse bool
}

type grepMatch struct {
	entry keg.NodeIndexEntry
	lines []string
}

func (t *Tap) List(ctx context.Context, opts ListOptions) ([]string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return []string{}, fmt.Errorf("unable to open keg: %w", err)
	}
	dex, err := k.Dex(ctx)
	if err != nil {
		return []string{}, fmt.Errorf("unable to read dex: %w", err)
	}

	entries := dex.Nodes(ctx)
	return renderNodeEntries(entries, opts.Format, opts.IdOnly, opts.Reverse), nil
}

func (t *Tap) Backlinks(ctx context.Context, opts BacklinksOptions) ([]string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return []string{}, fmt.Errorf("unable to open keg: %w", err)
	}
	dex, err := k.Dex(ctx)
	if err != nil {
		return []string{}, fmt.Errorf("unable to read dex: %w", err)
	}

	node, err := keg.ParseNode(opts.NodeID)
	if err != nil {
		return []string{}, fmt.Errorf("invalid node ID %q: %w", opts.NodeID, err)
	}
	if node == nil {
		return []string{}, fmt.Errorf("invalid node ID %q: %w", opts.NodeID, keg.ErrInvalid)
	}
	id := keg.NodeId{ID: node.ID, Code: node.Code}

	exists, err := k.Repo.HasNode(ctx, id)
	if err != nil {
		return []string{}, fmt.Errorf("unable to inspect node: %w", err)
	}
	if !exists {
		return []string{}, fmt.Errorf("node %s not found", id.Path())
	}

	backlinks, ok := dex.Backlinks(ctx, id)
	if !ok || len(backlinks) == 0 {
		return []string{}, nil
	}

	entries := make([]keg.NodeIndexEntry, 0, len(backlinks))
	for _, source := range backlinks {
		ref := dex.GetRef(ctx, source)
		if ref != nil {
			entries = append(entries, *ref)
			continue
		}
		entries = append(entries, keg.NodeIndexEntry{ID: source.Path()})
	}
	sortNodeIndexEntries(entries)
	return renderNodeEntries(entries, opts.Format, opts.IdOnly, opts.Reverse), nil
}

func (t *Tap) Grep(ctx context.Context, opts GrepOptions) ([]string, error) {
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
		if len(lineMatches) > 0 {
			matches = append(matches, grepMatch{
				entry: entry,
				lines: lineMatches,
			})
		}
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
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return []string{}, fmt.Errorf("unable to open keg: %w", err)
	}
	dex, err := k.Dex(ctx)
	if err != nil {
		return []string{}, fmt.Errorf("unable to read dex: %w", err)
	}

	tag := strings.TrimSpace(opts.Tag)
	if tag == "" {
		tags := dex.TagList(ctx)
		sortStringsAsc(tags)
		if opts.Reverse {
			reverseStrings(tags)
		}
		return tags, nil
	}

	expr, err := parseTagExpression(tag)
	if err != nil {
		return []string{}, fmt.Errorf("invalid tag expression: %w", err)
	}

	indexEntries := dex.Nodes(ctx)
	universe := make(map[string]struct{}, len(indexEntries))
	entryByID := make(map[string]keg.NodeIndexEntry, len(indexEntries))
	for _, entry := range indexEntries {
		entryByID[entry.ID] = entry
		universe[entry.ID] = struct{}{}
		node, parseErr := keg.ParseNode(entry.ID)
		if parseErr == nil && node != nil {
			path := node.Path()
			entryByID[path] = entry
			universe[path] = struct{}{}
		}
	}

	matchedIDs := evaluateTagExpression(expr, universe, func(tagName string) map[string]struct{} {
		nodes, ok := dex.TagNodes(ctx, tagName)
		if !ok || len(nodes) == 0 {
			return map[string]struct{}{}
		}
		return setFromNodeIDs(nodes)
	})
	if len(matchedIDs) == 0 {
		return []string{}, nil
	}

	entries := make([]keg.NodeIndexEntry, 0, len(matchedIDs))
	for nodeID := range matchedIDs {
		if entry, ok := entryByID[nodeID]; ok {
			entries = append(entries, entry)
			continue
		}
		node, parseErr := keg.ParseNode(nodeID)
		if parseErr == nil && node != nil {
			ref := dex.GetRef(ctx, *node)
			if ref != nil {
				entries = append(entries, *ref)
				continue
			}
		}
		entries = append(entries, keg.NodeIndexEntry{ID: nodeID})
	}
	sortNodeIndexEntries(entries)
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
		line = strings.Replace(line, "%t", entry.Title, -1)
		lines = append(lines, line)
	}
	return lines
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
