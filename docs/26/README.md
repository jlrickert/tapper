# KEG CLI — KEG management tool (keg)

A focused command-line tool for managing KEG nodes, indices, and link resolution. keg helps maintain repository-level metadata, validate and generate node content, resolve keg: tokens, and integrate with Zeke for iterative content editing. This node documents the keg program itself — install, surface commands, integration points, and where to find focused documentation.

## Goals

- Manage [KEG specification](../5) content, [Keg node](../15) nodes, and [Keg index](../6) indices with predictable, reproducible commands.
- Validate and maintain repo metadata and node metadata ([Node meta (meta.yaml)](../18)).
- Provide safe, idempotent generators and resolvers for `keg:` tokens and node content ([Keg links (keg-link)](../21)).
- Integrate with [Zeke AI utility (zeke)](../3) for drafting and editing workflows while keeping operations explicit and safe.

## Quickstart (install & first run)

Install (from source):

- go install github.com/jlrickert/keg/cmd/keg@latest
- or go build ./cmd/keg

Initialize or inspect a KEG repository:

- cd path/to/keg-repo
- keg doctor # quick healthchecks (see Commands Reference (keg CLI) ../27)
- keg repo init # create or initialize a KEG repository (see Commands Reference ../27)

## Primary commands (concise list)

This list maps the user's needs to concrete keg subcommands and their responsibilities. Each command adheres to the single-responsibility principle (do one thing well). For detailed flags run `keg <command> --help`.

- keg repo init
  - Create a new KEG repository scaffold (keg file, dex/ dir, templates). (see Commands Reference ../27)
- keg create
  - Register/create a node with content and meta; supports stdin and explicit metadata fields.
  - Examples: create from stdin or provide flags for title/tags/author as supported by the command-line.
- keg edit <id>
  - Open node content for editing (editor from $EDITOR); writes changes to disk. Node meta will be updated on save. (see Commands Reference ../27)
- keg edit <id> [attachment]
  - Open node content attachment for editing (editor from $EDITOR); writes changes to disk.
- keg meta edit <id>
  - Edit keg meta for node
- keg meta update (all | <id>)
  - Update metadata programmatically (normalize tags, regenerate summaries, bump timestamps).
  - `keg meta update --all` — normalize/update all nodes.
  - `keg meta update <id>` — update a single node's metadata.
- keg index clean
  - Remove indices under dex/ ([Nodes index (`dex/nodes.tsv`)](../7), [Tags index - `dex/tags`](../17), [Links index - `dex/links`](../14), [Backlinks index - `dex/backlinks`](../16)).
- keg index gen
  - Update indices under dex/ (nodes.tsv, tags, links, backlinks).
- keg index gen --external
  - Update index and any configured external indices/exporters (explicit opt-in).
- keg link
  - Resolve `keg:` tokens to URLs/ids using repo keg file and index ([Keg links (keg-link)](../21)).
- keg cat / keg view
  - Show node content on stdout; optional pretty/strip rendering.
- keg import
  - Import nodes from another KEG (local path, archive, or via explicit export).
- keg doctor
  - Healthcheck repository layout, meta formats, and index consistency. (see Commands Reference ../27)
- keg help
  - Show help and command summaries.
- keg apply
  - Apply generator-produced `.new` files or accept generated changes (interactive merge support).
- keg conflicts / keg resolve-conflict
  - Inspect and manage content/index conflicts (tools for interactive resolution).
- keg node history / keg node versions
  - Manage or inspect different versions for a node (repo-backed history or imported snapshots).

CLI ergonomics

- Idempotent generators where practical.
- Generated conflicts are written with a `.new` suffix (no silent overwrites); use `keg apply` to accept.
- Non-destructive by default: destructive actions require explicit flags (`--yes`, `--force`) or `keg apply`.
- Repository maintainers may adapt scaffolding and generator behavior for organizational needs.

## Files & metadata affected by keg operations

Common files keg will create/manage:

- docs/<id>/README.md (node content)
- docs/<id>/meta.yaml (node metadata: title, summary, authors, updated) — see [Node meta (meta.yaml)](../18)
- dex/nodes.tsv, dex/tags, dex/links, dex/backlinks (indices) — see [Nodes index (`dex/nodes.tsv`)](../7), [Tags index - `dex/tags`](../17), [Links index - `dex/links`](../14), [Backlinks index - `dex/backlinks`](../16)
- keg.yml or docs/keg (repo-level keg file) — see [Keg configuration (keg-config)](../2)
- .keg-cache/ (optional local cache)

keg never performs network operations without explicit user consent.

## Design principles

- Single responsibility: each command performs one job.
- Deterministic: generated metadata and indices use stable, deterministic formatting.
- Safety first: no destructive writes without explicit acceptance.
- Predictable UX: clear output, consistent return codes, and machine-friendly flags for automation.

## Links (authoritative, single-responsibility docs)

- Node layout & conventions — [Keg node (keg-node)](../15)
- Index generation & indices — [Keg index (keg-index)](../6)
- Keg configuration (keg file) — [Keg configuration (keg-config)](../2)
- Keg links / token resolution — [Keg links (keg-link)](../21)
- Zeke integration — [Zeke AI utility (zeke)](../3)
- KEG specification — [KEG specification (keg-spec)](../5)

## Roadmap / TODOs

- exporters (JSON/TSV/OCI)
- improved CI integration for deterministic index generation

## Contributing

- Follow repository CONTRIBUTING.md when present.
- When editing [meta.yaml](../18) update `updated` using RFC3339 UTC format (Z suffix).
- Keep generated diffs minimal and document automated changes.

