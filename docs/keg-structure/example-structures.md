# Example Keg Structures

This page provides concrete starter layouts for common domains, including a
project-specific keg pattern.

## Programming KEG Example

### Suggested Entities

```yaml
entities:
  system:
    id: 100
    summary: "Keg-level conventions, policies, and operational rules"
  project:
    id: 101
    summary: "Project home and rollup notes"
  concept:
    id: 102
    summary: "Abstract ideas and theory (e.g., dependency injection, event sourcing)"
  reference:
    id: 103
    summary: "Concrete reasoning grounded in concepts — business and architecture context"
  task:
    id: 104
    summary: "Business details and specifications, often linked to external work items"
  plan:
    id: 105
    summary: "Implementation details — how to technically achieve a task"
  feature:
    id: 106
    summary: "Feature notes tied to project outcomes"
  patch:
    id: 107
    summary: "Implemented changes"
  pr:
    id: 108
    summary: "Pull request notes and review tracking"
  release:
    id: 109
    summary: "Release notes and release execution records"
  retrospect:
    id: 110
    summary: "Feedback and reflection on any entity"
  guide:
    id: 111
    summary: "How-to and operating guides"
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

- `ENTITY` is the note type (`SYSTEM`, `CONCEPT`, `REFERENCE`, `TASK`, `PLAN`, `FEATURE`, `PATCH`, `PR`, `RELEASE`, `RETROSPECT`, `GUIDE`).
- `- PROJECT` is optional and associates the note with a project.
- `(SLUG)` is optional and designates a canonical tag note.

Example:

```markdown
# PATCH: Add resolver precedence tests - tapper (resolver)

This patch updates resolution precedence tests so project and user defaults are validated in one
place.
```

### Execution Flow

A common programming flow is:

1. `concept` captures abstract ideas and theory.
2. `reference` grounds concepts into concrete business/architecture reasoning.
3. `task` specifies business details and requirements (often linked to an
   external tracker like Asana).
4. `plan` details the technical implementation approach for a task.
5. `feature` tracks the feature-level outcome for a project.
6. `patch` documents completed implementation increments.
7. `pr` records review decisions and merge context.
8. `release` summarizes shipped changes.
9. `retrospect` reflects on any node or cluster of nodes — a release, a plan, a
   task, or even a concept.

Task and plan typically interplay: a task may spawn multiple plans, and a plan
may reveal that a task needs to be split or refined.

> **Alternative names for `task`:** depending on your workflow, `spec`, `brief`,
> or `story` may better convey the business-specification role. Use whichever
> name fits your team's vocabulary.

## Project-Specific KEG Example

Use this pattern when one repository should have its own dedicated knowledge
graph with project-scoped defaults.

### Bootstrap Commands

```bash
tap repo init --keg tapper --project
tap repo config --project
tap config --project
```

### Suggested Entities

```yaml
entities:
  system:
    id: 300
    summary: "Keg-level conventions, policies, and operational rules"
  concept:
    id: 301
    summary: "Abstract ideas and theory"
  reference:
    id: 302
    summary: "Concrete reasoning grounded in concepts"
  task:
    id: 303
    summary: "Business details and specifications"
  plan:
    id: 304
    summary: "Implementation details for a task"
  feature:
    id: 305
    summary: "Feature note scoped to the project"
  patch:
    id: 306
    summary: "Completed implementation increments"
  pr:
    id: 307
    summary: "Pull request lifecycle and review outcomes"
  release:
    id: 308
    summary: "Release execution and published changes"
  retrospect:
    id: 309
    summary: "Feedback and reflection on any entity"
  guide:
    id: 310
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
  system:
    id: 200
    summary: "Keg-level conventions, policies, and operational rules"
  ingredient:
    id: 201
    summary: "Ingredient reference notes"
  recipe:
    id: 202
    summary: "Recipe definitions"
  bake:
    id: 203
    summary: "Execution logs of baking runs"
  concept:
    id: 204
    summary: "Technique and theory notes"
  retrospect:
    id: 205
    summary: "Feedback on a bake or recipe"
  guide:
    id: 206
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

- `concept` links to related `concept` nodes
- `reference` links to `concept` nodes it builds on
- `task` links to `reference` and `feature`
- `plan` links to `task` it implements
- `feature` links to `task`, `plan`, and `patch`
- `patch` links to `plan`, `pr`, and `release`
- `release` links to shipped `patch` notes
- `retrospect` links to whatever node(s) it evaluates — any entity is fair game

Interlink guidance reference: `keg:pub/921`.

## Entity Interplay Patterns

Entities gain structure through their relationships. Below are common patterns
showing how entities compose into larger workflows.

### Abstraction to Concretion

A `concept` captures a pure abstraction. A `reference` grounds one or more
concepts into concrete business or architecture reasoning for a specific
context. A `task` depends on references to specify the business details, and a
`plan` details how to implement a task technically. Task and plan interplay: a
task may spawn multiple plans, and a plan may reveal that a task needs to be
split or refined.

```text
concept → reference → task ↔ plan → patch → pr → release
```

### Retrospect

A `retrospect` can target any entity. It captures what worked, what didn't, and
what to change. Common targets include releases, plans, tasks, and even
references or concepts. Insights feed forward into new nodes in the next cycle.

```text
retrospect → release    (was the shipment successful?)
retrospect → plan       (was the implementation approach sound?)
retrospect → task       (were the business requirements clear?)
retrospect → reference  (does the reasoning still hold?)
retrospect → concept    (is the abstraction still useful?)
```

### Feature Rollup

A `feature` acts as a rollup point that ties multiple `task` and `patch` nodes
to a single outcome. The feature links down to its tasks and patches, while each
task and patch links back up to the feature.

```text
feature
  ├── task ↔ plan → patch
  ├── task ↔ plan → patch
  └── task ↔ plan → patch → pr
```

### Release Aggregation

A `release` aggregates the patches and PRs that ship together. Each patch links
to its release, and the release links back to the patches it includes.

```text
patch ──┐
patch ──┤
patch ──┼── release
pr ─────┘
```

### Natural Clustering

In practice, entities cluster around a shared concern. A feature, its tasks,
plans, patches, and a retrospect all link to each other and form a natural
group. Tags and project associations reinforce these clusters — browsing by tag
or backlinks reveals the full cluster without requiring a rigid hierarchy.

```text
                  ┌── concept
                  │      ↓
  feature ← task ↔ plan
               │     ↓
               │   patch → pr → release
               │                   ↓
               └── retrospect ─────┘
```

Clusters emerge organically through linking rather than upfront planning. A
single retrospect may span multiple clusters when it evaluates a release that
touches several features.

### System Notes

Every keg should include `system` nodes. They define how the keg itself
functions: conventions, tagging policies, entity definitions, naming rules, and
workflow expectations. Other entities reference system nodes when they need to
follow or cite a keg-level rule.

```text
system (tagging policy)
  ↑ referenced by
  ├── guide (how-to follows tagging rules)
  ├── plan (plan adopts naming convention)
  └── reference (references entity definitions)
```

System nodes are not part of the execution flow — they sit alongside it and
govern how the other entities behave within the keg.

## Notes

- Every keg should include `system` nodes to define its conventions and rules.
- Use these as starter templates, then tune entities and tags for your real workflow.
- Keep `0/` as a stable root node in both structures.
- Keep node files at least to the practical minimum described in
  [Minimum Keg Node](minimum-node.md).
