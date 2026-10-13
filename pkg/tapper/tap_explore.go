package tapper

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// ExploreKind names one section of a hub's public directory.
type ExploreKind string

const (
	// ExploreKegs lists public KEGs.
	ExploreKegs ExploreKind = "kegs"
	// ExploreFlights lists public flights.
	ExploreFlights ExploreKind = "flights"
	// ExplorePeople lists the people and org namespaces on the hub.
	ExplorePeople ExploreKind = "people"
)

// ExploreOptions searches one section of the public directory. Query must
// not be empty: the directory is hub-wide, and the caller's own resources
// come from the list commands instead.
type ExploreOptions struct {
	Query string
	Hub   string
}

// ExploreRow is one public directory entry. Role is the caller's role when
// they already belong to it, empty otherwise. A row grants nothing.
type ExploreRow struct {
	Ref         string `json:"ref"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Role        string `json:"role,omitempty"`
}

// ExploreResult is a bounded page of public directory matches.
type ExploreResult struct {
	Kind      ExploreKind  `json:"kind"`
	Results   []ExploreRow `json:"results"`
	Truncated bool         `json:"truncated"`
}

// ExploreRequest is the body of POST /api/v1/explore.
type ExploreRequest struct {
	Kind  ExploreKind `json:"kind"`
	Query string      `json:"query"`
}

// ValidExploreKind reports whether kind names a directory section.
func ValidExploreKind(kind ExploreKind) bool {
	switch kind {
	case ExploreKegs, ExploreFlights, ExplorePeople:
		return true
	}
	return false
}

// ExploreKegs searches the hub's public KEGs.
func (t *Tap) ExploreKegs(ctx context.Context, opts ExploreOptions) (ExploreResult, error) {
	return t.explore(ctx, ExploreKegs, opts)
}

// ExploreFlights searches the hub's public flights.
func (t *Tap) ExploreFlights(ctx context.Context, opts ExploreOptions) (ExploreResult, error) {
	return t.explore(ctx, ExploreFlights, opts)
}

// ExplorePeople searches the people and org namespaces on the hub.
func (t *Tap) ExplorePeople(ctx context.Context, opts ExploreOptions) (ExploreResult, error) {
	return t.explore(ctx, ExplorePeople, opts)
}

func (t *Tap) explore(ctx context.Context, kind ExploreKind, opts ExploreOptions) (ExploreResult, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return ExploreResult{}, fmt.Errorf("%w: query must not be empty", keg.ErrInvalid)
	}
	hubURL, token, err := t.resolveHubEndpoint(opts.Hub)
	if err != nil {
		return ExploreResult{}, err
	}
	return ExploreHub(ctx, hubURL, token, kind, query)
}

// ExploreHub searches one section of a hub's public directory via
// POST /api/v1/explore.
func ExploreHub(ctx context.Context, hubURL, token string, kind ExploreKind, query string) (ExploreResult, error) {
	var out ExploreResult
	if err := doHubJSON(ctx, http.MethodPost, hubURL, token, "/api/v1/explore", ExploreRequest{Kind: kind, Query: query}, &out); err != nil {
		return ExploreResult{}, err
	}
	if out.Kind == "" {
		out.Kind = kind
	}
	if out.Results == nil {
		out.Results = []ExploreRow{}
	}
	return out, nil
}
