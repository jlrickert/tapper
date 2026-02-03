package keg

import (
	"context"
)

// KegRepository is the storage backend contract used by KEG. Implementations
// provide access to node content, metadata, indices, and auxiliary artifacts
// (images, attachments). Methods are context-aware (honor
// cancellation/deadlines) and should return well-typed errors (for example the
// sentinel errors defined in pkg/keg/errors.go) so callers can reliably use
// errors.Is / errors.As.
type KegRepository interface {
	// Name returns a short, human-friendly name for the backend
	// implementation.
	Name() string

	// Next returns the next available NodeID that can be allocated by the
	// repo. The operation is cancellable via ctx.
	Next(ctx context.Context) (NodeId, error)

	// ReadContent returns the primary content for the given node id (for
	// example README.md) as a byte slice. If the content cannot be read return
	// an error.
	ReadContent(ctx context.Context, id NodeId) ([]byte, error)

	// ReadMeta returns the serialized node metadata (for example meta.yaml)
	// for the specified node id. If metadata is missing or unreadable return
	// an appropriate typed error.
	ReadMeta(ctx context.Context, id NodeId) ([]byte, error)

	// ListNodes returns a slice of all node IDs present in the repository.
	ListNodes(ctx context.Context) ([]NodeId, error)

	// ListItems returns the list of ancillary item names (attachments)
	// associated with the given node id. Implementations should return
	// ErrNodeNotFound if the node does not exist.
	ListItems(ctx context.Context, id NodeId) ([]string, error)

	// ListImages returns the list of stored image names associated with the
	// given node id. Implementations should return ErrNodeNotFound if the node
	// does not exist.
	ListImages(ctx context.Context, id NodeId) ([]string, error)

	// WriteContent writes the primary content for the given node id.
	// Implementers should perform atomic writes where possible (write temp +
	// rename) and update any canonical timestamps as appropriate.
	WriteContent(ctx context.Context, id NodeId, data []byte) error

	// WriteMeta writes the node metadata (for example meta.yaml) for the node
	// id. Write operations should be atomic when possible and return typed
	// errors on failure.
	WriteMeta(ctx context.Context, id NodeId, data []byte) error

	// UploadImage stores an image blob associated with the node. Name is the
	// destination filename/key and data is the file bytes.
	UploadImage(ctx context.Context, id NodeId, name string, data []byte) error

	// UploadItem stores a named ancillary item (attachment) for the node.
	UploadItem(ctx context.Context, id NodeId, name string, data []byte) error

	// MoveNode renames or moves a node from id to dst. Implementations should
	// return ErrDestinationExists (or an equivalent typed error) if the
	// destination already exists, and should ensure the move is atomic when
	// possible.
	MoveNode(ctx context.Context, id NodeId, dst NodeId) error

	// GetIndex reads the raw contents of an index file by name (for example
	// "nodes.tsv" or "tags"). The returned bytes are a copy and callers should
	// not mutate them.
	GetIndex(ctx context.Context, name string) ([]byte, error)

	// ClearIndexes removes or resets the repository index artifacts (for
	// example files under dex/). Implementations may acquire a short-lived
	// repo-level lock for this operation; callers should expect the method to
	// honor ctx for cancellation and to return typed errors on failure.
	ClearIndexes(ctx context.Context) error

	// WriteIndex writes the raw contents of an index file. Implementations
	// should write atomically (temp + rename) to avoid exposing partial state
	// to readers.
	WriteIndex(ctx context.Context, name string, data []byte) error

	// ListIndexes returns a list of index names (for example dex files)
	// available from this repository. Returned names should be sorted for
	// determinism.
	ListIndexes(ctx context.Context) ([]string, error)

	// DeleteNode removes the node and all associated content/metadata/items.
	// If the node does not exist implementations should return a typed
	// NodeNotFoundError.
	DeleteNode(ctx context.Context, id NodeId) error

	// DeleteImage removes a stored image by name for the node. If the node
	// does not exist return NodeNotFoundError; if the image is not present
	// return an appropriate sentinel (for example ErrNotFound) or typed error
	// per policy.
	DeleteImage(ctx context.Context, id NodeId, name string) error

	// DeleteItem removes a named ancillary item by name for the node.
	DeleteItem(ctx context.Context, id NodeId, name string) error

	// ReadConfig reads and returns the repository-level configuration (Config).
	// Implementations should load and parse the stored configuration. If the
	// configuration is missing, an implementation may return ErrNotFound or a
	// more specific sentinel.
	ReadConfig(ctx context.Context) (*KegConfig, error)

	// WriteConfig persists the provided Config to the repository.
	// Implementations should write atomically where possible and validate
	// input as appropriate.
	WriteConfig(ctx context.Context, config *KegConfig) error
}
