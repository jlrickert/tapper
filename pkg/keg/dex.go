package keg

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// IndexBuilder is a small abstraction for building a single index artifact.
// Implementations may be registered with a higher-level service or invoked
// directly by BuildIndexes. The Build method returns the bytes to write and a
// suggested index filename.
type IndexBuilder interface {
	// Name returns a short canonical name for the index (e.g., "dex/tags").
	Name() string

	// Build builds the index content from the repository and returns the bytes.
	Build(ctx context.Context, repo KegRepository) ([]byte, error)
}

// Dex holds in-memory representations of the common dex indices.
// It is a convenience high-level view used by index builders and tools.
type Dex struct {
	Nodes     []NodeRef           // list of nodes (id, title, updated)
	Tags      map[string][]NodeID // tag -> member node ids (sorted unique)
	Links     map[NodeID][]NodeID // src -> dst node ids (sorted unique)
	Backlinks map[NodeID][]NodeID // dst -> src node ids (sorted unique)
}

// ReadFromDex attempts to load index artifacts from the repository
// ("nodes.tsv", "tags", "links", "backlinks") and parse them into a Dex
// structure. Missing index files are treated as empty datasets (no error).
func ReadFromDex(repo KegRepository) (*Dex, error) {
	d := &Dex{
		Tags:      make(map[string][]NodeID),
		Links:     make(map[NodeID][]NodeID),
		Backlinks: make(map[NodeID][]NodeID),
	}

	// Helper to optionally read an index (missing -> nil, err -> propagate).
	readOpt := func(name string) ([]byte, error) {
		b, err := repo.GetIndex(name)
		if err != nil {
			// repo implementations return ErrMetaNotFound for missing index
			if IsNotFound(err) || errorsIsMetaNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return b, nil
	}

	// nodes.tsv
	if data, err := readOpt("nodes.tsv"); err != nil {
		return nil, fmt.Errorf("read nodes.tsv: %w", err)
	} else if len(bytesTrimSpace(data)) > 0 {
		nodes, err := parseNodesIndex(data)
		if err != nil {
			return nil, fmt.Errorf("parse nodes.tsv: %w", err)
		}
		d.Nodes = nodes
	}

	// tags
	if data, err := readOpt("tags"); err != nil {
		return nil, fmt.Errorf("read tags: %w", err)
	} else if len(bytesTrimSpace(data)) > 0 {
		tags, err := parseTagsIndex(data)
		if err != nil {
			return nil, fmt.Errorf("parse tags: %w", err)
		}
		d.Tags = tags
	}

	// links
	if data, err := readOpt("links"); err != nil {
		return nil, fmt.Errorf("read links: %w", err)
	} else if len(bytesTrimSpace(data)) > 0 {
		links, err := parseLinksIndex(data)
		if err != nil {
			return nil, fmt.Errorf("parse links: %w", err)
		}
		d.Links = links
	}

	// backlinks
	if data, err := readOpt("backlinks"); err != nil {
		return nil, fmt.Errorf("read backlinks: %w", err)
	} else if len(bytesTrimSpace(data)) > 0 {
		back, err := parseBacklinksIndex(data)
		if err != nil {
			return nil, fmt.Errorf("parse backlinks: %w", err)
		}
		d.Backlinks = back
	}

	return d, nil
}

// BuildFromRepo scans the repository and constructs a Dex representation from
// authoritative sources (node meta and content). It is used by index builders
// that need to regenerate indices from node data.
func BuildFromRepo(repo KegRepository) (*Dex, error) {
	d := &Dex{
		Tags:      make(map[string][]NodeID),
		Links:     make(map[NodeID][]NodeID),
		Backlinks: make(map[NodeID][]NodeID),
	}

	// enumerate nodes using ListNodes (prefer ListNodes to get titles/updated)
	nodes, err := repo.ListNodes()
	if err != nil {
		return nil, NewBackendError("repo", "ListNodes", 0, err, false)
	}
	d.Nodes = nodes

	// Build tag map from meta for each node id. Use ListNodesID when available.
	ids, err := repo.ListNodesID()
	if err != nil {
		return nil, NewBackendError("repo", "ListNodesID", 0, err, false)
	}

	for _, id := range ids {
		// Read meta (meta may be missing; skip)
		metaBytes, err := repo.ReadMeta(id)
		if err == nil && len(bytesTrimSpace(metaBytes)) > 0 {
			meta, perr := ParseMeta(metaBytes)
			if perr == nil {
				for _, tag := range meta.Tags() {
					if tag == "" {
						continue
					}
					d.Tags[tag] = append(d.Tags[tag], id)
				}
			}
		} else if err != nil && !IsMetaNotFound(err) {
			// treat unexpected repo errors as backend errors
			return nil, NewBackendError("repo", "ReadMeta", 0, err, false)
		}

		// Read content and extract numeric ../N links (content may be absent)
		content, cerr := repo.ReadContent(id)
		if cerr != nil {
			// if node not found it is unexpected because we enumerated ids above
			if !IsNotFound(cerr) {
				return nil, NewBackendError("repo", "ReadContent", 0, cerr, false)
			}
			continue
		}
		if len(bytesTrimSpace(content)) > 0 {
			cont, perr := ParseContent(content, "README.md")
			if perr == nil && len(cont.Links) > 0 {
				dstSet := map[int]struct{}{}
				for _, dst := range cont.Links {
					dstSet[int(dst)] = struct{}{}
				}
				// append unique dsts
				for dst := range dstSet {
					d.Links[id] = append(d.Links[id], NodeID(dst))
				}
			}
		}
	}

	// Normalize tags: dedupe & sort
	for tag, arr := range d.Tags {
		set := map[int]struct{}{}
		for _, v := range arr {
			set[int(v)] = struct{}{}
		}
		ints := make([]int, 0, len(set))
		for k := range set {
			ints = append(ints, k)
		}
		sort.Ints(ints)
		out := make([]NodeID, len(ints))
		for i, v := range ints {
			out[i] = NodeID(v)
		}
		d.Tags[tag] = out
	}

	// Normalize links: dedupe & sort destinations, ensure sources with no dsts can be empty slice
	for src, arr := range d.Links {
		set := map[int]struct{}{}
		for _, v := range arr {
			set[int(v)] = struct{}{}
		}
		ints := make([]int, 0, len(set))
		for k := range set {
			ints = append(ints, k)
		}
		sort.Ints(ints)
		out := make([]NodeID, len(ints))
		for i, v := range ints {
			out[i] = NodeID(v)
		}
		d.Links[src] = out
	}

	// Build backlinks by reversing links
	for src, dsts := range d.Links {
		for _, dst := range dsts {
			d.Backlinks[dst] = append(d.Backlinks[dst], src)
		}
	}
	// Ensure every node appears in backlinks map (possibly empty)
	for _, n := range d.Nodes {
		if _, ok := d.Backlinks[n.ID]; !ok {
			d.Backlinks[n.ID] = []NodeID{}
		}
	}
	// Normalize backlinks: dedupe & sort
	for dst, arr := range d.Backlinks {
		set := map[int]struct{}{}
		for _, v := range arr {
			set[int(v)] = struct{}{}
		}
		ints := make([]int, 0, len(set))
		for k := range set {
			ints = append(ints, k)
		}
		sort.Ints(ints)
		out := make([]NodeID, len(ints))
		for i, v := range ints {
			out[i] = NodeID(v)
		}
		d.Backlinks[dst] = out
	}

	return d, nil
}

// ----------------- Index parsing helpers -----------------

func parseNodesIndex(data []byte) ([]NodeRef, error) {
	var out []NodeRef
	lines := strings.Split(string(data), "\n")
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		parts := strings.SplitN(ln, "\t", 3)
		if len(parts) < 3 {
			// skip malformed line
			continue
		}
		idNum, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			continue
		}
		updatedStr := strings.TrimSpace(parts[1])
		var mod time.Time
		if updatedStr != "" {
			if t, err := time.Parse(time.RFC3339, updatedStr); err == nil {
				mod = t
			} else {
				// try legacy format
				const alt = "2006-01-02 15:04:05Z"
				if t2, err2 := time.Parse(alt, updatedStr); err2 == nil {
					mod = t2
				}
			}
		}
		title := strings.TrimSpace(parts[2])
		out = append(out, NodeRef{
			ID:      NodeID(idNum),
			Title:   title,
			Updated: mod,
		})
	}
	return out, nil
}

