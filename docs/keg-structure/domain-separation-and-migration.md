# Domain Separation And Migration

When a keg contains multiple domains with weak overlap, split them.

## Signal To Split

Split into separate kegs when:

- cross-links between domains are rare
- tags are overloaded to compensate for entity mismatch
- navigation and index pages mix unrelated workflows

A common case is specialized domain notes living in a broad organization keg
with minimal interaction.

## Recommended Domain Layout

- `@acme/general`: broad cross-domain organization notes
- `@acme/engineering`: project, architecture, release, and incident memory
- `@acme/product`: customer, roadmap, and product decision memory
- `@me/private`: personal notes in a private Hub namespace
- `@acme/domain-x`: a dedicated specialized knowledge base

## Migration Plan: Move A Low-Overlap Domain Out Of A General Keg

1. Create a dedicated destination keg for the domain.
2. Define domain entities in the destination keg.
3. Define tag semantics in the destination keg.
4. Move domain nodes from the source keg to the destination keg.
5. Add cross-keg links from old notes where needed.
6. Reindex both kegs and validate backlinks.

## Bootstrap Commands

Create a destination keg target:

```bash
tap keg create @acme/domain-x
```

Inspect and edit new keg settings:

```bash
tap keg settings --keg @acme/domain-x
tap keg settings edit --keg @acme/domain-x
```

## Guardrails During Migration

- Keep old note ids/links documented in a migration mapping file.
- Avoid changing entity/tag rules mid-migration.
- Perform migration in batches and validate after each batch.
- Keep a final checklist for unresolved cross-keg links.
