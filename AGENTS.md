# AGENTS.md

**tapper** is a Go CLI toolset for managing KEGs (Knowledge Exchange Graphs).
A KEG is a repository of numbered nodes, each containing README.md (content),
meta.yaml (metadata), and stats.json (programmatic stats), with indexing,
tagging, linking, and snapshot history. The `tap` CLI and the MCP server are
peer surfaces over the same Tap API; KEG data lives on a Tapper Hub.

This file is the entry point for every agent and contributor: the workflow and
the rules that are expensive to get wrong. Detail lives under [`docs/`](docs/),
which is the source of truth — this file stays small and delegates.

## Never do this

- **Never install a development build of `tap` on the host machine.** Not
  `go install ./cmd/tap`, not `task install-tap`, not a `go build -o` into a
  directory on `PATH`. The Claude Code plugin's MCP server runs plain
  `tap mcp` off `PATH`, so one stray install silently changes what every agent
  session on that machine talks to, and a development build is unversioned
  against the Hub's API contract — a `version dev` binary left every session
  failing with `400 Bad Request: send Tapper-API-Version`. Hosts run released
  builds only (`brew install jlrickert/formulae/tapper`). Unstable builds are
  tested in a container: `task sandbox:shell`. `task install-tap` exists for a
  human to run deliberately; an agent never runs it and never offers to. See
  [Installing Binaries](docs/development/commands.md#installing-binaries).
- **Never do I/O outside `toolkit.Runtime`** in `pkg/keg`, `pkg/tapper`,
  `pkg/cli`, or `pkg/integrations`. No `os.ReadFile`, `os.Stat`, `os.Stdout`,
  `time.Now()`, or bare `exec.Command`. Direct stdlib calls escape the test
  sandbox's jail. `task lint:fs` enforces the filesystem half. See the
  [Runtime Abstraction Rule](docs/architecture/runtime-and-concurrency.md#runtime-abstraction-rule).
- **Never reach into a hub's internals.** Tapper knows a Tapper Hub by its
  REST endpoint and API contract — nothing else. Its storage engine, schema,
  repository code, task names, and `go.mod` are not Tapper's business and do
  not belong in this repository's code, docs, or workflow. A change that spans
  both is coordinated from the hub side, which depends on Tapper, not the
  reverse.
- **Never ship a feature on one surface.** CLI and MCP are peers; a capability
  missing from either is a bug. See [Feature Organization](docs/development/feature-organization.md).
- **Never use Conventional Commits breaking-change syntax** (`type!:` or a
  `BREAKING CHANGE:` footer) while Tapper is on `v0.x`. Automatic versioning
  must never cross to `v1.0.0`; a stable `v1` needs explicit user direction and
  an explicit `version` workflow override.
- **Never open a PR, merge, tag, release, or publish unless asked.** These are
  separately authorized actions.

## Workflow

1. **Read** the [architecture](docs/architecture/README.md) page for the layer
   you are touching.
2. **Branch from `main`.**
3. **Change the code as a vertical slice**: Tap API → CLI command and
   completions → MCP tool → tests → docs. Configuration changes also update the
   JSON Schemas under `schemas/`.
4. **Test**: `go test ./...`, plus `-race` for `pkg/keg`, `pkg/tapper`, and
   `pkg/parity`.
5. **Lint**: `go vet ./...`, `task lint:docs`, `task lint:fs`.
6. **Commit** with Conventional Commits, summaries under 72 characters.

## Read before working on…

| Working on | Required reference |
| --- | --- |
| Changing Tapper itself: build, test, lint, install | [Development](docs/development/README.md) |
| Package layout, key types, storage model | [Architecture](docs/architecture/README.md) |
| I/O, locking, indexing, remote operations | [Runtime And Concurrency](docs/architecture/runtime-and-concurrency.md) |
| Config, keg, hub, and flight resolution | [Configuration Resolution](docs/architecture/configuration-resolution.md) |
| Adding or changing a feature | [Feature Organization](docs/development/feature-organization.md) |
| Tests and fixtures | [Testing](docs/development/testing.md) |
| Branching, commits, versioning, error handling | [Conventions](docs/development/conventions.md) |
| Connecting agents, MCP setup, launchers | [AI Coding Agents](docs/ai-coding-agents/README.md) |
| Behavior that has surprised people before | [Gotchas](docs/development/gotchas.md) |
