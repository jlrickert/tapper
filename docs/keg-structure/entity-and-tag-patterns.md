# Entity And Tag Patterns

A reliable pattern is to define entity types first, then define tag conventions.

## Step 1: Define Initial Entities

Use the keg-level `entities` map in `<keg-root>/keg` to declare canonical entity types and
reference notes.

Example:

```yaml
entities:
  project:
    id: 100
    summary: "Project home notes and rollups"
  feature:
    id: 101
    summary: "Feature-level outcomes for a project"
  task:
    id: 102
    summary: "Actionable units of work"
  patch:
    id: 103
    summary: "Implemented change notes"
  guide:
    id: 104
    summary: "How-to documentation"
  concept:
    id: 105
    summary: "Design and architecture ideas"
```

## Step 2: Define Tag Semantics

Use keg-level `tags` to define what a tag means. Keep tags simple and lowercase.

Example:

```yaml
tags:
  golang: "Go language and tooling"
  release: "Release process and versioning"
  ci: "Automation and pipeline notes"
```

## Step 3: Apply Entities And Tags Per Node

At node level (`<id>/meta.yaml`), apply:

- a primary entity
- focused tags (2-6 tags is a practical range)

Example:

```yaml
entity: patch
tags:
  - tapper
  - golang
  - release
```

## Pattern: Software Notes

For software-heavy kegs, a useful starter set:

- `project`
- `feature`
- `task`
- `patch`
- `concept`
- `guide`

Tag dimensions that usually scale well:

- language (`golang`, `typescript`)
- subsystem (`config`, `resolver`, `cli`)
- lifecycle (`planned`, `active`, `done`)

## Pattern: Domain-Specific Notes

For any focused domain, define entities around stable object types and note intent.
Example starter set:

- `item`
- `process`
- `run`
- `concept`
- `guide`

Tag dimensions that usually scale well:

- category (`primary`, `secondary`)
- method (`method-a`, `method-b`)
- outcome (`successful`, `needs-adjustment`)

## Practical Rules

- Entities describe note type.
- Tags describe facets of the note.
- Do not create entity types for every small variation.
- Split domains into separate kegs when links between domains are rare.
