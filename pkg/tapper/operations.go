package tapper

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapapi"
)

// Operations executes complete TAP operations against a resolved KEG: the
// relationship lookup plus the fields its listing renders, or a tag listing,
// as one unit. It is the seam that lets a Hub-backed KEG answer the whole
// operation next to the data instead of the client composing KEG calls.
//
// The default forwards to the KEG's Hub over /api/v1/tap/* when the KEG is a
// Hub KEG and composes KEG operations locally otherwise. A Hub serving its
// own MCP endpoint injects an implementation backed by its TAP service.
type Operations interface {
	// Related returns one page of a node set's links or backlinks in k.
	Related(ctx context.Context, k keg.Keg, direction keg.RelatedDirection, in tapapi.RelatedRequest) (*tapapi.RelatedResponse, error)
	// Tags lists k's tags, or with a query, one page of the nodes it selects.
	Tags(ctx context.Context, k keg.Keg, in tapapi.TagsRequest) (*tapapi.TagsResponse, error)
}

// operations returns the injected Operations or the default.
func (t *Tap) operations() Operations {
	if t.Operations != nil {
		return t.Operations
	}
	return DefaultOperations{}
}

// hubKeg is a KEG served by a Tapper Hub that can carry a complete TAP
// operation to it.
type hubKeg interface {
	HubPost(ctx context.Context, path, op string, in, out any) error
	Target() *keg.Target
}

// DefaultOperations forwards to the KEG's Hub when it is a Hub KEG and
// composes KEG operations otherwise.
type DefaultOperations struct{}

func (DefaultOperations) Related(ctx context.Context, k keg.Keg, direction keg.RelatedDirection, in tapapi.RelatedRequest) (*tapapi.RelatedResponse, error) {
	if hub, ref, ok := hubKegRef(k); ok {
		in.Keg = ref
		path := tapapi.PathLinks
		if direction == keg.RelatedBacklinks {
			path = tapapi.PathBacklinks
		}
		var out tapapi.RelatedResponse
		if err := hub.HubPost(ctx, path, "Related", in, &out); err != nil {
			return nil, err
		}
		return &out, nil
	}
	return LocalOperations{}.Related(ctx, k, direction, in)
}

func (DefaultOperations) Tags(ctx context.Context, k keg.Keg, in tapapi.TagsRequest) (*tapapi.TagsResponse, error) {
	if hub, ref, ok := hubKegRef(k); ok {
		in.Keg = ref
		var out tapapi.TagsResponse
		if err := hub.HubPost(ctx, tapapi.PathTags, "Tags", in, &out); err != nil {
			return nil, err
		}
		return &out, nil
	}
	return LocalOperations{}.Tags(ctx, k, in)
}

func hubKegRef(k keg.Keg) (hubKeg, string, bool) {
	hub, ok := k.(hubKeg)
	if !ok {
		return nil, "", false
	}
	target := hub.Target()
	if target == nil || target.Namespace == "" || target.KegName == "" {
		return nil, "", false
	}
	return hub, "@" + target.Namespace + "/" + target.KegName, true
}

// LocalOperations composes TAP operations from KEG operations. Field values
// for a page are resolved with one ListView call, never per node.
type LocalOperations struct{}

func (LocalOperations) Related(ctx context.Context, k keg.Keg, direction keg.RelatedDirection, in tapapi.RelatedRequest) (*tapapi.RelatedResponse, error) {
	if in.Offset < 0 || in.Limit < 0 {
		return nil, fmt.Errorf("offset and limit must be >= 0: %w", keg.ErrInvalid)
	}
	ids := make([]keg.NodeId, 0, len(in.NodeIDs))
	for _, raw := range in.NodeIDs {
		id, err := keg.ParseNode(raw)
		if err != nil || id == nil {
			return nil, fmt.Errorf("invalid node id %q: %w", raw, keg.ErrInvalid)
		}
		ids = append(ids, *id)
	}
	related, err := k.RelatedNodes(ctx, keg.RelatedNodesOptions{NodeIDs: ids, Direction: direction})
	if err != nil {
		return nil, err
	}
	entries := pageOf(related.Entries, in.Offset, in.Limit)
	rows, err := projectRows(ctx, k, entries, in.Fields)
	if err != nil {
		return nil, err
	}
	onPage := make(map[string]bool, len(entries))
	for _, entry := range entries {
		onPage[entry.ID] = true
	}
	pairs := make([]keg.RelatedPair, 0, len(related.Pairs))
	for _, pair := range related.Pairs {
		if onPage[pair.To] {
			pairs = append(pairs, pair)
		}
	}
	return &tapapi.RelatedResponse{Rows: rows, Pairs: pairs, Total: len(related.Entries)}, nil
}

func (LocalOperations) Tags(ctx context.Context, k keg.Keg, in tapapi.TagsRequest) (*tapapi.TagsResponse, error) {
	if in.Offset < 0 || in.Limit < 0 {
		return nil, fmt.Errorf("offset and limit must be >= 0: %w", keg.ErrInvalid)
	}
	if strings.TrimSpace(in.Query) == "" {
		listing, err := k.ListEntries(ctx, keg.ListEntriesOptions{})
		if err != nil {
			return nil, err
		}
		tags := slices.Clone(listing.Tags)
		sort.Strings(tags)
		return &tapapi.TagsResponse{Tags: pageOf(tags, in.Offset, in.Limit), Total: len(tags)}, nil
	}
	view, err := k.ListView(ctx, keg.ListViewOptions{Query: in.Query, Fields: in.Fields, Offset: in.Offset, Limit: in.Limit})
	if err != nil {
		return nil, err
	}
	return &tapapi.TagsResponse{Rows: view.Rows, Total: view.TotalMatches}, nil
}

// projectRows resolves fields for a known page of entries in one ListView.
func projectRows(ctx context.Context, k keg.Keg, entries []keg.NodeIndexEntry, fields []string) ([]keg.ListViewRow, error) {
	rows := make([]keg.ListViewRow, len(entries))
	for i, entry := range entries {
		rows[i] = keg.ListViewRow{Entry: entry}
	}
	if len(fields) == 0 || len(entries) == 0 {
		return rows, nil
	}
	ids := make([]keg.NodeId, 0, len(entries))
	for _, entry := range entries {
		if id, err := keg.ParseNode(entry.ID); err == nil && id != nil {
			ids = append(ids, *id)
		}
	}
	view, err := k.ListView(ctx, keg.ListViewOptions{NodeIDs: ids, Fields: fields})
	if err != nil {
		return nil, fmt.Errorf("unable to resolve listing fields: %w", err)
	}
	resolved := make(map[string]map[string]string, len(view.Rows))
	for _, row := range view.Rows {
		resolved[row.Entry.ID] = row.Fields
	}
	for i := range rows {
		rows[i].Fields = resolved[rows[i].Entry.ID]
	}
	return rows, nil
}

func pageOf[T any](items []T, offset, limit int) []T {
	if offset >= len(items) {
		return []T{}
	}
	items = items[offset:]
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}
