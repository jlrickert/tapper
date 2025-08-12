# Keg content (keg-content)

Content for a [node](../20) in a [KEG](../5): primary human-facing text stored in [README.md](../19) (or README.rst) that tooling can index, summarize, and link. The first line MUST be a single H1 title (recommended ≤72 characters). Immediately following the title should be a short Lead Paragraph that serves as the node summary.

## Scope

- Primary content file: [README.md](../19) (default, GitHub-flavored Markdown) or README.rst (reStructuredText).
- Small, focused documents: one topic per node.
- Machine-facing metadata belongs in [meta.yaml](../18); [README.md](../19) content is for human-readable explanation, examples, and narrative.

## File & format rules

- File names: [README.md](../19) or README.rst in the node directory.
- Preferred format: Markdown (GitHub-flavored). If using reStructuredText, ensure tooling can parse it.
- First non-empty line: H1 heading (single '# ...' line). [KEG](../5) tooling uses this as the canonical title.
- Only one H1 in the document is recommended; use H2/H3 for internal structure.

## Title and Lead Paragraph

- Title extraction:
  - [KEG](../5) reads the first H1 as the node title.
  - Keep it concise and human-friendly; may include a parenthetical slug (e.g., "[Keg tags (keg-tags)](../10)").
- Lead Paragraph:
  - The paragraph immediately after the title (separated by a blank line) is the Lead Paragraph.
  - It should be a concise summary (one or two sentences) used by indexes, hub pages, and search snippets.
  - Keep it short and suitable as a node summary in lists.

## Headings and structure

- Use H2/H3 for sections (##, ###); avoid additional H1s.
- Keep documents scannable: short sections, bullet lists, and examples.
- If a document needs many top-level sections, structure them under H2 headings.

## Links and references

- [KEG](../5) tooling extracts internal numeric links (../N) and relative references for the [dex/links](../14) index.
- Use relative links for other [Keg node](../15) references (e.g., ../42) so repo navigation remains portable.
- When referencing external sites, prefer absolute URLs and avoid embedding credentials.
- Tooling will try to parse inline Markdown links and standard reStructuredText references; keep link targets explicit to aid discovery.

## Images and attachments

- Store images/attachments under the node directory (images/, attachments/) and reference them with relative paths.
- Large binary blobs should be kept out of [meta.yaml](../18) and stored under attachments/ or in an external store; reference them from the README.

## Parsing expectations for tooling

- [KEG](../5) needs to reliably extract:
  - Title (first H1)
  - Lead Paragraph (first paragraph after title)
  - Outgoing numeric links (../N) for [dex/links](../14)
  - Inline code blocks and fenced examples (preserve for readers)
- Keep formatting predictable: blank line after title, then the lead paragraph, then other sections.

## Example [README.md](../19)

```markdown
# Keg content (keg-content)

Concise summary: what this node documents and why it exists.

## Background

Short background or motivation.

## Example

A small code or usage example.
```

## Best practices

- Single responsibility: one topic per node.
- Keep the title and lead paragraph up to date; update the `updated` field in [meta.yaml](../18) when content changes.
- Avoid multiple H1s — use H2/H3 for structure.
- Prefer short lead paragraphs suitable as summaries in index views.
- Use relative links for internal references so [Keg index (keg-index)](../6) and tag hub pages (see [Keg tags (keg-tags)](../10)) can resolve destinations.

If you need stricter conventions (front-matter, enforced summary length, or machine-parsable front-matter in the README), document those expectations here and update the indexer ([Keg index (keg-index)](../6)) to validate them.

