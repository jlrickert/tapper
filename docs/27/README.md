# KEG CLI — Commands Reference

A concise reference for the most-used keg commands and examples. For full flag lists run:

keg <command> --help

Note: commands may support short aliases; examples below use canonical subcommand names.

## Repo commands

### keg repo init

Purpose: Scaffold or initialize a KEG repository (keg file, dex/ directory).  
Example:
keg repo init

### keg repo show

Purpose: Show the active repo config and effective keg resolution.  
Example:
keg repo show

## Node creation & editing

### keg create (alias: keg new-node, short: keg c)

Purpose: Create a KEG node under docs/<id>/, optionally with explicit id, content, and metadata.

Usage examples:
keg create [--id <n>] [--title "<Title>"] [--tags "a,b"] [--author "Name <email>"] [--stdin README.md]  
keg create [--id <n>] --stdin README.md

Quick create / piping:
keg c
echo "# Quick note" | keg create --stdin README.md
echo "# Title" | keg c --tags="a,b"

Examples:
keg create --title "Hello World" --tags "example,cli"

### keg edit

Purpose: Open node content for editing using $EDITOR. On save, writes README.md and updates node meta `updated` timestamp.

Usage:
keg edit <id>

Example:
keg edit 123

Also supports editing attachments:
keg edit <id> <attachment-path>
Example:
keg edit 123 attachments/diagram.png

Notes: Attachments live under docs/<id>/attachments/ or images/ depending on repo implementation.

### keg meta edit

Purpose: Open docs/<id>/meta.yaml for editing; includes helpers/subcommands to set timestamps or normalize fields.

Usage:
keg meta edit <id>

## Metadata management

### keg meta update

Purpose: Normalize and update metadata fields programmatically.

Usage:
keg meta update --all  
keg meta update <id>

Behavior: Normalizes tags, ensures RFC3339 `updated` timestamps, and optionally regenerates summaries or other computed metadata.

See node meta docs: [Node meta — meta.yaml](../18)

## Node images — manage images on a node

Purpose

- Provide a simple, safe, and testable CLI surface for managing images stored with a KEG node.
- Reuse repository primitives (ListImages / UploadImage / DeleteImage) and follow atomic-write/sanitization rules.
- Offer both convenient CLI operations and predictable programmatic behavior for tooling and tests.

Overview

- Images are stored under docs/<id>/images/ (thumbs under docs/<id>/images/thumbs/). Small per-image metadata may be stored in meta.yaml under an "images" key or as per-image .meta JSON files under docs/<id>/images/.meta/.
- CLI is non-destructive by default; destructive actions require explicit confirmation or `--yes`/`--force`.
- Validation: allowed content types, size limits (default 8 MiB), name sanitization, and thumbnail generation are supported.

Commands

- keg image upload <id> <file> [--name NAME] [--thumb] [--force] [--store-meta] [--max-size BYTES]
  - Upload a local file to docs/<id>/images/<name>.
  - If --name omitted, uses normalized basename(file).
  - --thumb generates thumbnails (best-effort).
  - --force overwrites an existing image (without error).
  - --store-meta writes a per-image metadata entry (or updates meta.yaml images list) depending on repo policy.
  - Prints resulting image name, sha256, size, content-type on success.
  - Validation:
    - Allowed raster formats: image/png, image/jpeg, image/gif, image/webp. SVG (image/svg+xml) is treated as text and sanitized or rejected depending on repo policy.
    - Default MaxSize: 8 * 1024 * 1024 (configurable).
    - Name sanitization: basename only, allow [A-Za-z0-9._-], replace whitespace with '-'.
  - Example:
    keg image upload 42 ./diagram.png
    # returned: uploaded diagram.png (sha256: ..., size: 12345, content-type: image/png)

- keg image list <id> [--meta]
  - List images stored for a node. Without flags prints sorted image names.
  - --meta prints JSON array of per-image metadata (name, sha256, size, content-type, created).
  - Example:
    keg image list 42
    keg image list 42 --meta

- keg image delete <id> <name> [--yes]
  - Delete a named image and its thumbnails and .meta files.
  - Requires interactive confirmation unless --yes is provided.
  - Returns a non-error if the image doesn't exist (or a sentinel ErrMetaNotFound depending on policy); CLI warns and continues.
  - Example:
    keg image delete 42 diagram.png --yes

- keg image serve <id> <name> [--port N] [--readonly]
  - Serve a single image (or the images directory) over a temporary local HTTP server for preview (developer convenience).
  - Default bind: 127.0.0.1; prints URL after start.
  - Read-only by default; do not enable write endpoints on unsafe networks.
  - Example:
    keg image serve 42 diagram.png --port 0

- keg image import <id> <url> [...] [--name NAME] [--thumb] [--force]
  - Download remote images (explicit network action), validate and store local copies.
  - Enforces size limits and timeouts, rejects URLs with embedded credentials, follows redirects cautiously.
  - Example:
    keg image import 42 https://example.org/hero.jpg

