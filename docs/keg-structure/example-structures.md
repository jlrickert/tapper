# Example Keg Structures

This page provides concrete starter layouts for common domains, including a
project-specific keg pattern.

## Programming KEG Example

### Example Layout

```text
programming-keg/
  keg
  dex/
    nodes.tsv
    changes.md
    links
    backlinks
    tags
  0/
    README.md
    meta.yaml
    stats.json
  1/
    README.md
    meta.yaml
    stats.json
  2/
    README.md
    meta.yaml
    stats.json
```

### Example Node Metadata

`1/meta.yaml`

```yaml
entity: patch
tags:
  - project-a
  - golang
  - cli
```

`1/stats.json`

```json
{
  "title": "Patch: Add config precedence tests",
  "created": "2026-02-26T00:00:00Z",
  "updated": "2026-02-26T00:00:00Z"
}
```

### Programming Title Convention

For programming notes, a consistent title pattern:

```text
# ENTITY: title - PROJECT (SLUG)
```

- `ENTITY` is the note type (e.g. `SYSTEM`, `CONCEPT`, `REFERENCE`, `TASK`, `PLAN`, `FEATURE`, `PATCH`, `PR`, `RELEASE`, `GUIDE`).
- `- PROJECT` is optional and associates the note with a project.
- `(SLUG)` is optional and designates a canonical tag note.

Example:

```markdown
# PATCH: Add resolver precedence tests - tapper (resolver)

This patch updates resolution precedence tests so project and user defaults are validated in one
place.
```

## Project-Specific KEG Example

Use this pattern when one repository should have its own dedicated knowledge
graph with project-scoped defaults.

### Bootstrap Commands

```bash
tap repo init --keg tapper --project
tap repo config --project
tap config --project
```

### Example Layout

```text
my-project/
  .tapper/
    config.yaml
  kegs/
    tapper/
      keg
      dex/
        nodes.tsv
        changes.md
        links
        backlinks
        tags
      0/
        README.md
        meta.yaml
        stats.json
      100/
        README.md
        meta.yaml
        stats.json
      101/
        README.md
        meta.yaml
        stats.json
```

### Example Project Config

`.tapper/config.yaml`:

```yaml
defaultKeg: tapper
fallbackKeg: tapper
kegMap: []
kegs:
  tapper:
    file: kegs/tapper
kegSearchPaths:
  - kegs
defaultHub: knut
```

## Baker KEG Example

### Example Layout

```text
baker-keg/
  keg
  dex/
    nodes.tsv
    changes.md
    links
    backlinks
    tags
  0/
    README.md
    meta.yaml
    stats.json
  10/
    README.md
    meta.yaml
    stats.json
  11/
    README.md
    meta.yaml
    stats.json
  12/
    README.md
    meta.yaml
    stats.json
```

### Example Node Metadata

`10/meta.yaml`

```yaml
entity: recipe
tags:
  - bread
  - sourdough
```

`11/meta.yaml`

```yaml
entity: bake
tags:
  - bread
  - sourdough
  - needs-adjustment
```

`11/stats.json`

```json
{
  "title": "Bake: Country sourdough test run #3",
  "created": "2026-02-26T00:00:00Z",
  "updated": "2026-02-26T00:00:00Z"
}
```

## Interlinking And Atomic Notes

Interlinking is a core KEG behavior. Notes should be atomic and linked explicitly.

- Keep each note focused on one idea or one execution unit.
- Link between related notes rather than combining multiple topics in one note.
- Use relative KEG links for local notes (for example `../42`).
- Use cross-KEG links for references across kegs (for example `keg:pub/921`).

## Notes

- Every keg should include `system` nodes to define its conventions and rules.
- Use these as starter templates for your real workflow.
- Keep `0/` as a stable root node in both structures.
- Keep node files at least to the practical minimum described in
  [Minimum Keg Node](minimum-node.md).
