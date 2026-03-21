# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with
code in this repository.

## Overview

**tapper** is a Go CLI toolset for managing KEGs (Knowledge Exchange Graphs). A
KEG is a repository of numbered nodes, each containing README.md (content),
meta.yaml (metadata), and stats.json (programmatic stats). The system supports
indexing, tagging, linking between nodes, and snapshot-based revision history.

Two CLI entrypoints share the same Cobra command tree:

- `tap` — full CLI surface with multi-keg support and user/project config
  resolution
- `keg` — pruned profile with project-local defaults

## Build & Development Commands

```bash
# Build
go build ./cmd/tap
go build ./cmd/keg

# Test
go test ./...                              # all tests
go test ./pkg/keg/... -v                   # single package, verbose
go test ./pkg/keg -run TestConcurrentCreate # single test by name
go test -race ./pkg/keg/...               # with race detector

# Install (requires go-task)
task install-tap                           # install tap + zsh completions
task install-keg                           # install keg + zsh completions
task test                                  # cached test run of ./pkg/...

# Lint
go vet ./...
```

## Architecture

### Package Map

- **`pkg/keg/`** — Core KEG library: node CRUD, indexing, repository
  abstraction, locking, snapshots.
- **`pkg/tapper/`** — User-facing service layer: config resolution, keg
  discovery, `Tap.Create`/`Edit`/`List`/etc. wrappers that resolve a keg then
  delegate to `pkg/keg`.
- **`pkg/cli/`** — Cobra command definitions bridging CLI flags to `pkg/tapper`
  and `pkg/keg`.
- **`pkg/keg_url/`** — Target URL parsing (file://, memory://, API schemes) and
  expansion.
- **`pkg/lsp/`** — Language Server Protocol support (stub).
- **`pkg/mcp/`** — MCP server: 31 tools exposing the full Tap surface over stdio
  JSON-RPC. See `docs/ai-coding-agents/mcp-setup.md`.

### Key Types and Flow

**Keg** (`pkg/keg/keg.go`) is the central service. It wraps a `Repository`
interface and a `*toolkit.Runtime` (from `cli-toolkit`). All node operations
flow through Keg via two parallel entry points:

```
CLI command   → pkg/cli (Cobra)    → pkg/tapper.Tap → pkg/keg.Keg → Repository
MCP tool call → pkg/mcp (JSON-RPC) → pkg/tapper.Tap → pkg/keg.Keg → Repository
```

Both paths converge at `pkg/tapper.Tap`, sharing the same method and `*Options`
struct for each feature. The CLI path uses `applyKegTargetProfile()` to resolve
Cobra flags into options and writes results to stdout. The MCP path uses
`resolveKegTarget()` with input structs annotated via `jsonschema` tags and
returns `CallToolResult` values. Server wiring in `NewServer()` calls 14
`register*Tools()` functions to expose the full Tap surface over stdio JSON-RPC.

**Repository** (`pkg/keg/repository.go`) is the storage contract with two
implementations:

- `MemoryRepo` (`repo_memory.go`) — in-memory, used in tests
- `FsRepo` (`repo_filesystem.go`) — filesystem-backed, numbered directories

**Dex** (`pkg/keg/dex.go`) is the in-memory index aggregator. It holds
NodeIndex, TagIndex, LinkIndex, BacklinkIndex, and ChangesIndex. Written as
deterministic TSV/markdown files under `dex/`.

**KegService** (`pkg/tapper/keg_service.go`) resolves which keg to use via
config precedence: explicit alias → `defaultKeg` → `kegMap` path match →
`fallbackKeg` → discovered aliases from `kegSearchPaths` → project-local
`./kegs/<alias>`.

### Storage Model

```
<keg-root>/
  keg                  # KEG config (YAML, versioned with kegv field)
  0/                   # Zero node (always present after init)
    README.md          # Content (markdown)
    meta.yaml          # User-facing metadata (tags, links, title)
    stats.json         # Programmatic stats (hash, timestamps, access count)
  1/
    README.md
    meta.yaml
    stats.json
  dex/                 # Generated indices
    nodes.tsv          # ID → timestamp → title
    tags               # tag → node IDs
    links              # source → destinations
    backlinks          # destination → sources
    changes.md         # Reverse-chronological changelog
```

### Config Hierarchy

- User config: `~/.config/tapper/config.yaml`
- Project config: `.tapper/config.yaml`
- Keg config: `<keg-root>/keg`

Config is merged by `ConfigService` in `pkg/tapper/config_service.go`. Project
config overrides user config for `defaultKeg`.

### Dependency: cli-toolkit

The `github.com/jlrickert/cli-toolkit` module (local at `../cli-toolkit`)
provides `toolkit.Runtime` — the explicit dependency container carrying
filesystem, env, clock, logger, hasher, stream, and process identity. All I/O in
tapper flows through Runtime, enabling sandboxed test environments.

### Runtime Abstraction Rule

All I/O in `pkg/keg`, `pkg/tapper`, and `pkg/cli` must go through
`toolkit.Runtime`. Direct stdlib calls bypass the sandboxed test environment and
break test isolation. Specifically:

- **File I/O**: Use `rt.ReadFile` / `rt.WriteFile` — never `os.ReadFile` /
  `os.WriteFile`.
- **Streams**: Use `rt.Stream().Out` / `rt.Stream().Err` — never `os.Stdout` /
  `os.Stderr` directly.
- **Clock**: Use `rt.Clock().Now()` — never `time.Now()`.
- **Commands**: Use `exec.CommandContext(ctx, ...)` — never bare
  `exec.Command(...)`.
- **Exception — fsnotify debounce**: Filesystem-event debounce timers in
  `repo_fs_events.go` must use `time.Now()` because the test clock is frozen and
  wall-clock timing is required for real event coalescing.
- **Exception — append-mode log files**: `os.OpenFile` is acceptable when
  Runtime doesn't provide an equivalent (e.g., opening a file in append mode for
  logging).