Behavior & implementation notes (summary)

- Atomic writes: uploads write to a temp file in the images directory and os.Rename into place. FsRepo implementation should fsync file and parent dir when possible.
- Repository locking: operations that should be consistent with index writes should acquire a short-lived repository or per-node lock (.keg-lock) to avoid races.
- Metadata:
  - Option A: record minimal image list in meta.yaml.images: [{name, sha256, size, content_type, created}] — convenient for indexing but may bloat meta.yaml and risk comment churn.
  - Option B (preferred to avoid meta.yaml churn): per-image .meta JSON files under docs/<id>/images/.meta/<name>.json.
- Thumbnail generation is optional and best-effort. If thumbnail generation fails, the original image is still stored and the CLI prints a warning.
- Validation:
  - Detect content type by magic/header (image.DecodeConfig for raster formats), not only by extension.
  - Enforce configurable max upload size (default 8 MiB).
  - Reject or sanitize SVG content unless a sanitization policy is in place.
- Deduplication strategies:
  - Simple mode: overwrite guarded by --force.
  - Dedup-by-content: compute sha256 and optionally store canonical by sha prefix and record aliases in metadata (more complex).
- Security:
  - Never execute or render uploaded images in an unsafe context.
  - Sanitize SVGs or disallow them unless explicitly allowed.
  - For imports, disallow URLs with credentials and enforce timeouts and size limits.

Examples

- Upload:
  keg image upload 42 ./diagram.png
- Upload with thumbnail and custom name:
  keg image upload 42 ./diagram.png --name diagram-v2.png --thumb
- List with metadata:
  keg image list 42 --meta
- Delete with confirmation:
  keg image delete 42 diagram-v2.png
- Import from URL:
  keg image import 42 https://example.org/hero.jpg --thumb

See node docs for implementation guidance and API sketches: [Node images — manage images on a node](../27)

## Indices

### keg index clean

Purpose: Remove generated indices under dex/ (nodes.tsv, tags, links, backlinks, changes.md). Typically guarded and requires confirmation or --yes for scripts.

Usage:
keg index clean

### keg index gen

Purpose: Generate or update one or more dex indices (nodes.tsv, tags, links, backlinks, changes.md, etc.). When run without selecting specific indexes, regenerates the canonical set configured in the keg file or standard dex files.

Selecting specific indexes:
--index <name> (may be passed multiple times)  
Or positional: keg index gen <name>

Recognized index names:

- nodes.tsv — [dex/nodes.tsv](../7) — nodes index: id → updated → title
- tags — [dex/tags](../17) — tag → node ids
- links — [dex/links](../14) — src → dst node ids (TSV)
- backlinks — [dex/backlinks](../16) — dst → src node ids (TSV)
- changes.md — [dex/changes.md](../8) — human-readable changelog

Output control:
--out <path> — write generated index to the given path (use --out - to print to stdout)

Safety & atomicity:
By default writes atomically (temp file + rename). When writing to stdout (--out -) atomic replacement is not provided.

Additional flags:
--external — run configured external indexers/exporters (explicit opt-in)  
--force — force overwrite (use with care)  
--dry-run — show what would be generated without writing files

Examples:
keg index gen  
keg index gen --index tags  
keg index gen tags  
keg index gen --index tags --out -  
keg index gen --index nodes.tsv --out ./tmp/nodes.tsv  
keg index gen --index tags --index links  
keg index gen --external

### Viewing and editing dex items

Purpose: Provide quick, safe ways to inspect or edit index files stored in dex/. Useful for troubleshooting or quick reads. Note: editing indices directly may be clobbered by the index generator; prefer regenerating with `keg index gen` when possible.

Commands:

- keg index view <name>

  - Print the named index to stdout. <name> is an index basename (e.g., tags, nodes.tsv, links, backlinks, changes.md).
  - Example: keg index view tags
  - Shortcut: keg index view nodes.tsv

- keg index cat <name>

  - Alias for `keg index view <name>` (convenience for cat-like behavior).
  - Example: keg index cat tags

- keg index edit <name>

  - Open the index file in $EDITOR for inspection or temporary edits.
  - Example: keg index edit tags
  - Note: edits may be overwritten by future `keg index gen` runs. For persistent changes, modify the upstream sources (meta/README) and regenerate.

- Direct filesystem fallback:
  - You can also inspect index files directly: cat dex/tags or $EDITOR dex/tags

Examples:
keg index view tags
keg index edit nodes.tsv
$EDITOR dex/tags

## Validation & linting

### keg validate

Purpose: Validate nodes and repo indices for schema/consistency.

