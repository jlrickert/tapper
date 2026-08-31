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

Overwrites the live node aggregate with the state captured at revision `REV`.
A new snapshot is automatically created to record the restore action. Without `--yes`, the command prompts for
confirmation on a TTY and refuses in non-interactive contexts.

## Storage Model

Snapshots are durable PostgreSQL records owned by Tapper Hub and addressed by
node plus revision. Clients access them only through the Hub-compatible
snapshot APIs. A revision record has the following logical metadata shape:

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

`Parent` is `0` for the first revision. `IsCheckpoint` marks whether the Hub
stores full content or a patch internally.

## Patch-Based Compression

To minimize storage, most revisions store a line-based diff rather than the
full content. The patch algorithm (`line-patch-v1`) uses three operations:

| Operation | Description |
|-----------|-------------|
| `equal`   | Retain N lines unchanged from the base |
| `delete`  | Skip N lines from the base |
| `insert`  | Add new lines |

Patch payloads use this JSON shape internally:

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

The archive preserves revision history so restored nodes retain their
snapshots, without making the archive layout a live repository format.

For full backup and restore workflows, see
[Backups And Archives](backups-and-archives.md).

## Key Source Files

| File | Purpose |
|------|---------|
| `pkg/keg/keg_snapshots.go` | Keg-level snapshot API |
| `pkg/keg/repository.go` | `RepositorySnapshots` interface used by Hub-side `LocalKeg` orchestration |
| `pkg/keg/snapshot_patch.go` | Patch algorithm |
| `internal/testkegrepo/memory_repository.go` | In-memory storage used only by tests |
| `pkg/tapper/tap_snapshots.go` | Service layer |
| `pkg/cli/cmd_snapshot.go` | CLI commands |
