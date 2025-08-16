# Keg content (keg-content)

Content for a [node](../20) in a [KEG](../5): primary human-facing text stored in README.md (or README.rst) that tooling can index, summarize, and link. The first line MUST be a single H1 title (recommended ≤72 characters). Immediately following the title should be a short Lead Paragraph that serves as the node summary.

## Scope

- Primary content file: README.md (default, GitHub-flavored Markdown) or README.rst (reStructuredText).
- Small, focused documents: one topic per node.
- Machine-facing metadata belongs in [meta.yaml](../18); README content is for human-readable explanation, examples, and narrative.

## File & format rules

- File names: README.md or README.rst in the node directory.
- Preferred format: Markdown (GitHub-flavored). If using reStructuredText, ensure tooling can parse it.
- First non-empty line: H1 heading (single '# ...' line). KEG tooling uses this as the canonical title.
- Only one H1 in the document is recommended; use H2/H3 for internal structure.

## Title and Lead Paragraph

- Title extraction:
  - KEG reads the first H1 as the node title.
  - Keep it concise and human-friendly; may include a parenthetical slug (e.g., "Keg tags (keg-tags)").
- Lead Paragraph:
  - The paragraph immediately after the title (separated by a blank line) is the Lead Paragraph.
  - It should be a concise summary (one or two sentences) used by indexes, hub pages, and search snippets.
  - Keep it short and suitable as a node summary in lists.

## Headings and structure

- Use H2/H3 for sections (##, ###); avoid additional H1s.
- Keep documents scannable: short sections, bullet lists, and examples.
- If a document needs many top-level sections, structure them under H2 headings.

## Links and references

- Use relative links for internal node references (e.g., ../42) so repository navigation remains portable.
- Do not duplicate link-resolution rules or alias formats here. The canonical documentation for keg: token resolution and link alias semantics is:
  - Keg links (keg-link): ../21
- For index format and outgoing numeric-link indexing, see:
  - Links index (dex/links): ../14
- For keg-level alias configuration (where aliases are declared), see the repo keg file docs (node 5) and the Keg links doc above.

## Images and attachments

- Store images/attachments under the node directory (images/, attachments/) and reference them with relative paths.
- Large binary blobs should be kept out of meta.yaml and stored under attachments/ or in an external store; reference them from the README.

## Parsing expectations for tooling

KEG tooling will typically extract:

- Title (first H1)
- Lead Paragraph (first paragraph after title)
- Outgoing numeric links (../N) and relative references — see dex/links (../14) and Keg links (../21) for authoritative behavior
- Inline code blocks and fenced examples (preserve for readers)

Keep formatting predictable: blank line after title, then the lead paragraph, then other sections.

## Example README.md

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
- Use relative links for internal references.
- Rely on the canonical link documentation (../21) for link/token resolution rules and on dex/links (../14) for index format — do not duplicate those rules here.
