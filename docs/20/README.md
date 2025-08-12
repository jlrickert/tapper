# Keg node (keg-node)

A KEG node is the primary content unit managed by KEG. It groups small, human-editable content and machine-readable metadata under a stable numeric id. Nodes are easy to reference, index, tag, and link from other nodes or tooling.

## Purpose

- Provide a single, discoverable unit for notes, docs, drafts, and small artifacts.
- Keep human content (README.md) next to canonical metadata (meta.yaml).
- Enable tooling to index by id, tags, and outgoing links for automation and search.

## On-disk layout

Each node lives in a directory named by its numeric id:

- `<repo-root>/<id>/`
  - README.md — primary markdown content (first H1 is the title)
  - meta.yaml — canonical node metadata (YAML)
  - attachments/ — optional binary files
  - images/ — optional images

Example:

```
./docs/42/README.md
./docs/42/meta.yaml
./docs/42/attachments/diagram.png
./docs/42/images/cover.jpg
```

## meta.yaml (metadata)

Do not duplicate meta format here — see the dedicated documentation: [Node meta (meta.yaml)](../18).

## README.md (content)

Do not re-document README conventions here — see the dedicated documentation: [Keg content (keg-content)](../19).

KEG tooling extracts title, lead paragraph, and outgoing numeric links from the README as described in the linked pages.

## Tags & canonical slug pages

Use tags for discovery and automation; if a node is intended to be the canonical page for a tag, follow the parenthetical-slug convention described in the tag docs: [Keg tags (keg-tags)](../10) and the organization guidance: [Organization of this documentation (docs)](../11).

## Indices interaction

Nodes are referenced by dex indices; see the index documentation for details:

- Nodes index: [dex/nodes.tsv](../7)
- Tags index: [dex/tags](../17)
- Links index: [dex/links](../14)
- Backlinks index: [dex/backlinks](../16)

## Best practices

- One topic per node; keep documents focused.
- Update `updated` on any meaningful change (meta.yaml) — see [Node meta (meta.yaml)](../18).
- Normalize tags and avoid duplicates — see [Keg tags (keg-tags)](../10).
- Use relative links for internal references.
- Preserve comments in meta.yaml if possible.
- Use automated index generation rather than manual edits.

## Example (complete)

See the canonical examples in the dedicated pages rather than duplicating them here:

- Meta example: [Node meta (meta.yaml)](../18)
- README/content example: [Keg content (keg-content)](../19)

Keeping nodes small, well-indexed, and consistently normalized makes KEG tooling fast, reliable, and easy to automate.
