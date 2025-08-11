# Organization of this documentation (docs)

This documentation set uses a simple convention: when a node's title ends with a parenthetical slug (for example, "Keg tags (keg-tags)"), that node is the canonical documentation page for the tag named by the parenthetical slug. The human-readable title before the parentheses is for display; the parenthetical slug is a short, machine-friendly identifier used for indexing and automation.

## Summary of the convention

- Canonical tag page: A node whose title ends with "(tag-slug)" documents the tag named tag-slug.
- Slug form: Prefer lowercase, hyphen-separated tokens (example: keg-tags, api, zeke).
- Discovery: The same slug should appear in the node’s meta.yaml tags list so indexers and tools can discover the page.
- Purpose: Treat the canonical page as the single source of truth for the tag’s meaning, intended usage, examples, and best practices.

## Why this helps

- Human + machine friendly: Titles remain readable while providing a stable identifier for scripts and indexes.
- Clear landing page: Tag hub pages and automation can point to the canonical page for concise documentation about what the tag means.
- Easier tooling: Indexers can prefer nodes with parenthetical slugs when generating tag documentation or tag lists.

Recommended meta.yaml snippet
Include a matching slug in the node metadata. Example meta.yaml for the "keg-tags" page:

```yaml
updated: 2025-08-09 00:00:00Z
title: Keg tags (keg-tags)
tags:
  - keg-tags
  - tags
  - metadata
```

## How to use this convention

- Create tag docs: When you intend a node to document a tag, end the title with the slug in parentheses and add the slug to meta.yaml tags.
- Indexing: Keep dex/nodes.tsv and dex/tags updated. Indexers should map tag → node ids from meta.yaml and prefer the parenthetical-title node as the tag's canonical page.
- Linking: Link to the canonical page from tag hub pages, changelogs, and indexes (use relative links like ../N where appropriate).
- Stability: Keep slugs stable. If you rename a slug, update meta.yaml, the title, and regenerate indexes.

## Best practices

- Keep slugs short and descriptive (eg. zeke, keg-tags, api-design).
- Use hyphens for multi-word slugs (avoid spaces and punctuation).
- Duplicate the slug in meta.yaml tags to ensure the node is discoverable by tag-based lookups.
- Use namespaces only when helpful (e.g., lang-go or pkg:zeke) — be consistent across the repo.
- Reserve parenthetical slugs for pages that truly describe a tag (don’t use parentheses purely for internal identifiers).

## CLI examples

- List all tags:
  - ku tags
- List nodes for a tag:
  - ku tags keg-tags
- Show canonical page for a tag (indexer/automation behavior):
  - prefer node whose title matches regex ".\*\(keg-tags\)$"

## Automation & indexing guidance

- Tag index (dex/tags):
  - Map tag → node ids (one line per tag: tag id1 id2 ...).
  - The indexer should read meta.yaml tags for mapping.
- Canonical tag detection:
  - Prefer nodes whose title ends with "(<tag>)" as the canonical documentation node for that tag.
  - If multiple nodes match, prefer the most recently updated or warn for manual review.
- Tag hub generation (recommended layout):
  - Top: canonical tag page (if found) with link and short summary
  - Then: list of nodes that carry the tag (id, title, one-line summary), sorted by updated timestamp
- Example indexer pseudocode:

```text
for each tag T in dex/tags:
  canonical = find node where title matches /.*\(\s*T\s*\)$/
  members = list nodes from dex/tags[T]
  sort members by updated desc
  render hub:
    if canonical:
      render "Canonical: [Title (T)](../id-of-canonical)"
    render members list
```

## Examples

- "Keg tags (keg-tags)" → canonical for tag: [keg-tags](../10)
- "Zeke AI utility (zeke)" → canonical for tag: [zeke](../3)

## Caveats & notes

- This is a convention, not an enforced rule. Tooling must be configured to treat parenthetical titles specially.
- Avoid using the parenthetical slug for transient notes. Keep it for durable, descriptive tag documentation.
- If you adopt a different tokenization (e.g., using colons for namespaces), document that choice and ensure the indexer understands the format.
