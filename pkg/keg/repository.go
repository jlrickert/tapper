package keg

// KegRepository defines the storage backend contract used by KEG.
// Implementations provide access to node content, metadata, indices, and
// auxiliary artifacts (images, attachments). Methods should return well-typed
// errors (for example the sentinel errors defined in pkg/keg/errors.go) so
// callers can use errors.Is / errors.As for handling.
type KegRepository interface {
	// ReadContent returns the primary content for the given node id as a byte
	// slice. Implementations should read and return the content bytes or an
	// error if the content cannot be read.
	ReadContent(id NodeID) ([]byte, error)

	// ReadMeta returns the serialized node metadata (for example meta.yaml)
	// for the specified node id.
	ReadMeta(id NodeID) ([]byte, error)

	// Stats returns NodeStats (timestamps) for the node.
	Stats(id NodeID) (NodeStats, error)

	// ListIndexes returns a list of index names (for example dex files)
	// available from this repository.
	ListIndexes() ([]string, error)

	// ListNodesID returns a slice of all node IDs present in the repository.
	ListNodesID() ([]NodeID, error)

	// ListNodes returns a slice of NodeRef describing nodes (id, title,
	// updated).
	ListNodes() ([]NodeRef, error)

	// ListItems returns the list of ancillary item names (attachments)
	// associated with the given node id. Implementations should return
	// ErrNodeNotFound if the node does not exist.
	ListItems(id NodeID) ([]string, error)

	// ListImages returns the list of stored image names associated with the
	// given node id. Implementations should return ErrNodeNotFound if the node
	// does not exist.
	ListImages(id NodeID) ([]string, error)

	// WriteContent writes the primary content for the given node id.
	WriteContent(id NodeID, data []byte) error

	// WriteMeta writes the node metadata (for example meta.yaml) for the node
	// id.
	WriteMeta(id NodeID, data []byte) error

	// UploadImage stores an image blob associated with the node.
	UploadImage(id NodeID, name string, data []byte) error

	// UploadItem stores a named ancillary item (attachment) for the node.
	UploadItem(id NodeID, name string, data []byte) error

	// MoveNode renames or moves a node from id to dst. Implementations should
	// return ErrDestinationExists (or equivalent) if the destination already
	// exists.
	MoveNode(id NodeID, dst NodeID) error

	// GetIndex reads the raw contents of an index file by name (e.g.,
	// "nodes.tsv").
	GetIndex(name string) ([]byte, error)

	// WriteIndex writes the raw contents of an index file.
	WriteIndex(name string, data []byte) error

	// ClearDex removes or resets the repository index as supported by the
	// implementation.
	ClearDex() error

	// DeleteNode removes the node and all associated content/metadata/items.
	DeleteNode(id NodeID) error

	// DeleteImage removes a stored image by name for the node.
	DeleteImage(id NodeID, name string) error

	// DeleteItem removes a stored ancillary item by name for the node.
	DeleteItem(id NodeID, name string) error

	// ReadConfig reads and returns the repository-level configuration (Config).
	// Implementations should load and parse the stored configuration (for example a keg file
	// or project zeke.yaml). If the configuration is missing, implementations MAY return
	// ErrMetaNotFound or a more specific error. Return the parsed Config on success.
	ReadConfig() (Config, error)

	// WriteConfig persists the provided Config to the repository.
	// Implementations should write atomically where possible and return an
	// error on failure.
	WriteConfig(config Config) error
}
