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
	// NodeEventAccessed indicates a node's content or metadata was read.
	NodeEventAccessed
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
	case NodeEventAccessed:
		return "accessed"
	default:
		return "unknown"
	}
}

// NodeEvent describes a single change or access observed on a node. Field
// identifies which part of the node was affected: "content" for README.md,
// "meta" for meta.yaml, "stats" for stats.json, or "" when the event applies
// to the node as a whole (e.g. creation or deletion of the entire directory).
type NodeEvent struct {
	Kind   NodeEventKind
	NodeID NodeId
	Field  string // "content", "meta", "stats", or ""
}

// RepositoryEvents is an optional interface that Repository implementations
// may satisfy to provide live change notifications. Consumers use a type
// assertion to check whether the underlying repository supports events.
//
// Watch begins observing changes for the specified node IDs (or all nodes
// when no IDs are given). The watch is scoped to ctx: events are delivered
// on the returned channel until ctx is cancelled, and implementations must
// close the channel when observation ends. There is no separate teardown —
// cancelling ctx releases all per-watch resources.
type RepositoryEvents interface {
	// Watch begins observing the specified node ids, or all nodes when no ids
	// are supplied. It closes the returned channel and releases per-watch
	// resources when ctx is canceled.
	Watch(ctx context.Context, ids ...NodeId) (<-chan NodeEvent, error)
	// Emit sends a NodeEvent to all active subscribers whose filters match.
	// Repositories use it for programmatic events such as access tracking.
	Emit(ev NodeEvent)
}
