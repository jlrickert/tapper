package keg

import (
	"bytes"
	"strconv"
	"strings"
	"time"
)

type Dex struct {
	Nodes     []NodeRef
	Tags      map[string][]NodeID
	Links     map[NodeID][]NodeID
	Backlinks map[NodeID][]NodeID
}

// ReadFromDex loads and parses all index files from the [Keg] repository's dex
// directory into a unified [Dex] structure. This function reads nodes.tsv,
// tags, links, and backlinks indices, performing best-effort parsing that
// skips malformed lines rather than failing completely. Missing index files
// are treated as empty datasets rather than errors.
func ReadFromDex(keg *Keg) (*Dex, error) {
	dex := &Dex{
		Tags:      make(map[string][]NodeID),
		Links:     make(map[NodeID][]NodeID),
		Backlinks: make(map[NodeID][]NodeID),
	}

	// Helper to read an index file from the repository.
	readIndex := func(name string) ([]byte, error) {
		data, err := keg.repo.GetIndex(name)
		if err != nil {
			// Propagate repository errors; callers of ReadFromDex can wrap or
			// interpret them as appropriate.
			return nil, err
		}
		return data, nil
	}

	// Parse nodes.tsv -> populate dex.Nodes
	// Expected format: "<id>\t<updated>\t<title>\n"
	if data, err := readIndex("nodes.tsv"); err == nil && len(bytes.TrimSpace(data)) > 0 {
		lines := bytes.SplitSeq(bytes.TrimSpace(data), []byte{'\n'})
		for ln := range lines {
			ln = bytes.TrimSpace(ln)
			if len(ln) == 0 {
				continue
			}
			parts := bytes.SplitN(ln, []byte{'\t'}, 3)
			if len(parts) < 3 {
				// malformed line; skip it
				continue
			}

			// parse id
			idNum, err := strconv.Atoi(string(bytes.TrimSpace(parts[0])))
			if err != nil {
				// invalid id token; skip line
				continue
			}

			// parse updated timestamp (best-effort). Keep zero value if parse fails.
			updatedStr := string(bytes.TrimSpace(parts[1]))
			var mod time.Time
			if t, err := time.Parse("2006-01-02 15:04:05Z07:00", updatedStr); err == nil {
				mod = t
			}

			title := string(bytes.TrimSpace(parts[2]))

			dex.Nodes = append(dex.Nodes, NodeRef{
				Id:       NodeID(idNum),
				Modified: mod,
				Title:    title,
			})
		}
	} else if err != nil {
		return nil, err
	}

	// Helper to parse a tokenized list of numeric node ids into []NodeID.
	parseIDs := func(tokens []string) []NodeID {
		var out []NodeID
		for _, tk := range tokens {
			tk = strings.TrimSpace(tk)
			if tk == "" {
				continue
			}
			if n, err := strconv.Atoi(tk); err == nil {
				out = append(out, NodeID(n))
			}
		}
		return out
	}

	// Parse dex/tags -> collect tag names and their member node ids.
	// Tag index format: "tag id1 id2 ..." (space-separated).
	if data, err := readIndex("tags"); err == nil && len(bytes.TrimSpace(data)) > 0 {
		lines := bytes.SplitSeq(bytes.TrimSpace(data), []byte{'\n'})
		for ln := range lines {
			s := strings.TrimSpace(string(ln))
			if s == "" {
				continue
			}
			fields := strings.Fields(s)
			if len(fields) == 0 {
				continue
			}
			tag := fields[0]
			if len(fields) > 1 {
				dex.Tags[tag] = append(dex.Tags[tag], parseIDs(fields[1:])...)
			} else {
				// ensure the tag exists with an empty slice when no nodes are listed
				if _, ok := dex.Tags[tag]; !ok {
					dex.Tags[tag] = []NodeID{}
				}
			}
		}
	} else if err != nil {
		return nil, err
	}

	// Parse dex/links -> map[srcID][]dstID
	// Format per line: "<src>\t<dst1> <dst2> ...\n". If the destinations field
	// is empty, record an empty slice for the source to indicate "no outgoing links".
	if data, err := readIndex("links"); err == nil && len(bytes.TrimSpace(data)) > 0 {
		lines := bytes.SplitSeq(bytes.TrimSpace(data), []byte{'\n'})
		for ln := range lines {
			ln = bytes.TrimSpace(ln)
			if len(ln) == 0 {
				continue
			}
			parts := bytes.SplitN(ln, []byte{'\t'}, 2)
			if len(parts) == 0 {
				continue
			}
			idNum, err := strconv.Atoi(string(bytes.TrimSpace(parts[0])))
			if err != nil {
				// invalid source id; skip
				continue
			}
			var rest string
			if len(parts) > 1 {
				rest = strings.TrimSpace(string(parts[1]))
			}
			if rest == "" {
				dex.Links[NodeID(idNum)] = []NodeID{}
			} else {
				toks := strings.Fields(rest)
				dex.Links[NodeID(idNum)] = parseIDs(toks)
			}
		}
	} else if err != nil {
		return nil, err
	}

	// Parse dex/backlinks -> map[dstID][]srcID
	// Format per line: "<dst>\t<src1> <src2> ...\n".
	if data, err := readIndex("backlinks"); err == nil && len(bytes.TrimSpace(data)) > 0 {
		lines := bytes.SplitSeq(bytes.TrimSpace(data), []byte{'\n'})
		for ln := range lines {
			ln = bytes.TrimSpace(ln)
			if len(ln) == 0 {
				continue
			}
			parts := bytes.SplitN(ln, []byte{'\t'}, 2)
			if len(parts) == 0 {
				continue
			}
			idNum, err := strconv.Atoi(string(bytes.TrimSpace(parts[0])))
			if err != nil {
				// invalid destination id; skip
				continue
			}
			var rest string
			if len(parts) > 1 {
				rest = strings.TrimSpace(string(parts[1]))
			}
			if rest == "" {
				dex.Backlinks[NodeID(idNum)] = []NodeID{}
			} else {
				toks := strings.Fields(rest)
				dex.Backlinks[NodeID(idNum)] = parseIDs(toks)
			}
		}
	} else if err != nil {
		return nil, err
	}

	return dex, nil
}

func (dex *Dex) Index(keg *Keg) error {
	return nil
}

func (dex *Dex) updateTags(keg *Keg) error {
	return nil
}

func (dex *Dex) updateNodes(keg *Keg) error {
	return nil
}

func (dex *Dex) updateLinks(keg *Keg) error {
	return nil
}

func (dex *Dex) Update(fp string) error {
	return nil
}
