# Markdown Style Guide

Use this guide to keep KEG notes consistent, linkable, and easy to scan.

## Required Structure

For node `README.md` files in this documentation pattern:

1. First line is a single H1 title (`# ...`).
2. A lead paragraph appears directly under the title.
3. The note remains focused on one atomic idea or execution unit.

## Title Conventions

### General Pattern

Use clear, entity-oriented titles:

```text
# ENTITY: title
```

### Programming Pattern

For programming notes, use:

```text
# ENTITY: title - PROJECT (SLUG)
```

- `ENTITY` is required.
- `PROJECT` is optional.
- `SLUG` is optional and can reference a canonical tag note.

Example:

```markdown
# PATCH: Add resolver precedence tests - tapper (resolver)

This patch updates resolver tests to verify project-first defaults and fallback behavior.
```

## Lead Paragraph

The lead paragraph should:

- summarize the note in 1-3 sentences
- explain why the note exists
- avoid implementation noise unless the note itself is a patch-level detail

## Heading Structure

- Use one H1 per note.
- Use H2/H3 for internal sections only as needed.
- Keep heading names short and descriptive.

## Linking Rules

Interlinking is a core KEG behavior.

- Use relative links for local nodes: `../42`
- Use cross-KEG links when referencing outside the current keg: `keg:pub/921`
- Prefer explicit links over vague references

Recommended execution chain:

- `plan` links to `concept` and `feature`
- `feature` links to `plan`, `task`, and `patch`
- `task` links to `feature` and `patch`
- `patch` links to `task`, `pr`, and `release`
- `release` links to shipped `patch` notes

## Atomic Note Guidelines

- One note should capture one unit of meaning.
- If a section becomes a separate concern, split it into a new node and link it.
- Keep summaries in the current note, details in linked notes.

## Lists And Code Blocks

- Use bullets for short enumerations.
- Use numbered lists for process/sequence.
- Fence command snippets with language hints (`bash`, `yaml`, `json`, `markdown`).
- Keep commands copy-paste ready.

## Metadata Alignment

- Keep `meta.yaml` entity/tags aligned with the README content.
- Keep `stats.json` title aligned with README intent.
- Update links as soon as related notes move or split.

## Good And Bad Example

Good:

```markdown
# PLAN: Implement release guardrails - tapper (release)

This plan connects release failures to concrete workflow changes and test checkpoints.

## Steps

1. Add tag existence checks.
2. Add dirty-tree cleanup before release.
3. Link to patch and release notes.

Related notes: `../410`, `../411`
```

Bad:

```markdown
# Stuff

Lots of unrelated thoughts, no links, no lead, no entity context.
```

## Reference

- Interlinking best practices: `keg:pub/921`
