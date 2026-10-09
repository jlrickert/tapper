package tapper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/jlrickert/tapper/pkg/apicontract"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapapi"
)

// ErrRankedSearchUnavailable reports that ranked search cannot run for the
// target: it is not a Hub KEG, or its Hub predates ranked search. Regex
// matching (Tap.Grep, `tap node grep`, node_grep) still works in both cases.
var ErrRankedSearchUnavailable = errors.New("ranked search unavailable")

// searchResponseLimit bounds how much of a search response is read.
const searchResponseLimit = 8 << 20

// SearchOptions configures ranked node search.
//
// With Keg set, the search covers that one Hub KEG. Otherwise it covers every
// KEG the caller may read on the selected Hub, narrowed to Kegs when given and
// to the active flight's cover unless BypassFlightRestrictions is set.
type SearchOptions struct {
	KegTargetOptions

	// Kegs narrows a whole-Hub search to these "@namespace/alias" references.
	// It cannot be combined with Keg.
	Kegs []string

	// Query is plain terms, "quoted phrases", and -exclusions. The Hub parses
	// it.
	Query string

	// Cursor continues the page a previous response's NextCursor named. It is
	// opaque and sent unchanged.
	Cursor string

	// Limit caps the hits returned; 0 lets the Hub choose. The Hub owns the
	// default and the maximum.
	Limit int
}

// Validate checks the request shape every surface shares and returns the
// canonical, de-duplicated Kegs. A provider serving search in-process calls it
// before running the search so its surface refuses what Tap.Search refuses.
func (o SearchOptions) Validate() ([]string, error) {
	if strings.TrimSpace(o.Query) == "" {
		return nil, fmt.Errorf("search query must not be empty: %w", keg.ErrInvalid)
	}
	if o.Limit < 0 {
		return nil, fmt.Errorf("search limit must be >= 0: %w", keg.ErrInvalid)
	}
	if strings.TrimSpace(o.Keg) != "" && len(o.Kegs) > 0 {
		return nil, fmt.Errorf("search one KEG with keg or several with kegs, not both: %w", keg.ErrInvalid)
	}
	kegs := make([]string, 0, len(o.Kegs))
	seen := make(map[string]bool, len(o.Kegs))
	for _, raw := range o.Kegs {
		namespace, alias, err := ParseCanonicalKegRef(raw)
		if err != nil {
			return nil, err
		}
		ref := "@" + namespace + "/" + alias
		if !seen[ref] {
			seen[ref] = true
			kegs = append(kegs, ref)
		}
	}
	return kegs, nil
}

// Search ranks nodes by relevance to opts.Query. The Hub scores, pages, and
// authorizes; the client only chooses the KEG scope.
func (t *Tap) Search(ctx context.Context, opts SearchOptions) (*tapapi.SearchResponse, error) {
	kegs, err := opts.Validate()
	if err != nil {
		return nil, err
	}
	if opts.RequireBootstrap && !t.ConfigService.UserConfigExists() {
		return nil, ErrNotBootstrapped
	}
	req := tapapi.SearchRequest{Query: opts.Query, Cursor: opts.Cursor, Limit: opts.Limit}
	flight, err := t.searchFlight(ctx, opts.KegTargetOptions)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(opts.Keg) != "" {
		return t.searchOneKeg(ctx, opts.KegTargetOptions, flight, req)
	}

	scope := searchScope(kegs, flight)
	if scope.restricted && len(scope.kegs) == 0 {
		// An empty KEG list asks the Hub for every readable KEG, so a scope
		// that narrowed to nothing must never reach it.
		return &tapapi.SearchResponse{Hits: []tapapi.SearchHit{}, Warnings: scope.warnings}, nil
	}
	req.Kegs = scope.kegs
	hubURL, token, err := t.resolveHubEndpoint(opts.Hub)
	if err != nil {
		return nil, err
	}
	resp, err := postHubSearch(ctx, hubURL, token, req)
	if err != nil {
		return nil, err
	}
	resp.Warnings = append(scope.warnings, resp.Warnings...)
	return resp, nil
}

