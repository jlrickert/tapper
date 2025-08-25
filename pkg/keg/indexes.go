package keg

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// IndexBuilder constructs a single index artifact (for example: nodes.tsv,
// tags, links, backlinks). Implementations maintain in-memory state via Add,
// Remove, Clear and produce the serialized bytes to write via Data.
type IndexBuilder interface {
	// Name returns the canonical index filename (for example "dex/tags").
	Name() string

	// Add incorporates information from a node into the index's in-memory state.
	Add(ctx context.Context, node Node) error

	// Remove deletes node-related state from the index.
	Remove(ctx context.Context, node NodeID) error

	// Clear resets the index to an empty state.
	Clear(ctx context.Context) error

	// Data returns the serialized index bytes to be written to storage.
	Data(ctx context.Context) ([]byte, error)
}

// ----------------- IndexBuilder implementations -----------------

// NodesIndex builds the "nodes.tsv" index (id -> updated -> title).
type NodesIndex struct {
	NextID NodeID
	Nodes  []NodeRef
}

var _ IndexBuilder = (*NodesIndex)(nil)

// NewNodesIndex constructs an empty NodesIndex.
func NewNodesIndex() *NodesIndex {
	return &NodesIndex{Nodes: []NodeRef{}}
}

// NewNodesIndexFromRepo attempts to load a prebuilt nodes.tsv index from repo.
// If the index is missing, unreadable, or empty, it returns an empty NodesIndex
// so callers can rebuild deterministically.
func NewNodesIndexFromRepo(ctx context.Context, repo KegRepository) (*NodesIndex, error) {
	idx := &NodesIndex{}

	// If caller passed nil, return an empty index (no error).
	if repo == nil {
		return idx, nil
	}

	// Try to read a prebuilt nodes.tsv. Missing or unreadable index yields an
	// empty index so generators can recreate it.
	data, err := repo.GetIndex(ctx, idx.Name())
	if err != nil || len(data) == 0 {
		return idx, nil
	}

	// Parse TSV lines. Expected per-line form: "<id>\t<updated>\t<title>\n"
	start := 0
	for start < len(data) {
		// find end of line
		i := start
		for i < len(data) && data[i] != '\n' {
			i++
		}
		line := data[start:i]
		line = bytesTrim(line)
		if len(line) > 0 {
			// find first and second tab positions
			t1 := -1
			t2 := -1
			for j := 0; j < len(line); j++ {
				if line[j] == '\t' {
					if t1 == -1 {
						t1 = j
					} else {
						t2 = j
						break
					}
				}
			}

			if t1 != -1 {
				idBytes := bytesTrim(line[:t1])
				var titleBytes []byte

				if t2 != -1 {
					// title is the third column (after second tab)
					titleBytes = bytesTrim(line[t2+1:])
				} else {
					// fallback: remainder after first tab may contain updated + title.
					rem := bytesTrim(line[t1+1:])
					// try to split on first space (updated may contain no tab)
					sidx := -1
					for j := range rem {
						if rem[j] == ' ' {
							sidx = j
							break
						}
					}
					if sidx != -1 && sidx+1 < len(rem) {
						titleBytes = bytesTrim(rem[sidx+1:])
					} else {
						// treat remainder as title if parsing fails
						titleBytes = rem
					}
				}

				idStr := string(bytesTrim(idBytes))
				if idStr != "" {
					if idInt, perr := strconv.Atoi(idStr); perr == nil {
						title := string(bytesTrim(titleBytes))
						idx.Nodes = append(idx.Nodes, NodeRef{
							ID:    NodeID(idInt),
							Title: title,
							// Updated left zero; authoritative timestamps should be taken
							// from meta.yaml when available.
						})
					}
				}
			}
		}
		// advance to next line
		start = i + 1
	}

	return idx, nil
}

func (idx *NodesIndex) Name() string { return "nodes.tsv" }

// Add appends the node reference to the in-memory nodes list. The Updated
// timestamp is taken from node.Meta when available; callers should ensure meta
// is present when relying on Updated for ordering.
func (idx *NodesIndex) Add(ctx context.Context, node Node) error {
	idx.Nodes = append(idx.Nodes, NodeRef{
		ID:      node.ID,
		Title:   node.Content.Title,
		Updated: node.Meta.GetUpdated(),
	})
	return nil
}

