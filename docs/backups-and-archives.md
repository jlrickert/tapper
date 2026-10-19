# Backups And Archives

Export kegs as `.tar.gz` archives for backup, migration, and sharing, and
restore a whole keg from one on its Tapper Hub.

## Quick Start

```bash
# Export a team keg to an archive
tap archive export --keg @acme/engineering -o ~/backups/engineering-2026-03-14.tar.gz
```

## Export

```bash
tap archive export -o OUTPUT [flags]
```

Creates a gzip-compressed tar archive of a keg. Prints the absolute path of
the created archive to stdout.

### Flags

| Flag | Description |
|------|-------------|
| `-o, --output PATH` | Archive output path (required) |
| `--nodes IDS` | Comma-separated node IDs to export (default: all) |
| `--no-history` | Omit snapshot history from the archive |
| `--keg @namespace/name` | Target a specific keg |
| `--namespace NAME` | Resolve a bare keg name in a namespace |
| `--hub NAME` | Override namespace-to-hub resolution |

### Examples

```bash
# Full keg export
tap archive export -o backup.tar.gz

# Export specific nodes
tap archive export -o selection.tar.gz --nodes 5,12,42

# Export without snapshot history (smaller archive)
tap archive export -o lightweight.tar.gz --no-history

# Export a specific keg
tap archive export -o engineering.tar.gz --keg @acme/engineering
```

## Restore

`tap` does not load archives into a keg. A full-keg export is restored on the
Tapper Hub that holds the keg, through its archive restore endpoint
(`POST /api/v1/@{namespace}/kegs/{alias}/archive/restore`) or the keg's
settings page. A restore replaces the keg's contents with the archive's and
runs as a background job on the hub; it needs an archive exported without
`--nodes`.

## Archive Format

Archives use the `keg-archive/v3` format, stored as a gzip-compressed tar:

```text
keg-archive/
├── manifest.json
├── keg.yaml              # present in full-keg exports
├── schemas/              # present in full-keg exports when schemas exist
│   ├── task.schema.yaml
│   └── decision.schema.yaml
└── nodes/
    ├── 0/
    │   ├── README.md
    │   ├── meta.yaml
    │   ├── stats.json
    │   ├── assets/       # present when file attachments are included
    │   ├── images/       # present when images are included
    │   └── snapshots/          # present when history is included
    │       ├── index.json
    │       ├── 0.full
    │       ├── 0.meta
    │       ├── 0.stats
    │       ├── 1.full
    │       ├── 1.meta
    │       └── 1.stats
    ├── 5/
    │   ├── README.md
    │   ├── meta.yaml
    │   └── stats.json
    └── 42/
        └── ...
```

`stats.json` is included because it is derived/internal node state (for
example hashes, timestamps, access counters, and link summaries). It is carried
so a restore can bring node state back consistently; authors normally edit
`README.md`, `meta.yaml`, and schema files instead.

### manifest.json

The manifest records export metadata and the list of included nodes:

```json
{
  "format": "keg-archive/v3",
  "source": "personal",
  "exported_at": "2026-03-14T10:00:00Z",
  "with_history": true,
  "with_settings": true,
  "with_schemas": true,
  "schemas": ["decision", "task"],
  "nodes": [
    { "source_id": "0", "revision_count": 0 },
    { "source_id": "5", "revision_count": 3 },
    { "source_id": "42", "revision_count": 1 }
  ]
}
```

Each node entry records the original source ID and the number of snapshot
revisions included.

Full-keg exports include `keg.yaml`, the keg settings document, and any
available keg-level schemas under `schemas/<type>.schema.yaml`. Exports that
select explicit nodes with `--nodes` omit `keg.yaml` and schemas, so they
cannot be restored over a keg.

When an archive contains schemas, a restore validates nodes against those
archived schemas and then replaces matching schema types in the keg.

## Snapshot History

Snapshot history is **included by default**. Use `--no-history` to create a
smaller archive without revision history.

```bash
# With history (default)
tap archive export -o full.tar.gz

# Without history
tap archive export -o slim.tar.gz --no-history
```

When history is included, each node's `snapshots/` directory is preserved in
the archive and replayed on restore with original timestamps intact.

For details on how snapshots work, see [Node Snapshots](node-snapshots.md).

## Backup Strategies

### Full keg backup

Export the entire keg with a date-stamped filename:

```bash
tap archive export -o ~/backups/engineering-2026-03-14.tar.gz --keg @acme/engineering
```

### Selective node backup

Export only the nodes you care about:

```bash
tap archive export -o critical-nodes.tar.gz --nodes 1,5,12,42
```

### Periodic backup script

```bash
#!/bin/bash
DATE=$(date +%Y-%m-%d)
tap archive export -o ~/backups/engineering-$DATE.tar.gz --keg @acme/engineering
# Optional: remove backups older than 30 days
find ~/backups -name "engineering-*.tar.gz" -mtime +30 -delete
```

### Lightweight snapshots

Use `--no-history` for faster, smaller backups when you only need the current
state:

```bash
tap archive export -o ~/backups/engineering-$DATE.tar.gz --keg @acme/engineering --no-history
```

## Migration And Sharing

### Move a keg between hubs

Export the keg from the source hub:

```bash
tap archive export -o engineering.tar.gz --keg @acme/engineering
```

Then restore the archive into a keg on the target hub (see [Restore](#restore)).

### Share nodes with a collaborator

Export specific nodes and host the archive or share the file directly:

```bash
tap archive export -o shared-notes.tar.gz --nodes 10,20,30
```

The archive is a plain gzip tar, so its nodes can be read without `tap`.

## Behavior Details

- **Preserved IDs**: A restore lands nodes at their archive IDs and removes
  every node the archive does not carry; node 0 is never removed.

- **Compression detection**: An archive that starts with the gzip magic bytes
  is decompressed; anything else is read as an uncompressed tar.

- **Streaming read**: A restore decompresses an archive once into a plain tar
  and reads each entry by offset, so it never holds every payload in memory
  at once. Directory entries are ignored, and when a name appears more than
  once the last entry wins. An entry name that is absolute or contains `..`
  rejects the archive, as does an attachment entry under `assets/`, `images/`,
  or `videos/` whose name is not a single plain filename.

- **Size limits**: A Tapper Hub bounds the archives it exports and restores by
  entry count, entry size, and compressed and uncompressed size. The
  uncompressed size counts every byte of the tar stream, extended (PAX and GNU
  long-name) headers included, and an archive is refused as soon as it crosses
  a limit rather than after it has been read in full. The hub checks the same
  limits on both sides, so an archive the hub exports is one it will restore.