func parseTagsIndex(data []byte) (map[string][]NodeID, error) {
	out := make(map[string][]NodeID)
	lines := strings.Split(string(data), "\n")
	for _, ln := range lines {
		s := strings.TrimSpace(ln)
		if s == "" {
			continue
		}
		fields := strings.Fields(s)
		if len(fields) == 0 {
			continue
		}
		tag := fields[0]
		if len(fields) > 1 {
			for _, tok := range fields[1:] {
				if n, err := strconv.Atoi(strings.TrimSpace(tok)); err == nil {
					out[tag] = append(out[tag], NodeID(n))
				}
			}
		} else {
			if _, ok := out[tag]; !ok {
				out[tag] = []NodeID{}
			}
		}
	}
	return out, nil
}

func parseLinksIndex(data []byte) (map[NodeID][]NodeID, error) {
	out := make(map[NodeID][]NodeID)
	lines := strings.Split(string(data), "\n")
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		parts := strings.SplitN(ln, "\t", 2)
		if len(parts) == 0 {
			continue
		}
		idNum, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			continue
		}
		var rest string
		if len(parts) > 1 {
			rest = strings.TrimSpace(parts[1])
		}
		if rest == "" {
			out[NodeID(idNum)] = []NodeID{}
			continue
		}
		toks := strings.Fields(rest)
		for _, tk := range toks {
			if n, err := strconv.Atoi(strings.TrimSpace(tk)); err == nil {
				out[NodeID(idNum)] = append(out[NodeID(idNum)], NodeID(n))
			}
		}
	}
	return out, nil
}