### Concurrency Model

- **Per-node locking**: `Repository.WithNodeLock(ctx, id, fn)` serializes
  operations on a single node. FsRepo uses atomic `mkdir` of a `.keg-lock`
  directory with optional process metadata for stale lock detection. MemoryRepo
  uses in-process mutex + map.
- **Lock context propagation**: `contextWithNodeLock`/`contextHasNodeLock` allow
  re-entrant locking within the same call chain.
- **Dex mutex**: `Dex.mu sync.RWMutex` guards index data; `Keg.dexMu` guards
  lazy initialization.
- **FsRepo.Next()**: Uses atomic mkdir loop to prevent duplicate ID allocation
  across concurrent callers.
- **KegService cache**: `cacheMu sync.Mutex` guards the shared keg resolution
  cache.

## Testing

- **Sandbox pattern**: Tests use `sandbox.NewSandbox(t, ...)` from cli-toolkit,
  which creates a jailed temp directory with a test runtime (mock clock, MD5
  hasher, test logger).
- **Fixtures**: `pkg/keg/data/` contains `empty`, `example`, `home` fixtures.
  `pkg/tapper/data/` contains `basic`, `example`, `keep`.
- **MemoryRepo for speed**: Prefer `NewMemoryRepo(rt)` for unit tests; use
  FsRepo + sandbox only when testing filesystem behavior.
- **Testify**: Uses `github.com/stretchr/testify/require` for assertions.
- **Race detection**: Run `go test -race ./pkg/keg/...` and
  `go test -race ./pkg/tapper/...` to verify concurrent safety.
- **Parity tests**: `pkg/parity/` contains table-driven tests that verify CLI
  commands and MCP tools produce equivalent results for the same Tap API
  operations. The coverage test (`TestCoverage_AllTapMethodsHaveBothSurfaces`)
  uses reflection to check that every exported Tap method has both a CLI command
  and MCP tool registered. When adding a new feature, add a parity test case to
  the appropriate file (`parity_read_test.go`, `parity_write_test.go`, or
  `parity_utility_test.go`). Run with `go test ./pkg/parity/...` and
  `go test -race ./pkg/parity/...`.