// Remove removes the node reference from the in-memory list and recomputes
// NextID as the maximum remaining node id. If the id is not present this is a
// no-op.
func (idx *NodesIndex) Remove(ctx context.Context, id NodeID) error {
	var filtered []NodeRef

	var nextID NodeID
	for _, node := range idx.Nodes {
		if node.ID != id {
			filtered = append(filtered, node)
		}

		if node.ID != id && node.ID > nextID {
			nextID = node.ID
		}
	}

	idx.NextID = NodeID(nextID)
	idx.Nodes = filtered
	return nil
}

// Clear resets the nodes index to empty.
func (idx *NodesIndex) Clear(ctx context.Context) error {
	idx.Nodes = []NodeRef{}
	return nil
}

// Data serializes the nodes index to TSV. Each line is "<id>\t<updated>\t<title>\n".
// Titles have tabs replaced by spaces to keep the TSV well-formed.
func (idx *NodesIndex) Data(ctx context.Context) ([]byte, error) {
	var b strings.Builder
	for _, n := range idx.Nodes {
		id := int(n.ID)
		updated := ""
		if !n.Updated.IsZero() {
			updated = n.Updated.UTC().Format("2006-01-02 15:04:05Z")
		}
		title := strings.ReplaceAll(strings.TrimSpace(n.Title), "\t", " ")
		fmt.Fprintf(&b, "%d\t%s\t%s\n", id, updated, title)
	}
	return []byte(b.String()), nil
}

// TagsIndex builds the "tags" index mapping tag -> sorted list of node ids.
type TagsIndex struct {
	tags map[string][]NodeID
}

func NewTagsIndex() *TagsIndex {
	return &TagsIndex{tags: make(map[string][]NodeID)}
}

// NewTagsIndexFromRepo loads an existing tags index if present. Missing or
// unreadable index returns an empty TagsIndex so callers can rebuild it.
func NewTagsIndexFromRepo(ctx context.Context, repo KegRepository) (*TagsIndex, error) {
	idx := &TagsIndex{tags: map[string][]NodeID{}}

	// If caller passed nil, return empty index.
	if repo == nil {
		return idx, nil
	}

	data, err := repo.GetIndex(ctx, idx.Name())
	if err != nil || len(data) == 0 {
		// Missing or unreadable index => return empty index rather than failing.
		return idx, nil
	}

	start := 0
	for start < len(data) {
		// find end of line
		i := start
		for i < len(data) && data[i] != '\n' {
			i++
		}
		line := bytesTrim(data[start:i])
		if len(line) > 0 {
			parts := strings.Fields(string(line))
			if len(parts) > 0 {
				tag := parts[0]
				if tag != "" {
					seen := make(map[NodeID]struct{}, len(parts))
					var ids []NodeID
					for _, p := range parts[1:] {
						p = strings.TrimSpace(p)
						if p == "" {
							continue
						}
						if v, perr := strconv.Atoi(p); perr == nil && v >= 0 {
							id := NodeID(v)
							if _, ok := seen[id]; ok {
								continue
							}
							seen[id] = struct{}{}
							ids = append(ids, id)
						}
					}
					// selection sort for small slices to ensure ascending order
					for a := 0; a < len(ids); a++ {
						min := a
						for b := a + 1; b < len(ids); b++ {
							if ids[b] < ids[min] {
								min = b
							}
						}
						if min != a {
							ids[a], ids[min] = ids[min], ids[a]
						}
					}
					if len(ids) > 0 {
						idx.tags[tag] = ids
					} else {
						// ensure tag exists with empty slice if no valid ids were found
						idx.tags[tag] = []NodeID{}
					}
				}
			}
		}
		start = i + 1
	}

	return idx, nil
}

func (idx *TagsIndex) Name() string { return "tags" }

// Add appends the node ID for each tag, deduplicates, and keeps per-tag lists
// sorted in ascending order.
func (t *TagsIndex) Add(ctx context.Context, node Node) error {
	if t.tags == nil {
		t.tags = map[string][]NodeID{}
	}

	for _, tag := range node.Meta.Tags() {
		tagList := append(t.tags[tag], node.ID)

		// deduplicate
		seen := make(map[NodeID]struct{}, len(tagList))
		unique := make([]NodeID, 0, len(tagList))
		for _, id := range tagList {
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			unique = append(unique, id)
		}

		// selection sort (efficient for small slices; avoids extra imports)
		for i := 0; i < len(unique); i++ {
			min := i
			for j := i + 1; j < len(unique); j++ {
				if unique[j] < unique[min] {
					min = j
				}
			}
			if min != i {
				unique[i], unique[min] = unique[min], unique[i]
			}
		}

		t.tags[tag] = unique
	}
	return nil
}

