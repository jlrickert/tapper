# Backups And Archives

Export and import kegs as `.tar.gz` archives for backup, migration, and
sharing.

## Quick Start

```bash
# Export your keg to an archive
tap archive export -o ~/backups/my-keg-2026-03-14.tar.gz

# Import an archive into your keg
tap archive import ~/backups/my-keg-2026-03-14.tar.gz
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
| `--keg ALIAS` | Target keg by alias |
| `--project` | Target the project-local keg |
| `--cwd` | Target keg in the current working directory |
| `--path PATH` | Target keg by filesystem path |

### Examples

```bash
# Full keg export
tap archive export -o backup.tar.gz

# Export specific nodes
tap archive export -o selection.tar.gz --nodes 5,12,42

# Export without snapshot history (smaller archive)
tap archive export -o lightweight.tar.gz --no-history

# Export a specific keg
tap archive export -o personal.tar.gz --keg personal
```

## Import

```bash
tap archive import ARCHIVE [flags]
```

Imports nodes from an archive into the target keg. Prints the path of each
imported node to stdout.

### Flags

| Flag | Description |
|------|-------------|
| `--keg ALIAS` | Target keg by alias |
| `--project` | Target the project-local keg |
| `--cwd` | Target keg in the current working directory |
| `--path PATH` | Target keg by filesystem path |

### Sources

Archives can be loaded from local files or remote URLs:

```bash
# Local file
tap archive import ./backup.tar.gz

# Remote URL
tap archive import https://example.com/shared-keg.tar.gz
```

HTTP and HTTPS URLs are downloaded automatically before import.

## Archive Format

Archives use the `keg-archive/v1` format, stored as a gzip-compressed tar:

```text
keg-archive/
├── manifest.json
└── nodes/
    ├── 0/
    │   ├── README.md
    │   ├── meta.yaml
    │   ├── stats.json
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

### manifest.json

The manifest records export metadata and the list of included nodes:

```json
{
  "format": "keg-archive/v1",
  "source": "personal",
  "exported_at": "2026-03-14T10:00:00Z",
  "with_history": true,
  "nodes": [
    { "source_id": "0", "revision_count": 0 },
    { "source_id": "5", "revision_count": 3 },
    { "source_id": "42", "revision_count": 1 }
  ]
}
```

Each node entry records the original source ID and the number of snapshot
revisions included.

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
the archive and replayed on import with original timestamps intact.

For details on how snapshots work, see [Node Snapshots](node-snapshots.md).

## Backup Strategies

### Full keg backup

Export the entire keg with a date-stamped filename:

```bash
tap archive export -o ~/backups/personal-2026-03-14.tar.gz --keg personal
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
tap archive export -o ~/backups/personal-$DATE.tar.gz --keg personal
# Optional: remove backups older than 30 days
find ~/backups -name "personal-*.tar.gz" -mtime +30 -delete
```

### Lightweight snapshots

Use `--no-history` for faster, smaller backups when you only need the current
state:

```bash
tap archive export -o ~/backups/personal-$DATE.tar.gz --keg personal --no-history
```

## Migration And Sharing

### Move a keg between machines

On the source machine:

```bash
tap archive export -o my-keg.tar.gz --keg personal
```

Copy the archive to the target machine (scp, USB drive, cloud storage, etc.),
then import:

```bash
tap archive import my-keg.tar.gz --keg personal
```

### Share nodes with a collaborator

Export specific nodes and host the archive or share the file directly:

```bash
tap archive export -o shared-notes.tar.gz --nodes 10,20,30
```

The recipient imports from a local file or a URL:

```bash
tap archive import https://example.com/shared-notes.tar.gz
```

## Behavior Details

Understanding these behaviors helps avoid surprises during import:

- **Fresh IDs**: Imported nodes receive new IDs allocated sequentially in the
  target keg. Source IDs are never reused directly.

- **Link rewriting**: Relative links (`../N`) in node content are
  automatically rewritten to point to the newly allocated IDs. Links in
  `stats.json` are also remapped.

- **Asset preservation**: If an imported node replaces an existing node at the
  same target ID, any file and image attachments on the existing node are
  preserved.

- **Atomic failure**: If any node fails to import, the entire import operation
  fails. Partially imported nodes are not rolled back, but the dex is only
  rebuilt on complete success.

- **Dex rebuild**: Keg indices are automatically rebuilt after a successful
  import.

- **Compression fallback**: The importer tries gzip decompression first. If
  that fails, it falls back to reading the archive as an uncompressed tar.
