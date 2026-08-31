# Minimum Keg Node

This page documents the bare minimum for a node, plus the recommended practical minimum.

## Technical Minimum

A node is a Hub-managed aggregate identified by a numeric ID such as `0`, `1`,
or `42`. It exists only after the Hub creates it through `POST /nodes`; creating
a directory or local file never creates a Tapper node.

## Practical Minimum (Required Pattern For These Docs)

For usable, index-friendly notes, create the node through Tapper with markdown
content containing an explicit H1 title. For example:

```markdown
# Concept: Hydration adjustments

Short lead paragraph describing the note.
```

For this documentation pattern, `README.md` should contain:

- a title line (`# ...`)
- a lead paragraph directly under the title

Optional metadata can be supplied as YAML frontmatter or through `tap meta`:

```yaml
entity: concept
tags:
  - baking
  - hydration
```

Stats and indexes are owned by the Hub and derived from content, metadata, and
access. Clients do not write them.

## Special Node: Zero Node

Every keg has node `0` as a stable placeholder/root note. Leave it unchanged;
write ordinary content in a newly created node.

## Notes

- Metadata supports tags and extension fields.
- Node reads expose content, raw metadata, derived stats, and attachments as one
  aggregate.
- The old filesystem layout is not a supported target or migration source.