// Remove deletes the node ID from all tag lists. If a tag ends up with no
// members it is removed from the map.
func (t *TagsIndex) Remove(ctx context.Context, id NodeID) error {
	if t.tags == nil {
		return nil
	}

	for tag, list := range t.tags {
		// Build a new slice excluding the id to remove.
		newList := make([]NodeID, 0, len(list))
		for _, v := range list {
			if v == id {
				continue
			}
			newList = append(newList, v)
		}

		// If the list is empty after removal, delete the tag entry.
		if len(newList) == 0 {
			delete(t.tags, tag)
			continue
		}

		// Ensure sorted and deduped as a defensive measure.
		for i := 0; i < len(newList); i++ {
			min := i
			for j := i + 1; j < len(newList); j++ {
				if newList[j] < newList[min] {
					min = j
				}
			}
			if min != i {
				newList[i], newList[min] = newList[min], newList[i]
			}
		}
		uniq := make([]NodeID, 0, len(newList))
		prev := NodeID(-1)
		for _, v := range newList {
			if v == prev {
				continue
			}
			uniq = append(uniq, v)
			prev = v
		}

		t.tags[tag] = uniq
	}

	return nil
}

// Clear resets the tags index to empty.
func (t *TagsIndex) Clear(ctx context.Context) error {
	t.tags = map[string][]NodeID{}
	return nil
}

// Data serializes the tags index. Lines are emitted in lexicographic tag order.
// Each line is: "<tag> <id1> <id2>...\n". Tags with no members are omitted.
func (t *TagsIndex) Data(ctx context.Context) ([]byte, error) {
	// Handle nil or empty map
	if t.tags == nil || len(t.tags) == 0 {
		return []byte{}, nil
	}

	// collect and sort tags
	tags := make([]string, 0, len(t.tags))
	for tag := range t.tags {
		tags = append(tags, tag)
	}
	sort.Strings(tags)

	// Build deterministically using strings.Builder.
	var b strings.Builder
	for _, tag := range tags {
		ids := t.tags[tag]
		if len(ids) == 0 {
			continue
		}
		ints := make([]int, len(ids))
		for i, v := range ids {
			ints[i] = int(v)
		}
		sort.Ints(ints)
		fmt.Fprintf(&b, "%s", tag)
		for _, v := range ints {
			fmt.Fprintf(&b, " %d", v)
		}
		fmt.Fprint(&b, "\n")
	}
	return []byte(b.String()), nil
}

var _ IndexBuilder = (*TagsIndex)(nil)

// LinksIndex builds the "links" index (source -> destinations).
type LinksIndex struct {
	links map[NodeID][]NodeID
}

var _ IndexBuilder = (*LinksIndex)(nil)

func NewLinksIndex() *LinksIndex {
	return &LinksIndex{links: make(map[NodeID][]NodeID)}
}

// NewLinksIndexFromRepo loads an existing links index. Missing or unreadable
// index yields an empty LinksIndex so callers can recreate it.
func NewLinksIndexFromRepo(ctx context.Context, repo KegRepository) (*LinksIndex, error) {
	idx := &LinksIndex{links: map[NodeID][]NodeID{}}

	// If caller passed nil, return empty index.
	if repo == nil {
		return idx, nil
	}

	data, err := repo.GetIndex(ctx, idx.Name())
	if err != nil || len(data) == 0 {
		// Missing or unreadable index => return empty index rather than failing.
		return idx, nil
	}

	start := 0
	for start < len(data) {
		// find end of line
		i := start
		for i < len(data) && data[i] != '\n' {
			i++
		}
		line := bytesTrim(data[start:i])
		if len(line) > 0 {
			// Expect form: "<src>\t<dst1> <dst2> ..."
			// find first tab
			tpos := -1
			for j := 0; j < len(line); j++ {
				if line[j] == '\t' {
					tpos = j
					break
				}
			}
			if tpos != -1 {
				srcBytes := bytesTrim(line[:tpos])
				srcStr := string(srcBytes)
				if srcStr != "" {
					if sInt, perr := strconv.Atoi(srcStr); perr == nil {
						src := NodeID(sInt)
						rest := bytesTrim(line[tpos+1:])
						var ids []NodeID
						if len(rest) > 0 {
							parts := strings.Fields(string(rest))
							seen := make(map[NodeID]struct{}, len(parts))
							for _, p := range parts {
								p = strings.TrimSpace(p)
								if p == "" {
									continue
								}
								if v, err := strconv.Atoi(p); err == nil && v >= 0 {
									id := NodeID(v)
									if _, ok := seen[id]; ok {
										continue
									}
									seen[id] = struct{}{}
									ids = append(ids, id)
								}
							}
							// sort ids
							ints := make([]int, len(ids))
							for k, vv := range ids {
								ints[k] = int(vv)
							}
							sort.Ints(ints)
							ids = make([]NodeID, len(ints))
							for k, vv := range ints {
								ids[k] = NodeID(vv)
							}
						}
						idx.links[src] = ids
					}
				}
			}
		}
		start = i + 1
	}

	return idx, nil
}

