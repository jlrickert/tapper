# Development

This section documents how to work on Tapper itself. The rest of `docs/` is
written for people using Tapper; these pages are for people changing it.
[AGENTS.md](../../AGENTS.md) is the entry point and carries the short list of
rules that are expensive to get wrong; the detail lives here.

## Audience

Use these docs when you are:

- adding or changing a feature across the Tap API, CLI, and MCP surfaces
- writing or fixing tests
- building, linting, or installing binaries
- committing, branching, or preparing a change for release

For how the code is structured, read [Architecture](../architecture/README.md)
first — especially [Runtime And Concurrency](../architecture/runtime-and-concurrency.md),
whose Runtime rule constrains every change.

## Workflow

1. **Read** the architecture page for the layer you are touching.
2. **Branch from `main`.**
3. **Change the code** as a vertical slice: a feature cuts from the Tap API
   through both the CLI and MCP surfaces. See
   [Feature Organization](feature-organization.md) — a missing surface is a bug,
   not a follow-up.
4. **Test**: `go test ./...`, plus `-race` for `pkg/keg`, `pkg/tapper`, and
   `pkg/parity`. See [Testing](testing.md).
5. **Lint**: `go vet ./...`, `task lint:docs`, `task lint:fs`.
6. **Commit** with Conventional Commits, no breaking-change syntax. See
   [Conventions](conventions.md).
7. **Open a PR only when asked.** Merges, tags, releases, and publishing are
   separately authorized actions.

## Read Next

- [Commands](commands.md) — build, test, lint, and the binary install rule
- [Testing](testing.md)
- [Feature Organization](feature-organization.md)
- [Conventions](conventions.md) — branching, commits, versioning, error handling
- [Gotchas](gotchas.md)