// searchOneKeg searches the single KEG target names. It resolves the KEG's Hub
// from configuration without opening the KEG: the Hub authorizes the KEG
// itself, and the client applies only the active flight's cover.
func (t *Tap) searchOneKeg(ctx context.Context, opts KegTargetOptions, flight *Flight, req tapapi.SearchRequest) (*tapapi.SearchResponse, error) {
	target, err := t.ConfigService.ResolveTarget(strings.TrimSpace(opts.Keg), opts.Namespace, opts.Hub)
	if err != nil {
		return nil, err
	}
	ref := searchKegRef(target)
	if strings.TrimSpace(target.HubURL) == "" || ref == "" {
		return nil, fmt.Errorf("%w: %q is not a Hub KEG; use tap node grep for regex matching", ErrRankedSearchUnavailable, opts.Keg)
	}
	if err := t.enforceFlightTarget(flight, target, FlightRoleViewer); err != nil {
		return nil, err
	}
	token := t.hubTokenForTarget(target)
	if token == "" {
		return nil, fmt.Errorf("not logged in to hub %q (run `tap auth login --hub %s`)", target.Hub, target.HubURL)
	}
	req.Kegs = []string{ref}
	return postHubSearch(ctx, target.HubURL, token, req)
}

// searchKegRef names a resolved target as "@namespace/alias": from its
// namespace and name, or for a KEG endpoint URL, from the Hub's per-KEG route
// /api/v1/@namespace/kegs/alias. It returns "" for anything else.
func searchKegRef(target *keg.Target) string {
	if ref := CanonicalKegRef(target); ref != "" {
		return ref
	}
	_, route, ok := strings.Cut(target.Url, "/api/v1/@")
	if !ok {
		return ""
	}
	parts := strings.Split(strings.Trim(route, "/"), "/")
	if len(parts) != 3 || parts[1] != "kegs" {
		return ""
	}
	namespace, alias, err := ParseCanonicalKegRef("@" + parts[0] + "/" + parts[2])
	if err != nil {
		return ""
	}
	return "@" + namespace + "/" + alias
}

// searchFlight returns the composed flight a search is restricted to, or nil:
// the call's resolved snapshot, else the named flight.
func (t *Tap) searchFlight(ctx context.Context, opts KegTargetOptions) (*Flight, error) {
	if opts.BypassFlightRestrictions {
		return nil, nil
	}
	if opts.FlightContext != nil {
		return ComposedFlight(opts.FlightContext), nil
	}
	if strings.TrimSpace(opts.Flight) == "" || t.FlightService == nil {
		return nil, nil
	}
	flight, err := t.FlightService.GetFlight(ctx, opts.Flight)
	if err != nil {
		return nil, err
	}
	return ComposedFlight(flight), nil
}

// searchScopeResult is the KEG list a whole-Hub search sends. restricted marks
// a deliberate allowlist, where an empty list means "nothing", not
// "everything".
type searchScopeResult struct {
	kegs       []string
	restricted bool
	warnings   []string
}

// searchScope narrows explicit KEGs to the flight's cover, or without explicit
// KEGs, names the flight's cover.
func searchScope(explicit []string, flight *Flight) searchScopeResult {
	if flight == nil {
		return searchScopeResult{kegs: explicit, restricted: len(explicit) > 0}
	}
	out := searchScopeResult{kegs: []string{}, restricted: true}
	if len(explicit) > 0 {
		for _, ref := range explicit {
			namespace, alias, _ := strings.Cut(strings.TrimPrefix(ref, "@"), "/")
			if role, ok := flight.RoleFor("", namespace, alias); ok && role.AtLeast(FlightRoleViewer) {
				out.kegs = append(out.kegs, ref)
				continue
			}
			out.warnings = append(out.warnings, fmt.Sprintf("%s is not in flight %s's cover and was not searched", ref, flight.Name))
		}
		return out
	}
	seen := map[string]bool{}
	for _, c := range flight.Cover {
		namespace := strings.TrimPrefix(strings.TrimSpace(c.Namespace), "@")
		alias := strings.TrimSpace(c.Keg)
		if alias == "" {
			continue
		}
		if namespace == "" {
			out.warnings = append(out.warnings, fmt.Sprintf("cover entry %q names no namespace and was not searched", alias))
			continue
		}
		ref := "@" + namespace + "/" + alias
		if !seen[ref] {
			seen[ref] = true
			out.kegs = append(out.kegs, ref)
		}
	}
	return out
}