func (idx *LinksIndex) Name() string { return "links" }

// Add ensures there's an entry for the source node. Extraction of outgoing
// links from content is handled elsewhere.
func (l *LinksIndex) Add(ctx context.Context, node Node) error {
	if l.links == nil {
		l.links = map[NodeID][]NodeID{}
	}
	if _, ok := l.links[node.ID]; !ok {
		l.links[node.ID] = []NodeID{}
	}
	return nil
}

// Remove deletes the source entry and purges the id from any destination lists.
func (l *LinksIndex) Remove(ctx context.Context, id NodeID) error {
	if l.links == nil {
		return nil
	}
	delete(l.links, id)
	// Also remove id from destination lists
	for src, dsts := range l.links {
		newList := make([]NodeID, 0, len(dsts))
		for _, d := range dsts {
			if d == id {
				continue
			}
			newList = append(newList, d)
		}
		l.links[src] = newList
	}
	return nil
}

func (l *LinksIndex) Clear(ctx context.Context) error {
	l.links = map[NodeID][]NodeID{}
	return nil
}

// Data serializes links as TSV lines: "<src>\t<dst1> <dst2>...\n". Sources are
// emitted in ascending numeric order and destination lists are deduped/sorted.
func (l *LinksIndex) Data(ctx context.Context) ([]byte, error) {
	if l.links == nil || len(l.links) == 0 {
		return []byte{}, nil
	}
	// collect and sort source ids
	srcs := make([]int, 0, len(l.links))
	for s := range l.links {
		srcs = append(srcs, int(s))
	}
	sort.Ints(srcs)

	var b strings.Builder
	for _, s := range srcs {
		src := NodeID(s)
		dstList := l.links[src]
		// ensure sorted ascending and deduped
		ints := make([]int, 0, len(dstList))
		seen := make(map[int]struct{})
		for _, d := range dstList {
			di := int(d)
			if _, ok := seen[di]; ok {
				continue
			}
			seen[di] = struct{}{}
			ints = append(ints, di)
		}
		sort.Ints(ints)
		// write: "<src>\t<dst1> <dst2>...\n"
		fmt.Fprintf(&b, "%d\t", src)
		for i, v := range ints {
			if i > 0 {
				fmt.Fprint(&b, " ")
			}
			fmt.Fprint(&b, v)
		}
		fmt.Fprint(&b, "\n")
	}
	return []byte(b.String()), nil
}

// BacklinksIndex builds "backlinks" (destination -> sources).
type BacklinksIndex struct {
	backlinks map[NodeID][]NodeID
}

var _ IndexBuilder = (*BacklinksIndex)(nil)

func NewBacklinksIndex() *BacklinksIndex {
	return &BacklinksIndex{
		backlinks: make(map[NodeID][]NodeID),
	}
}