func parseBacklinksIndex(data []byte) (map[NodeID][]NodeID, error) {
	out := make(map[NodeID][]NodeID)
	lines := strings.Split(string(data), "\n")
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		parts := strings.SplitN(ln, "\t", 2)
		if len(parts) == 0 {
			continue
		}
		idNum, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			continue
		}
		var rest string
		if len(parts) > 1 {
			rest = strings.TrimSpace(parts[1])
		}
		if rest == "" {
			out[NodeID(idNum)] = []NodeID{}
			continue
		}
		toks := strings.Fields(rest)
		for _, tk := range toks {
			if n, err := strconv.Atoi(strings.TrimSpace(tk)); err == nil {
				out[NodeID(idNum)] = append(out[NodeID(idNum)], NodeID(n))
			}
		}
	}
	return out, nil
}

// ----------------- Serialization helpers -----------------

func (d *Dex) NodesTSV() []byte {
	var b strings.Builder
	for _, n := range d.Nodes {
		id := int(n.ID)
		updated := ""
		if !n.Updated.IsZero() {
			updated = n.Updated.UTC().Format("2006-01-02 15:04:05Z")
		}
		title := strings.ReplaceAll(strings.TrimSpace(n.Title), "\t", " ")
		fmt.Fprintf(&b, "%d\t%s\t%s\n", id, updated, title)
	}
	return []byte(b.String())
}

