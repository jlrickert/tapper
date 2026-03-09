package keg

import "context"

// NodeEventKind identifies the type of change that occurred on a node.
type NodeEventKind int

const (
	// NodeEventCreated indicates a new node was created.
	NodeEventCreated NodeEventKind = iota + 1
	// NodeEventModified indicates an existing node file was modified.
	NodeEventModified
	// NodeEventDeleted indicates a node or node file was removed.
	NodeEventDeleted
)

// String returns a human-readable label for the event kind.
func (k NodeEventKind) String() string {
	switch k {
	case NodeEventCreated:
		return "created"
	case NodeEventModified:
		return "modified"
	case NodeEventDeleted:
		return "deleted"
	default:
		return "unknown"
	}
}

// NodeEvent describes a single change observed on a node. Field identifies
// which part of the node changed: "content" for README.md, "meta" for
// meta.yaml, "stats" for stats.json, or "" when the change applies to the
// node as a whole (e.g. creation or deletion of the entire directory).
type NodeEvent struct {
	Kind   NodeEventKind
	NodeID NodeId
	Field  string // "content", "meta", "stats", or ""
}

// RepositoryEvents is an optional interface that Repository implementations
// may satisfy to provide live change notifications. The editing layer uses a
// type assertion to check whether the underlying repository supports events.
//
// Watch begins observing changes for the specified node IDs (or all nodes
// when no IDs are given). Events are delivered on the returned channel until
// the context is cancelled or Close is called. Implementations must close
// the channel when observation ends.
//
// Close releases all watcher resources. After Close returns, event channels
// are closed and no further events are delivered.
type RepositoryEvents interface {
	Watch(ctx context.Context, ids ...NodeId) (<-chan NodeEvent, error)
	Close() error
}
