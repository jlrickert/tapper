package tapper

import (
	"context"
	"github.com/jlrickert/tapper/pkg/keg"
)

// Relationship identifies an actual resolved, directed link in a result page.
type Relationship struct{ Source, Target keg.NodeId }

// RelationshipResultObserver receives structured relationships after pagination
// and before formatting. Consumers must buffer until the enclosing operation
// succeeds; formatting can still fail. The owning Keg carries resolved identity.
type RelationshipResultObserver func(context.Context, keg.Keg, []Relationship)
type relationshipObserverKey struct{}

// WithRelationshipResultObserver scopes observation to one explicit operation.
func WithRelationshipResultObserver(ctx context.Context, observer RelationshipResultObserver) context.Context {
	return context.WithValue(ctx, relationshipObserverKey{}, observer)
}
