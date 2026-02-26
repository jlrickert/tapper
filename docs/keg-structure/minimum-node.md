# Minimum Keg Node

This page documents the bare minimum for a node, plus the recommended practical minimum.

## Technical Minimum

A node is recognized by directory name under the keg root:

```text
<keg-root>/<node-id>/
```

Where `<node-id>` is a valid node id such as `0`, `1`, `2`, etc.

For filesystem repos, node existence is directory-based. In other words, a directory named as a
valid node id is enough for the repo to treat it as a node.

## Practical Minimum (Required Pattern For These Docs)

For usable, index-friendly notes, create these files:

- `<keg-root>/<node-id>/README.md`
- `<keg-root>/<node-id>/meta.yaml`
- `<keg-root>/<node-id>/stats.json`

### Example

```text
kegs/my-keg/
  42/
    README.md
    meta.yaml
    stats.json
```

`README.md`:

```markdown
# Concept: Hydration adjustments

Short lead paragraph describing the note.
```

For this documentation pattern, `README.md` should contain:

- a title line (`# ...`)
- a lead paragraph directly under the title

`meta.yaml`:

```yaml
entity: concept
tags:
  - baking
  - hydration
```

`stats.json`:

```json
{
  "title": "Concept: Hydration adjustments",
  "created": "2026-02-26T00:00:00Z",
  "updated": "2026-02-26T00:00:00Z"
}
```

## Special Node: Zero Node

Every keg should have node `0` as a stable placeholder/root note.

Typical file:

- `<keg-root>/0/README.md`

## Notes

- `meta.yaml` supports manual metadata and tags.
- `stats.json` is the canonical programmatic stats file.
- Empty or missing metadata files are tolerated, but complete files make indexing and migration
  significantly easier.
