# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

**tapper** is a Go CLI toolset for managing KEGs (Knowledge Exchange Graphs). A KEG is a repository of numbered nodes, each containing README.md (content), meta.yaml (metadata), and stats.json (programmatic stats). The system supports indexing, tagging, linking between nodes, and snapshot-based revision history.

Two CLI entrypoints share the same Cobra command tree:
- `tap` — full CLI surface with multi-keg support and user/project config resolution
- `kegv2` — pruned profile with project-local defaults

## Build & Development Commands

```bash
# Build
go build ./cmd/tap
go build ./cmd/kegv2

# Test
go test ./...                              # all tests
go test ./pkg/keg/... -v                   # single package, verbose
go test ./pkg/keg -run TestConcurrentCreate # single test by name
go test -race ./pkg/keg/...               # with race detector

# Install (requires go-task)
task install-tap                           # install tap + zsh completions
task install-keg                           # install kegv2 + zsh completions
task test                                  # cached test run of ./pkg/...

# Lint
go vet ./...
```

## Architecture

### Package Map

- **`pkg/keg/`** — Core KEG library: node CRUD, indexing, repository abstraction, locking, snapshots.
- **`pkg/tapper/`** — User-facing service layer: config resolution, keg discovery, `Tap.Create`/`Edit`/`List`/etc. wrappers that resolve a keg then delegate to `pkg/keg`.
- **`pkg/cli/`** — Cobra command definitions bridging CLI flags to `pkg/tapper` and `pkg/keg`.
- **`pkg/keg_url/`** — Target URL parsing (file://, memory://, API schemes) and expansion.
- **`pkg/lsp/`** — Language Server Protocol support (stub).
- **`pkg/mcp/`** — MCP server: 27 tools exposing the full Tap surface over stdio JSON-RPC. See `docs/ai-coding-agents/mcp-setup.md`.

### Key Types and Flow

**Keg** (`pkg/keg/keg.go`) is the central service. It wraps a `Repository` interface and a `*toolkit.Runtime` (from `cli-toolkit`). All node operations flow through Keg:

```
CLI command → pkg/cli (Cobra) → pkg/tapper.Tap → pkg/keg.Keg → Repository
```

**Repository** (`pkg/keg/repository.go`) is the storage contract with two implementations:
- `MemoryRepo` (`repo_memory.go`) — in-memory, used in tests
- `FsRepo` (`repo_filesystem.go`) — filesystem-backed, numbered directories

**Dex** (`pkg/keg/dex.go`) is the in-memory index aggregator. It holds NodeIndex, TagIndex, LinkIndex, BacklinkIndex, and ChangesIndex. Written as deterministic TSV/markdown files under `dex/`.

**KegService** (`pkg/tapper/keg_service.go`) resolves which keg to use via config precedence: explicit alias → `defaultKeg` → `kegMap` path match → `fallbackKeg` → discovered aliases from `kegSearchPaths` → project-local `./kegs/<alias>`.

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

Config is merged by `ConfigService` in `pkg/tapper/config_service.go`. Project config overrides user config for `defaultKeg`.

### Dependency: cli-toolkit

The `github.com/jlrickert/cli-toolkit` module (local at `../cli-toolkit`) provides `toolkit.Runtime` — the explicit dependency container carrying filesystem, env, clock, logger, hasher, stream, and process identity. All I/O in tapper flows through Runtime, enabling sandboxed test environments.

### Concurrency Model

- **Per-node locking**: `Repository.WithNodeLock(ctx, id, fn)` serializes operations on a single node. FsRepo uses atomic `mkdir` of a `.keg-lock` directory with optional process metadata for stale lock detection. MemoryRepo uses in-process mutex + map.
- **Lock context propagation**: `contextWithNodeLock`/`contextHasNodeLock` allow re-entrant locking within the same call chain.
- **Dex mutex**: `Dex.mu sync.RWMutex` guards index data; `Keg.dexMu` guards lazy initialization.
- **FsRepo.Next()**: Uses atomic mkdir loop to prevent duplicate ID allocation across concurrent callers.
- **KegService cache**: `cacheMu sync.Mutex` guards the shared keg resolution cache.

## Testing

- **Sandbox pattern**: Tests use `sandbox.NewSandbox(t, ...)` from cli-toolkit, which creates a jailed temp directory with a test runtime (mock clock, MD5 hasher, test logger).
- **Fixtures**: `pkg/keg/data/` contains `empty`, `example`, `home` fixtures. `pkg/tapper/data/` contains `basic`, `example`, `keep`.
- **MemoryRepo for speed**: Prefer `NewMemoryRepo(rt)` for unit tests; use FsRepo + sandbox only when testing filesystem behavior.
- **Testify**: Uses `github.com/stretchr/testify/require` for assertions.
- **Race detection**: Run `go test -race ./pkg/keg/...` and `go test -race ./pkg/tapper/...` to verify concurrent safety.

## Error Handling

- Sentinel errors in `pkg/keg/errors.go`: `ErrNotExist`, `ErrExist`, `ErrLock`, `ErrLockTimeout`, `ErrDestinationExists`, etc.
- Typed errors: `BackendError` (with Retryable), `RateLimitError`, `TransientError`.
- Check with `errors.Is()` for sentinels, `errors.As()` for typed errors.

## Feature Surface Checklist

Every new capability added to the Tap API (`pkg/tapper`) must be propagated to all consumer surfaces. Missing a surface leads to incomplete features — for example, an MCP tool that exists but has no matching CLI command, or a CLI command with no documentation node.

When adding or modifying a feature, update each of these:

1. **Tap API** (`pkg/tapper/tap_*.go`) — business logic method with tests
2. **CLI command** (`pkg/cli/cmd_*.go`) — Cobra command wiring flags to the Tap method
3. **Shell completions** — register `ValidArgsFunction` and custom completers for all flags and positional arguments (node IDs, keg aliases, tags, etc.)
4. **MCP tool** (`pkg/mcp/tools_*.go`) — MCP tool exposing the same capability over JSON-RPC, with input struct and `jsonschema` tags
5. **Documentation node** — a Feature entity in the dev keg describing usage, flags, and examples
6. **Tests** — unit tests for the Tap method, CLI integration tests, MCP tool tests, and completion tests

**Configuration changes:** Any change to configuration structure must also update the JSON Schema files under `schemas/`:
- `schemas/tap-config.json` — tap user/project config schema
- `schemas/keg-config.json` — keg config schema

These schemas are referenced by editors for validation and completion hints. A config field added without a schema update will lack editor support and validation.

## Gotchas

- A keg must be initialized (`keg.Init(ctx)`) before Create/SetContent/etc. Init writes the config file and zero node.
- The Dex is lazily loaded and cached; direct `k.dex` assignment is guarded by `k.dexMu`.
- Node content (README.md) and meta (meta.yaml) and stats (stats.json) are separate reads.
- The keg config file is named `keg` (no extension), though `keg.yaml` and `keg.yml` are also accepted.
- `FsRepo.Next()` creates the node directory as a reservation — `WriteContent` must handle pre-existing directories.
- Commit conventions: conventional commits (`feat:`, `fix:`, `refactor:`), summaries ≤72 chars.
