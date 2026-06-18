# KEG Structure Patterns

This section documents practical patterns for structuring KEGs so project memory
stays useful to both people and AI agents.

## Why This Matters

Before adding lots of notes, decide:

- which domains belong in shared organization kegs
- which domains need private or restricted kegs
- how tags are used for slicing and filtering
- whether multiple domains should share one keg or be split

Early decisions here reduce onboarding friction and make long-term recall more
reliable.

## Start Here

1. Create your initial structure with minimum required node files:
   [Minimum Keg Node](minimum-node.md)
2. Split low-overlap domains into separate kegs when needed:
   [Domain Separation And Migration](domain-separation-and-migration.md)
3. Use concrete starter layouts by domain:
   [Example Keg Structures](example-structures.md)
4. Apply consistent note writing conventions:
   [Markdown Style Guide](markdown-style-guide.md)

## Design Principles

- Prefer one keg per high-level domain when cross-links are rare.
- Keep zero node (`0`) as a stable landing node in every keg.
