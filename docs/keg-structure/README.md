# KEG Structure Patterns

This section documents practical patterns for structuring KEGs, based on workflows that are
working well in active kegs.

## Why This Matters

Before adding lots of notes, decide:

- what entity types your keg supports
- how tags are used for slicing and filtering
- whether multiple domains should share one keg or be split

Early decisions here reduce rework and make long-term indexing/search cleaner.

## Start Here

1. Define entities and tag rules:
   [Entity And Tag Patterns](entity-and-tag-patterns.md)
2. Create your initial structure with minimum required node files:
   [Minimum Keg Node](minimum-node.md)
3. Split low-overlap domains into separate kegs when needed:
   [Domain Separation And Migration](domain-separation-and-migration.md)
4. Use concrete starter layouts by domain:
   [Example Keg Structures](example-structures.md)
5. Apply consistent note writing conventions:
   [Markdown Style Guide](markdown-style-guide.md)

## Design Principles

- Keep entity sets small at the beginning.
- Use tags for cross-cutting concerns, not as a replacement for entities.
- Prefer one keg per high-level domain when cross-links are rare.
- Keep zero node (`0`) as a stable landing node in every keg.
