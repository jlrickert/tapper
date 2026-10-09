package tapper

import (
	"bytes"
	"context"
	"encoding/json"
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
// It matches keg.ErrNotSupported, so every surface reports NOT_SUPPORTED and
// points the caller at a supported tool rather than at a partial write.
var ErrRankedSearchUnavailable error = rankedSearchUnavailableError{}

type rankedSearchUnavailableError struct{}

func (rankedSearchUnavailableError) Error() string { return "ranked search unavailable" }

func (rankedSearchUnavailableError) Is(target error) bool { return target == keg.ErrNotSupported }

// searchResponseLimit bounds how much of a search response is read.
const searchResponseLimit = 8 << 20

// SearchOptions configures ranked node search.
//
// With Keg set, the search covers that one Hub KEG. Otherwise it covers every
// KEG the caller may read on the selected Hub, narrowed to Kegs when given and
// to the active flight's cover unless BypassFlightRestrictions is set. A
// governed call that carries the orientation proof leaves an unscoped search
// for the Hub to scope to the cover; otherwise the client names the cover,
// which must fit in tapapi.SearchMaxKegs.
type SearchOptions struct {
	KegTargetOptions

	// Kegs narrows a whole-Hub search to these "@namespace/alias" references,
	// at most tapapi.SearchMaxKegs of them. It cannot be combined with Keg.
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
	if len(kegs) > tapapi.SearchMaxKegs {
		return nil, fmt.Errorf("search names %d KEGs; one search can name at most %d, so split the kegs across searches: %w",
			len(kegs), tapapi.SearchMaxKegs, keg.ErrInvalid)
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
	hubURL, token, err := t.resolveHubEndpoint(opts.Hub)
	if err != nil {
		return nil, err
	}
	req.Kegs = scope.kegs
	if len(kegs) == 0 && flight != nil {
		if hubScopesToCover(ctx, hubURL) {
			// The orientation proof travels with this request, and the Hub
			// scopes an unscoped governed search to the proof's flight cover
			// itself. Naming the cover instead would trip the per-request KEG
			// cap for a wide flight.
			req.Kegs = nil
		} else if len(req.Kegs) > tapapi.SearchMaxKegs {
			return nil, fmt.Errorf("flight %s covers %d KEGs, more than the %d one search can name; narrow the search with kegs (--kegs): %w",
				flight.Name, len(req.Kegs), tapapi.SearchMaxKegs, keg.ErrInvalid)
		}
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
		return nil, fmt.Errorf("%w: %q is not a Hub KEG; use tap node grep (node_grep) for regex matching", ErrRankedSearchUnavailable, opts.Keg)
	}
	if err := t.enforceFlightTarget(flight, target, FlightRoleViewer); err != nil {
		return nil, err
	}
	token := t.hubTokenForTarget(target)
	if token == "" {
		return nil, notLoggedInError(target.Hub, target.HubURL)
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
// KEGs, names the flight's cover. Both read the cover through searchCover, so
// a KEG is searchable on exactly the same terms either way.
func searchScope(explicit []string, flight *Flight) searchScopeResult {
	if flight == nil {
		return searchScopeResult{kegs: explicit, restricted: len(explicit) > 0}
	}
	cover, warnings := searchCover(flight)
	out := searchScopeResult{kegs: []string{}, restricted: true, warnings: warnings}
	if len(explicit) == 0 {
		out.kegs = cover
		return out
	}
	covered := make(map[string]bool, len(cover))
	for _, ref := range cover {
		covered[ref] = true
	}
	for _, ref := range explicit {
		if covered[ref] {
			out.kegs = append(out.kegs, ref)
			continue
		}
		out.warnings = append(out.warnings, fmt.Sprintf("%s is not in flight %s's cover and was not searched", ref, flight.Name))
	}
	return out
}

// searchCover lists, in cover order and once each, the "@namespace/alias"
// references of the flight's cover a search may read: the entries RoleFor
// evaluates, legacy AllowedKegs included, at viewer or above. Ranked search
// names KEGs canonically, so a cover entry without a namespace cannot be
// searched; it is reported as a warning rather than matched against any
// namespace.
func searchCover(flight *Flight) (refs, warnings []string) {
	refs = []string{}
	seen := map[string]bool{}
	for _, c := range flight.coverEntries() {
		if c.Namespace == "" {
			warnings = append(warnings, fmt.Sprintf("cover entry %q names no namespace and was not searched", c.Keg))
			continue
		}
		if !normalizeFlightRole(c.Role).AtLeast(FlightRoleViewer) {
			continue
		}
		ref := "@" + c.Namespace + "/" + c.Keg
		if !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	return refs, warnings
}

// searchEndpoint is the Hub's TAP search route.
func searchEndpoint(hubURL string) (string, error) {
	base, err := normalizeHubURL(hubURL)
	if err != nil {
		return "", err
	}
	return base + "/api/v1" + tapapi.PathSearch, nil
}

// hubScopesToCover reports whether a search sent to hubURL carries the
// orientation proof, which makes the Hub scope an unscoped search to the
// proof's flight cover.
func hubScopesToCover(ctx context.Context, hubURL string) bool {
	endpoint, err := searchEndpoint(hubURL)
	if err != nil {
		return false
	}
	_, ok := keg.OrientationHeaderForURL(ctx, endpoint)
	return ok
}

// postHubSearch sends one ranked-search request to the Hub's TAP search route
// with the caller's credentials and, inside a governed MCP call, its
// orientation proof.
func postHubSearch(ctx context.Context, hubURL, token string, in tapapi.SearchRequest) (*tapapi.SearchResponse, error) {
	endpoint, err := searchEndpoint(hubURL)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("hub: encode search request: %w", err)
	}
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
		routed := resp.Header.Get(tapapi.SearchRouteHeader) == tapapi.SearchRouteHeaderValue
		return nil, searchError(resp.StatusCode, body, routed)
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
// routed reports the search route's tapapi.SearchRouteHeader. A Hub that
// predates ranked search has no route for it and answers 404 or 405 without
// the header, often with the same NOT_FOUND envelope the route uses for an
// unreadable KEG; only the header tells the two apart.
func searchError(status int, body []byte, routed bool) error {
	var envelope struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	_ = json.Unmarshal(body, &envelope)
	missingRoute := !routed && (status == http.StatusNotFound || status == http.StatusMethodNotAllowed)
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

// SearchNextCursorLine renders the line that hands a text reader the next
// page's cursor. The CLI prints it to stderr and node_search ends its text
// with it, so both surfaces show the cursor the same way.
func SearchNextCursorLine(cursor string) string {
	return "next cursor: " + cursor
}

// searchLineField keeps a value on one tab-separated line.
func searchLineField(value string) string {
	return strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(value))
}