Usage:
keg validate [docs/<id> | docs/*]

Checks include:

- meta.yaml contains required fields (title, updated)
- updated, created, accessed are RFC3339 UTC timestamps
- indices reference valid node ids (per [dex/nodes.tsv](../7))

### keg lint

Purpose: Lint content and metadata for formatting and conventions.

Usage:
keg lint docs/123

## Link resolution & view

### keg link

Purpose: Resolve keg: tokens to URLs or ids using the repo keg file and index.

Usage:
keg link <token> [--format url|id|meta]

Example:
keg link "keg:repo" --format url

See: [Keg links (keg-link)](../21) for alias/token resolution rules.

### keg cat / keg view

Purpose: Dump node content to stdout (optionally pretty).

Usage:
keg cat <id>  
keg view <id> --pretty

Note: Primary content conventions are documented in [Keg content (keg-content)](../19).

Also: keg cat can be used to dump index files by path (for quick inspections) — e.g., keg cat dex/tags or simply cat dex/tags. Prefer `keg index view` / `keg index cat` for index-focused ergonomics.

## Server

Purpose: Serve repository content over HTTP for local preview, CI previews, or simple automation. Not intended as a production server.

Usage:
keg server [flags]

Common flags:
--host <host> Host/interface to bind to (default: 127.0.0.1)  
--port <port> TCP port to listen on (default: 8080)  
--root <path> Repository root to serve (default: discovered repo root)  
--dir <subpath> Serve a specific subdirectory (repeatable, e.g., docs or dex)  
--open Open the server URL in the default browser after start  
--readonly Disable write/unsafe endpoints (recommended for previews)  
--tls-cert <file> Path to TLS certificate (enable HTTPS when both cert & key provided)  
--tls-key <file> Path to TLS private key  
--cors Enable permissive CORS (useful for local frontend development)  
--log-level <lvl> Logging verbosity (info|debug|warn|error)

Behavior:

- Default bind: 127.0.0.1:8080, serves the repo root. Exposes:
  - / — browsable node directories; README.md rendered or downloadable
  - /dex/ — raw index files ([nodes.tsv](../7), [tags](../17), [links](../14), [backlinks](../16), [changes.md](../8))
  - /keg — optional endpoint that serves parsed repo-level config as JSON
- If README.md is requested the server may render simple HTML for convenience.
- Avoid enabling write endpoints on untrusted networks.

Examples:
keg server  
keg server --open  
keg server --host 0.0.0.0 --port 8000 --dir docs  
keg server --port 8443 --tls-cert cert.pem --tls-key key.pem

Security & recommendations:

- For local/dev use only; if exposed to untrusted networks, run behind a hardened reverse proxy (TLS termination, auth, rate-limiting).
- Keep --readonly enabled for previews.
- Do not store secrets in served files (meta.yaml, keg file).

Stopping:
Stop the server with Ctrl‑C (SIGINT). In service wrappers, stop per your platform service manager.

Automation notes:

- Use --host 127.0.0.1 --port 0 to bind an ephemeral port; the server will print the chosen port for tests.
- The /keg endpoint (if present) is handy for scripts needing parsed config metadata instead of raw YAML.

## Applying & conflicts

### keg apply

Purpose: Apply .new files generated by commands or accept generator output.

Usage:
keg apply [path] [--yes]

Example:
keg apply docs/123/README.md.new

### keg conflicts / keg resolve-conflict

Purpose: Inspect and manage conflicts introduced by generators or merges.

Usage:
keg conflicts  
keg resolve-conflict <path> --accept theirs|ours|manual

## Import & export

### keg import

Purpose: Import nodes from another KEG repository or import bundle.

Usage:
keg import --source <path|url> [--ids 1,2,3] [--dry-run]

Behavior: Validates incoming node ids and metadata; avoids overwriting without explicit flags.

### keg export

Purpose: Export indices or nodes in a chosen format.

Usage:
keg export nodes --format json --out ./nodes.json

## Zeke & integration helpers

### keg zeke / zk

Purpose: Helpers to pipe text to/from Zeke for drafting and patch notes.

Usage example:
zk "draft README for node 123" | keg apply docs/123/README.md.new

Note: Requires Zeke configured in repo settings. See [Zeke AI utility (zeke)](../3) and [Integration — KEG, Zeke, and Link Resolution](../28) for details.

## Maintenance & health

### keg doctor

Purpose: Run repository health checks.

Usage:
keg doctor

## Help

keg help — Display help and subcommand details

## Examples

Create node via Zeke and validate (preferred pattern):

zk "Draft README for new node about X" | keg create  
zk "some file contents" | keg create item

# Inspect generated docs/999/README.md.new if produced, then:

keg apply docs/999/README.md.new  
keg edit 999  
keg validate docs/999  
keg index gen --dry-run

Resolve a token in a documentation build:
keg link "keg:repo" --format url

## References

- [KEG specification (keg-spec)](../5) — KEG spec
- [Keg index (keg-index)](../6) — dex/ indices
- [Nodes index — dex/nodes.tsv](../7)
- [Tags index — dex/tags](../17)
- [Links index — dex/links](../14)
- [Backlinks index — dex/backlinks](../16)
- [Node meta — meta.yaml](../18)
- [Keg content/README conventions — keg-content](../19)
- [Zeke AI utility — zeke](../3)
- [Integration — KEG, Zeke, and Link Resolution](../28)