## Error Handling

- Sentinel errors in `pkg/keg/errors.go`: `ErrNotExist`, `ErrExist`, `ErrLock`,
  `ErrLockTimeout`, `ErrDestinationExists`, etc.
- Typed errors: `BackendError` (with Retryable), `RateLimitError`,
  `TransientError`.
- Check with `errors.Is()` for sentinels, `errors.As()` for typed errors.

## Feature Organization

Features are vertical slices: each capability cuts through every layer from the
Tap API down to tests. CLI and MCP are peer surfaces — both must expose the same
features at parity.

### Feature anatomy

| Layer             | Location pattern        | Purpose                                                  |
| ----------------- | ----------------------- | -------------------------------------------------------- |
| **Tap API**       | `pkg/tapper/tap_*.go`   | Business logic method + `*Options` struct                |
| **CLI command**   | `pkg/cli/cmd_*.go`      | Cobra command wiring flags to the Tap method             |
| **Completions**   | `pkg/cli/cmd_*.go`      | `ValidArgsFunction` and custom completers for flags/args |
| **MCP tool**      | `pkg/mcp/tools_*.go`    | JSON-RPC tool with input struct and `jsonschema` tags    |
| **Tests**         | `*_test.go` in each pkg | Unit, integration, completion, and MCP tool tests        |
| **Documentation** | `docs/`                 | User-facing docs for the capability                      |

**Example — `create`:** `Tap.Create()` in `tap_create.go` → `createCmd` in
`cmd_create.go` (with node-ID completions) → `registerCreateTools()` in
`tools_create.go` → tests in each package.

### Parity rules

- CLI and MCP must expose the same features. A missing surface is a bug.
- Both accept equivalent parameters that map 1:1 to the shared `*Options`
  struct.
- Tests verify both surfaces produce equivalent results for the same input.
- Docs document features, not surfaces — one description covers both CLI and MCP
  usage.

### Checklist

When adding or modifying a feature, update each of these:

1. **Tap API** (`pkg/tapper/tap_*.go`) — business logic method with tests
2. **CLI command** (`pkg/cli/cmd_*.go`) — Cobra command wiring flags to the Tap
   method
3. **Shell completions** — register `ValidArgsFunction` and custom completers
   for all flags and positional arguments (node IDs, keg aliases, tags, etc.).
   Verify with `go test ./pkg/cli/... -run Completion`
4. **MCP tool** (`pkg/mcp/tools_*.go`) — MCP tool exposing the same capability
   over JSON-RPC, with input struct and `jsonschema` tags
5. **Documentation** — user-facing docs for the new capability
6. **Tests** — unit tests for the Tap method, CLI integration tests, MCP tool
   tests, and completion tests

**Configuration changes:** Any change to configuration structure must also
update the JSON Schema files under `schemas/`:

- `schemas/tap-config.json` — tap user/project config schema
- `schemas/keg-config.json` — keg config schema

These schemas are referenced by editors for validation and completion hints. A
config field added without a schema update will lack editor support and
validation.

## Gotchas

- A keg must be initialized (`keg.Init(ctx)`) before Create/SetContent/etc. Init
  writes the config file and zero node.
- The Dex is lazily loaded and cached; direct `k.dex` assignment is guarded by
  `k.dexMu`.
- Node content (README.md) and meta (meta.yaml) and stats (stats.json) are
  separate reads.
- The keg config file is named `keg` (no extension), though `keg.yaml` and
  `keg.yml` are also accepted.
- `FsRepo.Next()` creates the node directory as a reservation — `WriteContent`
  must handle pre-existing directories.
- Commit conventions: conventional commits (`feat:`, `fix:`, `refactor:`),
  summaries ≤72 chars.