// NewBacklinksIndexFromRepo loads an existing backlinks index. Missing/unreadable
// data yields an empty BacklinksIndex so callers can recreate it.
func NewBacklinksIndexFromRepo(ctx context.Context, repo KegRepository) (*BacklinksIndex, error) {
	idx := &BacklinksIndex{backlinks: map[NodeID][]NodeID{}}

	// If caller passed nil, return empty index.
	if repo == nil {
		return idx, nil
	}

	data, err := repo.GetIndex(ctx, idx.Name())
	if err != nil || len(data) == 0 {
		// Missing or unreadable index => return empty index rather than failing.
		return idx, nil
	}

	start := 0
	for start < len(data) {
		// find end of line
		i := start
		for i < len(data) && data[i] != '\n' {
			i++
		}
		line := bytesTrim(data[start:i])
		if len(line) > 0 {
			// Expect form: "<dst>\t<src1> src2 ..."
			tpos := -1
			for j := 0; j < len(line); j++ {
				if line[j] == '\t' {
					tpos = j
					break
				}
			}
			if tpos != -1 {
				dstBytes := bytesTrim(line[:tpos])
				dstStr := string(dstBytes)
				if dstStr != "" {
					if dInt, perr := strconv.Atoi(dstStr); perr == nil {
						dst := NodeID(dInt)
						rest := bytesTrim(line[tpos+1:])
						var ids []NodeID
						if len(rest) > 0 {
							parts := strings.Fields(string(rest))
							seen := make(map[NodeID]struct{}, len(parts))
							for _, p := range parts {
								p = strings.TrimSpace(p)
								if p == "" {
									continue
								}
								if v, err := strconv.Atoi(p); err == nil && v >= 0 {
									id := NodeID(v)
									if _, ok := seen[id]; ok {
										continue
									}
									seen[id] = struct{}{}
									ids = append(ids, id)
								}
							}
							// sort ids
							ints := make([]int, len(ids))
							for k, vv := range ids {
								ints[k] = int(vv)
							}
							sort.Ints(ints)
							ids = make([]NodeID, len(ints))
							for k, vv := range ints {
								ids[k] = NodeID(vv)
							}
						}
						idx.backlinks[dst] = ids
					}
				}
			}
		}
		start = i + 1
	}

	return idx, nil
}

func (BacklinksIndex) Name() string { return "backlinks" }

// Add ensures there's an entry for the destination node (no-op for parsing).
func (b *BacklinksIndex) Add(ctx context.Context, node Node) error {
	if b.backlinks == nil {
		b.backlinks = map[NodeID][]NodeID{}
	}
	// ensure destination exists (may be filled later by indexer)
	if _, ok := b.backlinks[node.ID]; !ok {
		b.backlinks[node.ID] = []NodeID{}
	}
	return nil
}

// Remove removes the destination entry and purges the id from all source lists.
func (b *BacklinksIndex) Remove(ctx context.Context, id NodeID) error {
	if b.backlinks == nil {
		return nil
	}
	delete(b.backlinks, id)
	for dst, srcs := range b.backlinks {
		newList := make([]NodeID, 0, len(srcs))
		for _, s := range srcs {
			if s == id {
				continue
			}
			newList = append(newList, s)
		}
		b.backlinks[dst] = newList
	}
	return nil
}

func (b *BacklinksIndex) Clear(ctx context.Context) error {
	b.backlinks = map[NodeID][]NodeID{}
	return nil
}

// Data serializes backlinks as TSV lines: "<dst>\t<src1> <src2>...\n". Destinations
// are emitted in ascending order; source lists are deduped and sorted.
func (b *BacklinksIndex) Data(ctx context.Context) ([]byte, error) {
	if len(b.backlinks) == 0 {
		return []byte{}, nil
	}
	// collect and sort destination ids
	dests := make([]int, 0, len(b.backlinks))
	for d := range b.backlinks {
		dests = append(dests, int(d))
	}
	sort.Ints(dests)

	var sb strings.Builder
	for _, di := range dests {
		dst := NodeID(di)
		srcs := b.backlinks[dst]
		ints := make([]int, 0, len(srcs))
		seen := make(map[int]struct{})
		for _, s := range srcs {
			si := int(s)
			if _, ok := seen[si]; ok {
				continue
			}
			seen[si] = struct{}{}
			ints = append(ints, si)
		}
		sort.Ints(ints)
		fmt.Fprintf(&sb, "%d\t", dst)
		for i, v := range ints {
			if i > 0 {
				fmt.Fprint(&sb, " ")
			}
			fmt.Fprint(&sb, v)
		}
		fmt.Fprint(&sb, "\n")
	}
	return []byte(sb.String()), nil
}

// ----------------- small helpers to avoid import churn -----------------

// bytesTrim is a small local implementation of bytes.TrimSpace to avoid an
// extra import in this file. It trims common ASCII whitespace from both ends
// of the provided byte slice.
func bytesTrim(b []byte) []byte {
	i := 0
	j := len(b)
	for i < j {
		c := b[i]
		if c == ' ' || c == '\n' || c == '\t' || c == '\r' || c == '\f' || c == '\v' {
			i++
			continue
		}
		break
	}
	for j > i {
		c := b[j-1]
		if c == ' ' || c == '\n' || c == '\t' || c == '\r' || c == '\f' || c == '\v' {
			j--
			continue
		}
		break
	}
	return b[i:j]
}