func (d *Dex) TagsText() []byte {
	// collect and sort tag keys
	tags := make([]string, 0, len(d.Tags))
	for k := range d.Tags {
		tags = append(tags, k)
	}
	sort.Strings(tags)
	var b strings.Builder
	for _, tag := range tags {
		ids := d.Tags[tag]
		if len(ids) == 0 {
			fmt.Fprintf(&b, "%s\n", tag)
			continue
		}
		fmt.Fprintf(&b, "%s", tag)
		for _, id := range ids {
			fmt.Fprintf(&b, " %d", int(id))
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func (d *Dex) LinksTSV() []byte {
	// collect src keys and sort
	srcs := make([]int, 0, len(d.Links))
	for s := range d.Links {
		srcs = append(srcs, int(s))
	}
	sort.Ints(srcs)
	var b strings.Builder
	for _, s := range srcs {
		dstList := d.Links[NodeID(s)]
		if len(dstList) == 0 {
			fmt.Fprintf(&b, "%d\t\n", s)
			continue
		}
		fmt.Fprintf(&b, "%d\t", s)
		for i, ddd := range dstList {
			if i > 0 {
				b.WriteByte(' ')
			}
			fmt.Fprintf(&b, "%d", int(ddd))
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func (d *Dex) BacklinksTSV() []byte {
	dests := make([]int, 0, len(d.Backlinks))
	for dst := range d.Backlinks {
		dests = append(dests, int(dst))
	}
	sort.Ints(dests)
	var b strings.Builder
	for _, dst := range dests {
		srcList := d.Backlinks[NodeID(dst)]
		if len(srcList) == 0 {
			fmt.Fprintf(&b, "%d\t\n", dst)
			continue
		}
		fmt.Fprintf(&b, "%d\t", dst)
		for i, s := range srcList {
			if i > 0 {
				b.WriteByte(' ')
			}
			fmt.Fprintf(&b, "%d", int(s))
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// ----------------- IndexBuilder implementations -----------------

// NodesIndexBuilder builds "nodes.tsv".
type NodesIndexBuilder struct{}

var _ IndexBuilder = (*NodesIndexBuilder)(nil)

func (NodesIndexBuilder) Name() string { return "nodes.tsv" }
func (NodesIndexBuilder) Build(_ context.Context, repo KegRepository) ([]byte, error) {
	// prefer ListNodes since it returns titles and timestamps
	nodes, err := repo.ListNodes()
	if err != nil {
		return nil, NewBackendError("repo", "ListNodes", 0, err, false)
	}
	d := &Dex{Nodes: nodes}
	return d.NodesTSV(), nil
}

// TagsIndexBuilder builds "tags".
type TagsIndexBuilder struct{}

var _ IndexBuilder = (*TagsIndexBuilder)(nil)

func (TagsIndexBuilder) Name() string { return "tags" }
func (TagsIndexBuilder) Build(_ context.Context, repo KegRepository) ([]byte, error) {
	d, err := BuildFromRepo(repo)
	if err != nil {
		return nil, err
	}
	return d.TagsText(), nil
}

// LinksIndexBuilder builds "links".
type LinksIndexBuilder struct{}

var _ IndexBuilder = (*LinksIndexBuilder)(nil)

func (LinksIndexBuilder) Name() string { return "links" }
func (LinksIndexBuilder) Build(_ context.Context, repo KegRepository) ([]byte, error) {
	d, err := BuildFromRepo(repo)
	if err != nil {
		return nil, err
	}
	return d.LinksTSV(), nil
}

// BacklinksIndexBuilder builds "backlinks".
type BacklinksIndexBuilder struct{}

var _ IndexBuilder = (*BacklinksIndexBuilder)(nil)

func (BacklinksIndexBuilder) Name() string { return "backlinks" }
func (BacklinksIndexBuilder) Build(_ context.Context, repo KegRepository) ([]byte, error) {
	d, err := BuildFromRepo(repo)
	if err != nil {
		return nil, err
	}
	return d.BacklinksTSV(), nil
}

// ----------------- small helpers to avoid import churn -----------------

// bytesTrimSpace avoids importing bytes in many places; inline tiny helper.
func bytesTrimSpace(b []byte) []byte { return bytesTrim(b) }

func bytesTrim(b []byte) []byte {
	return bytesTrimFunc(b)
}

func bytesTrimFunc(b []byte) []byte {
	// quick implementation of bytes.TrimSpace to avoid adding an import in some
	// environments; use standard library if you prefer.
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
