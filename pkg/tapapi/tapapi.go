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
// Each reference is "@namespace/alias/id". Touch records the read as an open.
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