// postHubSearch sends one ranked-search request to the Hub's TAP search route
// with the caller's credentials and, inside a governed MCP call, its
// orientation proof.
func postHubSearch(ctx context.Context, hubURL, token string, in tapapi.SearchRequest) (*tapapi.SearchResponse, error) {
	base, err := normalizeHubURL(hubURL)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("hub: encode search request: %w", err)
	}
	endpoint := base + "/api/v1" + tapapi.PathSearch
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("hub: build search request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if value, ok := keg.OrientationHeaderForURL(ctx, endpoint); ok {
		req.Header.Set(keg.OrientationHeaderName, value)
	} else if state, governed := keg.OrientationStateFromContext(ctx); governed && state.RootHub != "" {
		// A governed call never searches a Hub its proof cannot reach: without
		// the proof that Hub would answer at the identity's full authority.
		return nil, fmt.Errorf("%w: search target is outside the pinned Hub", keg.ErrOrientationDenied)
	}

	resp, err := apicontract.Do(hubHTTPClient(), req)
	if err != nil {
		return nil, fmt.Errorf("hub: contact hub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, searchResponseLimit))
	if err != nil {
		return nil, fmt.Errorf("hub: read search response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, searchError(resp.StatusCode, body, len(in.Kegs) == 0)
	}
	var out tapapi.SearchResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("hub: parse search response: %w", err)
	}
	if out.Hits == nil {
		out.Hits = []tapapi.SearchHit{}
	}
	return &out, nil
}

// searchError maps a failed search response to the shared sentinels. It is the
// only place search statuses are interpreted.
//
// A Hub that predates ranked search has no route for it. A 405, a 404 without
// the Hub's error envelope, or any 404 for a whole-Hub request (which names no
// KEG that could be missing) can only mean that.
func searchError(status int, body []byte, wholeHub bool) error {
	var envelope struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	_ = json.Unmarshal(body, &envelope)
	missingRoute := status == http.StatusMethodNotAllowed ||
		(status == http.StatusNotFound && (envelope.Code == "" || wholeHub))
	switch {
	case missingRoute:
		return fmt.Errorf("%w: this Hub does not offer ranked search; upgrade the Hub, or use tap node grep (node_grep) for regex matching", ErrRankedSearchUnavailable)
	case status == http.StatusUnauthorized:
		return fmt.Errorf("hub: %w", ErrTokenRejected)
	}
	msg := strings.TrimSpace(envelope.Error)
	if msg == "" {
		msg = fmt.Sprintf("search failed: %d %s", status, http.StatusText(status))
	}
	return fmt.Errorf("hub: %w", keg.RemoteErrorFromCode(envelope.Code, status, msg))
}

// HighlightSnippet wraps each highlight span of snippet, given in rune
// offsets, in open and close markers. Spans are applied in order; a span that
// is empty, inverted, out of range, or overlaps an earlier one is clamped or
// skipped rather than trusted.
func HighlightSnippet(snippet string, spans []tapapi.Span, open, close string) string {
	if len(spans) == 0 {
		return snippet
	}
	runes := []rune(snippet)
	var b strings.Builder
	pos := 0
	for _, span := range spans {
		start, end := max(span.Start, pos), min(span.End, len(runes))
		if start >= end {
			continue
		}
		b.WriteString(string(runes[pos:start]))
		b.WriteString(open)
		b.WriteString(string(runes[start:end]))
		b.WriteString(close)
		pos = end
	}
	b.WriteString(string(runes[pos:]))
	return b.String()
}

// SearchHitLines renders one tab-separated line per hit: reference, score,
// title, and the snippet with its highlights wrapped in open and close. CLI and
// MCP share it so both surfaces show a hit the same way.
func SearchHitLines(hits []tapapi.SearchHit, open, close string) []string {
	lines := make([]string, 0, len(hits))
	for _, hit := range hits {
		lines = append(lines, strings.Join([]string{
			hit.Ref,
			strconv.FormatFloat(hit.Score, 'f', 3, 64),
			searchLineField(hit.Title),
			searchLineField(HighlightSnippet(hit.Snippet, hit.Highlights, open, close)),
		}, "\t"))
	}
	return lines
}

// searchLineField keeps a value on one tab-separated line.
func searchLineField(value string) string {
	return strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(value))
}
