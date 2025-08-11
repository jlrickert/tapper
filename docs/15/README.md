# Keg node (keg-node)

A [KEG](../5) node is the basic content unit managed by the [KEG](../5) utility. A node groups a small set of files (content, metadata, attachments) under a stable numeric id and provides the canonical place to store notes, documentation, drafts, and small artifacts. This page documents node layout, metadata conventions, common operations, and implementation notes for tooling and library authors.

## Purpose

- Provide a simple, discoverable unit for content that tooling can index, tag, and manipulate.
- Keep human-editable content (README.md) alongside machine-friendly metadata (meta.yaml).
- Make nodes easy to reference by id (numeric) and by tags/slugs for automation.

## Typical node directory layout

Each node lives in a directory named by its numeric id under the repo root (or repo layout root used by the FS repo). Minimal example:

- `<repo-root>/<id>/`
  - README.md — primary markdown content
  - meta.yaml — node metadata (YAML)
  - attachments/ — optional attachments (images, files)
  - images/ — optional stored images

Example on disk:

```
./docs/42/README.md
./docs/42/meta.yaml
./docs/42/attachments/diagram.png
./docs/42/images/cover.jpg
```

[KEG](../5) tooling (the keg CLI or repo indexers) treats the content file and meta.yaml as the canonical node pieces.

## meta.yaml (node metadata)

meta.yaml is the canonical machine-readable description of a node. Keep it small and explicit. The `updated` timestamp MUST be accurate and is used by indexers to determine freshness.

Recommended fields (YAML):

```yaml
updated: 2025-08-09 18:33:17Z # ISO 8601 UTC; first line if possible
title: Keg node (keg-node)
summary: |
  One-line summary or short paragraph describing the node.
tags:
  - keg
  - node
  - draft
links:
  - alias: upstream
    url: https://example.org
authors:
  - https://github.com/alice
```

Field guidance:

- updated: ISO 8601 UTC (ending with `Z`). Update whenever content or metadata changes.
- title: human-friendly; may include a parenthetical slug (e.g., "X (x-slug)") for canonical tag pages.
- summary: optional short summary used in index or hub pages.
- tags: lowercase, hyphen-separated tokens for discovery and automation.
- links/authors: optional provenance and external references.

Do not store secrets in meta.yaml. Avoid committing credentials or tokens.

## README.md (primary content)

- Markdown document describing the node content.
- Prefer a concise title and a short summary at the top.
- If you intend the node to be the canonical documentation for a tag, append the tag slug in parentheses in the title: e.g., `Keg node (keg-node)`.

Example README snippet:

```markdown
# Keg node (keg-node)

A KEG node documents the smallest unit of content managed by KEG.

- id: 42
- tags: keg-node, docs
```

## Tags & slugs

- Use tags to group nodes (see [Keg tags (keg-tags)](../10) - the canonical tag documentation).
- Prefer stable, meaningful tags.
- If the node documents a tag, include the slug in the title in parentheses and include the slug in meta.yaml tags.

CLI examples:

- Add a tag:
  `yq -i '.tags = (.tags // []) + ["my-tag"] | .tags |= unique' docs/42/meta.yaml`
- Remove a tag:
  `yq -i '.tags |= (. // []) | del(.tags[] | select(.=="my-tag"))' docs/42/meta.yaml`

## Index interaction

Nodes are referenced by the repository's dex indices ([dex/nodes.tsv](../7), [dex/tags](../10), [dex/links](../14), etc.). Indexers read `meta.yaml` and `README.md` to build the node index.

- [dex/nodes.tsv](../7): maps id → updated → title
- [dex/tags](../10): maps tag → node ids

When you change a node’s content or metadata, update `updated` and run the indexer (`keg index update` — see [Keg index (keg-index)](../6) or the repo's indexer) to regenerate the indices.

## CLI workflow (common commands)

- Create a new node (interactive/pipe): `[keg create](../5)` (or via [Zeke](../3) piping)
- Edit content: `[keg edit](../5) <id>`
- Show content: `[keg cat](../5) <id>`
- Create a patch note (example integration): `zk "draft short note on X" | [keg create](../5)`
- Commit a node: `[keg commit](../5) <id>`
- Update index: `[keg index update](../6)`
- Delete node: `[keg delete](../5) <id>` (implementation-specific)

Examples:

- Pipe AI output to create a node:
  `[zk](../3) "draft short note on X" | [keg create](../5)`
- Show node 42:
  `[keg cat](../5) 42`

## Implementation notes (Go)

[pkg/keg](../12) provides types and repository abstractions to operate on nodes. Relevant types/functions (see pkg/keg):

- type NodeID int — stable numeric identifier
  - func (id \*NodeID) Path() string
- type NodeRef { Id NodeID; Modified time.Time; Title string }
- type NodeMeta map[string]any — helpers:
  - func (meta \*NodeMeta) AddTag(tag string)
  - func (meta \*NodeMeta) RemoveTag(tag string)
  - func (meta \*NodeMeta) GetStats() NodeStats
- Repository interface (KegRepository) operations:
  - ReadContent(id NodeID) ([]byte, error)
  - ReadMeta(id NodeID) ([]byte, error)
  - WriteContent(id NodeID, data []byte) error
  - WriteMeta(id NodeID, data []byte) error
  - MoveNode(id NodeID, dst NodeID) error
  - ListNodes, ListNodesID, etc.

Design suggestions for authors of repo implementations:

- Read/Write meta and content atomically where possible.
- Preserve `updated` semantics: update timestamp on changes.
- Return typed errors (see [Idiomatic Go error handling](../12)) so callers can use errors.Is / errors.As (e.g., ErrNodeNotFound, NewNodeNotFoundError).
- Index generation should be idempotent and write dex files atomically (write temp file + rename).

## Testing

- Unit tests should exercise:
  - Reading/writing meta and content.
  - Tag add/remove behavior and index generation.
  - Error cases (missing node → NodeNotFoundError; destination exists → DestinationExistsError).
- Example test: assert errors.Is(err, keg.ErrNodeNotFound) and errors.As to typed NodeNotFoundError.

## Best practices

- Keep each node focused (one topic per node).
- Keep `updated` accurate and always change it when modifying content or metadata.
- Use tags for discovery and automation; keep tag names stable.
- Prefer atomic index writes and avoid partial state that tools can observe.
- Avoid embedding secrets in metadata or content.

## Example meta + README (complete)

meta.yaml:

```yaml
updated: 2025-08-10 12:00:00Z
title: Keg node (keg-node)
summary: Short reference for what a KEG node contains and tooling expectations.
tags:
  - keg-node
  - docs
authors:
  - https://github.com/jlrickert
```

README.md:

```markdown
# Keg node (keg-node)

This node documents the expected layout and metadata for KEG nodes.

Summary: nodes contain a README.md and meta.yaml, optional attachments, and are indexed by [dex/nodes.tsv](../7).
```
