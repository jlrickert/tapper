# Node Snapshots

Snapshots provide per-node revision history. Each snapshot captures the full
state of a node (content, metadata, and stats) at a point in time. Snapshots
are append-only and stored alongside the node they belong to.

## CLI Commands

### Create a snapshot

```bash
tap snapshot create NODE_ID -m "before refactor"
tap snapshot create 12 --keg personal -m "draft complete"
```

Captures the current state of the node. Prints the new revision ID to stdout.

### List snapshot history

```bash
tap snapshot history NODE_ID
tap snapshot history 12 --keg personal
```

Outputs a table with columns: `REV`, `CREATED`, `HASH`, `MESSAGE`.

### Restore a snapshot

```bash
tap snapshot restore NODE_ID REV --yes
tap snapshot restore 12 1 --keg personal --yes
```

Overwrites the live node files (README.md, meta.yaml, stats.json) with the
state captured at revision `REV`. A new snapshot is automatically created to
record the restore action. Without `--yes`, the command prompts for
confirmation on a TTY and refuses in non-interactive contexts.

## Storage Layout

Snapshots live in a `snapshots/` directory inside the node directory:

```text
<keg-root>/
  12/
    README.md
    meta.yaml
    stats.json
    snapshots/
      index.json        # Manifest of all revisions
      1.full            # Full content at revision 1
      1.meta            # Metadata at revision 1
      1.stats           # Stats at revision 1
      2.patch           # Patch from revision 1 to 2
      2.meta
      2.stats
      3.full            # Checkpoint (full content)
      3.meta
      3.stats
```

### index.json

The manifest is a JSON array of snapshot metadata entries:

```json
[
  {
    "ID": 1,
    "Node": {"ID": 12},
    "Parent": 0,
    "CreatedAt": "2026-02-26T09:05:00Z",
    "Message": "initial",
    "ContentHash": "abc123...",
    "IsCheckpoint": true
  },
  {
    "ID": 2,
    "Node": {"ID": 12},
    "Parent": 1,
    "CreatedAt": "2026-02-26T10:00:00Z",
    "Message": "update title",
    "ContentHash": "def456...",
    "IsCheckpoint": false
  }
]
```

`Parent` is `0` for the first revision. `IsCheckpoint` marks whether the
revision stores full content or a patch.

## Patch-Based Compression

To minimize storage, most revisions store a line-based diff rather than the
full content. The patch algorithm (`line-patch-v1`) uses three operations:

| Operation | Description |
|-----------|-------------|
| `equal`   | Retain N lines unchanged from the base |
| `delete`  | Skip N lines from the base |
| `insert`  | Add new lines |

Patch files (`.patch`) are JSON:

```json
{
  "base_hash": "abc123...",
  "ops": [
    {"type": "equal", "count": 5},
    {"type": "delete", "count": 2},
    {"type": "insert", "lines": ["new line 1\n", "new line 2\n"]},
    {"type": "equal", "count": 3}
  ]
}
```

The `base_hash` field allows patch application to detect mismatches between
the expected base content and the actual base content.

### Checkpoints

Periodically a revision stores full content instead of a patch. This bounds
reconstruction cost so that reading an old revision never needs to replay
an unbounded chain of patches.

- The first revision is always a checkpoint (full content).
- After every 20 consecutive patches, the next revision is stored as a
  checkpoint.
- The checkpoint interval is configurable per repository
  (`SnapshotCheckpointInterval`, default 20).

### Content Reconstruction

To read content at revision N:

1. Find the most recent checkpoint at or before N.
2. Apply each patch from that checkpoint through N sequentially.
3. Validate the content hash at the target revision.

## Concurrency

Snapshot operations acquire the per-node lock before reading or writing. This
prevents concurrent appends from corrupting the snapshot index or producing
conflicting revision IDs.

The `ExpectedParent` field on writes provides an additional optimistic
concurrency check: if another writer appended a revision between the read and
write, the expected parent will not match and the operation fails with
`ErrConflict`.

## Archive Integration

Snapshots are included in keg archives by default:

```bash
tap archive export -o archive.keg.tar.gz              # includes history
tap archive export -o archive.keg.tar.gz --no-history  # excludes snapshots/
```

The archive preserves the full `snapshots/` directory structure so that
imported nodes retain their revision history.

## Key Source Files

| File | Purpose |
|------|---------|
| `pkg/keg/keg_snapshots.go` | Keg-level snapshot API |
| `pkg/keg/repository.go` | `RepositorySnapshots` interface |
| `pkg/keg/snapshot_patch.go` | Patch algorithm |
| `pkg/keg/repo_filesystem_snapshots.go` | Filesystem storage |
| `pkg/keg/repo_memory_snapshots.go` | In-memory storage (tests) |
| `pkg/tapper/tap_snapshots.go` | Service layer |
| `pkg/cli/cmd_snapshot.go` | CLI commands |
