# Example Keg Structures

This page provides concrete starter layouts for common domains, including a
project-specific keg pattern.

## Programming KEG Example

### Suggested Entities

```yaml
entities:
  project:
    id: 100
    summary: "Project home and rollup notes"
  task:
    id: 101
    summary: "Actionable work items"
  feature:
    id: 102
    summary: "Feature notes tied to project outcomes"
  plan:
    id: 103
    summary: "Connect business concepts to technical execution"
  patch:
    id: 104
    summary: "Implemented changes"
  concept:
    id: 105
    summary: "Design and architecture ideas"
  guide:
    id: 106
    summary: "How-to and operating guides"
  release:
    id: 107
    summary: "Release notes and release execution records"
  pr:
    id: 108
    summary: "Pull request notes and review tracking"
```

### Suggested Tags

```yaml
tags:
  golang: "Go language notes"
  cli: "CLI behavior and UX"
  config: "Configuration model and examples"
  release: "Release automation and versioning"
  pr: "Pull request and review lifecycle"
  architecture: "Architecture and design decisions"
  business: "Business context and requirements"
  testing: "Tests and validation"
```

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

- `ENTITY` is the note type (`PLAN`, `FEATURE`, `TASK`, `PATCH`, `CONCEPT`, `GUIDE`, `RELEASE`, `PR`).
- `- PROJECT` is optional and associates the note with a project.
- `(SLUG)` is optional and designates a canonical tag note.

Example:

```markdown
# PATCH: Add resolver precedence tests - tapper (resolver)

This patch updates resolution precedence tests so project and user defaults are validated in one
place.
```

### Plan-Centric Execution Flow

A common programming flow is:

1. `concept` captures business/architecture reasoning.
2. `plan` connects business intent to implementation steps.
3. `feature` tracks the feature-level outcome for a project.
4. `task` tracks executable work items.
5. `patch` documents completed implementation increments.
6. `pr` records review decisions and merge context.
7. `release` summarizes shipped changes.

## Project-Specific KEG Example

Use this pattern when one repository should have its own dedicated knowledge
graph with project-scoped defaults.

### Bootstrap Commands

```bash
tap repo init tapper --project
tap repo config --project
tap config --project
```

### Suggested Entities

```yaml
entities:
  plan:
    id: 300
    summary: "Connect business intent to technical execution"
  feature:
    id: 301
    summary: "Feature note scoped to the project"
  task:
    id: 302
    summary: "Actionable implementation work items"
  patch:
    id: 303
    summary: "Completed implementation increments"
  pr:
    id: 304
    summary: "Pull request lifecycle and review outcomes"
  release:
    id: 305
    summary: "Release execution and published changes"
  concept:
    id: 306
    summary: "Architecture and design concepts"
  guide:
    id: 307
    summary: "Operating and maintenance guidance"
```

### Suggested Tags

```yaml
tags:
  architecture: "Architecture and system design notes"
  business: "Business requirements and context"
  backlog: "Planned but not implemented work"
  in-progress: "Actively implemented work"
  shipped: "Released work"
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
defaultRegistry: knut
```

## Baker KEG Example

### Suggested Entities

```yaml
entities:
  ingredient:
    id: 200
    summary: "Ingredient reference notes"
  recipe:
    id: 201
    summary: "Recipe definitions"
  bake:
    id: 202
    summary: "Execution logs of baking runs"
  concept:
    id: 203
    summary: "Technique and theory notes"
  guide:
    id: 204
    summary: "How-to and process guides"
```

### Suggested Tags

```yaml
tags:
  bread: "Bread-focused notes"
  pastry: "Pastry-focused notes"
  sourdough: "Sourdough method notes"
  enriched-dough: "Enriched dough method notes"
  successful: "Outcome met expectation"
  needs-adjustment: "Outcome needs iteration"
```

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

## Personal KEG Additional Entities (Non-Exhaustive)

If your personal keg spans broader knowledge domains, these entities are common
additions:

- `hardware`: a physical thing
- `gear`: hardware that you own
- `software`: a piece of software
- `article`: summary/description of external content (blog, video, PDF, etc.)
- `person`: an individual
- `company`: an organization

Example:

```yaml
entities:
  hardware:
    id: 400
    summary: "A physical device or component"
  gear:
    id: 401
    summary: "Hardware inventory that is personally owned"
  software:
    id: 402
    summary: "Software tools, services, and applications"
  article:
    id: 403
    summary: "Summaries of external content"
  person:
    id: 404
    summary: "Individual profiles"
  company:
    id: 405
    summary: "Organization profiles"
```

## Interlinking And Atomic Notes

Interlinking is a core KEG behavior. Notes should be atomic and linked explicitly.

- Keep each note focused on one idea or one execution unit.
- Link between related notes rather than combining multiple topics in one note.
- Use relative KEG links for local notes (for example `../42`).
- Use cross-KEG links for references across kegs (for example `keg:pub/921`).

Recommended link chain for execution work:

- `plan` links to `concept` and `feature`
- `feature` links to `plan`, `task`, and `patch`
- `task` links to `feature` and `patch`
- `patch` links to `task`, `pr`, and `release`
- `release` links to shipped `patch` notes

Interlink guidance reference: `keg:pub/921`.

## Notes

- Use these as starter templates, then tune entities and tags for your real workflow.
- Keep `0/` as a stable root node in both structures.
- Keep node files at least to the practical minimum described in
  [Minimum Keg Node](minimum-node.md).
