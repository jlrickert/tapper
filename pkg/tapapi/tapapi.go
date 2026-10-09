// Package tapapi defines the wire contract for complete TAP operations a Hub
// executes near the data, under /api/v1/tap/*.
//
// A per-KEG route performs one KEG operation. A TAP operation is a whole
// client-facing workflow (a relationship lookup plus the fields its listing
// renders, a tag listing, a multi-KEG read) that would otherwise take several
// round trips. The Hub resolves every KEG a request names through its KEG
// authority boundary; nothing here grants access.
//
// Rendering stays on the client: responses carry structured rows and resolved
// field values, never formatted lines.
package tapapi

import "github.com/jlrickert/tapper/pkg/keg"

// Paths of the TAP operations, relative to the Hub's /api/v1 root.
const (
	PathLinks     = "/tap/links"
	PathBacklinks = "/tap/backlinks"
	PathTags      = "/tap/tags"
	PathCat       = "/tap/cat"
	PathSearch    = "/tap/search"
)

// RelatedRequest asks for a node set's links or backlinks in one KEG, paged
// and with the named field selectors resolved for every returned row.
type RelatedRequest struct {
	// Keg is the qualified KEG reference, "@namespace/alias".
	Keg     string   `json:"keg"`
	NodeIDs []string `json:"node_ids"`
	Fields  []string `json:"fields,omitempty"`
	Offset  int      `json:"offset,omitempty"`
	Limit   int      `json:"limit,omitempty"`
}

// RelatedResponse is one page of related nodes. Pairs attributes every
// returned row to the input node that produced it; Total counts related
// nodes before paging.
type RelatedResponse struct {
	Rows  []keg.ListViewRow `json:"rows"`
	Pairs []keg.RelatedPair `json:"pairs"`
	Total int               `json:"total"`
}

// TagsRequest lists tags, or with a Query, the nodes it selects rendered as
// rows with the named fields resolved.
type TagsRequest struct {
	Keg    string   `json:"keg"`
	Query  string   `json:"query,omitempty"`
	Fields []string `json:"fields,omitempty"`
	Offset int      `json:"offset,omitempty"`
	Limit  int      `json:"limit,omitempty"`
}

// TagsResponse carries Tags when the request had no query, otherwise Rows.
// Total counts before paging.
type TagsResponse struct {
	Tags  []string          `json:"tags,omitempty"`
	Rows  []keg.ListViewRow `json:"rows,omitempty"`
	Total int               `json:"total"`
}

// CatRequest reads nodes across any number of KEGs on this Hub in one call.
// Each reference is qualified, "keg:@namespace/alias/id". Touch records the
// read as an open.
type CatRequest struct {
	Nodes []string `json:"nodes"`
	Touch bool     `json:"touch,omitempty"`
}

// CatNode is one requested node, in request order. A node that cannot be read
// carries Error and Code instead of content, so one failure does not hide the
// rest of the batch.
type CatNode struct {
	Ref     string `json:"ref"`
	Content string `json:"content,omitempty"`
	Meta    string `json:"meta,omitempty"`
	Hash    string `json:"hash,omitempty"`
	Error   string `json:"error,omitempty"`
	Code    string `json:"code,omitempty"`
}

// CatResponse returns the requested nodes in request order.
type CatResponse struct {
	Nodes []CatNode `json:"nodes"`
}

// SearchRequest asks for nodes ranked by relevance to Query across every KEG
// the caller may read on this Hub, narrowed to Kegs when it is non-empty: an
// empty Kegs means every readable KEG. The Hub resolves each KEG through its
// authority boundary; naming a KEG only narrows and never grants access.
//
// Cursor continues a previous response's NextCursor; it is opaque and passed
// through unchanged. Limit 0 lets the Hub choose the page size. Defaults and
// clamping live only on the Hub.
type SearchRequest struct {
	Query string `json:"query"`
	// Kegs are qualified references, "@namespace/alias".
	Kegs   []string `json:"kegs,omitempty"`
	Cursor string   `json:"cursor,omitempty"`
	Limit  int      `json:"limit,omitempty"`
}

// Span is a highlighted range of a hit's Snippet in rune offsets: Start is
// inclusive and End exclusive.
type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Search hit fields name where a hit matched.
const (
	SearchFieldTitle = "title"
	SearchFieldMeta  = "meta"
	SearchFieldBody  = "body"
)

// Search hit sources name the ranking signals that found a hit.
const (
	SearchSourceKeyword  = "keyword"
	SearchSourceSemantic = "semantic"
)

// Search modes report how a response was ranked. SearchModeKeyword means
// semantic ranking was not used: the Hub has no embedding model or degraded.
const (
	SearchModeKeyword = "keyword"
	SearchModeHybrid  = "hybrid"
)

// SearchHit is one ranked node. Hits arrive in a total order: Score
// descending, then Keg, then NodeID.
type SearchHit struct {
	// Ref is the qualified node reference, "keg:@namespace/alias/id".
	Ref string `json:"ref"`
	// Keg is the qualified KEG reference, "@namespace/alias".
	Keg    string  `json:"keg"`
	NodeID string  `json:"node_id"`
	Title  string  `json:"title"`
	Score  float64 `json:"score"`
	// Fields lists where the node matched: SearchFieldTitle, SearchFieldMeta,
	// SearchFieldBody.
	Fields []string `json:"fields"`
	// Sources lists the signals that found the node: SearchSourceKeyword,
	// SearchSourceSemantic.
	Sources []string `json:"sources,omitempty"`
	Snippet string   `json:"snippet,omitempty"`
	// Highlights are the matched ranges of Snippet.
	Highlights []Span `json:"highlights,omitempty"`
}

// TermExpansion reports how the Hub interpreted one query term: the lexemes it
// searched for, and whether they came from typo expansion.
type TermExpansion struct {
	Term    string   `json:"term"`
	Lexemes []string `json:"lexemes"`
	Typo    bool     `json:"typo"`
}

// SearchResponse is one page of ranked hits. NextCursor is empty on the last
// page. Mode is SearchModeKeyword or SearchModeHybrid. Warnings report
// partial results the caller should know about.
type SearchResponse struct {
	Hits       []SearchHit     `json:"hits"`
	NextCursor string          `json:"next_cursor,omitempty"`
	Terms      []TermExpansion `json:"terms,omitempty"`
	Mode       string          `json:"mode,omitempty"`
	Warnings   []string        `json:"warnings,omitempty"`
}
