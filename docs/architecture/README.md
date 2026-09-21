# Architecture Overview

This section documents how tapper is structured internally and how commands move
through the stack.

## Audience

Use these docs when you are:

- adding or changing CLI commands
- changing keg/config resolution behavior
- debugging remote Hub and namespace selection
- extending low-level repository behavior
- writing integration-style CLI tests

## Layered Model

1. CLI entrypoint (`cmd/tap`)
2. Cobra command tree and shared dependencies (`pkg/cli`)
3. Tap client and service layer (`pkg/tapper`)
4. KEG domain and repository abstraction (`pkg/keg`)
5. Server-side repository implementations, owned by the hub
6. Remote test servers and repository-independent memory tests

## Package Map

- **`pkg/keg/`** — Core KEG library: node CRUD, indexing, repository
  abstraction, locking, snapshots.
- **`pkg/tapper/`** — User-facing service layer: config resolution, keg
  discovery, `Tap.Create`/`Edit`/`List`/etc. wrappers that resolve a keg then
  delegate to `pkg/keg`.
- **`pkg/cli/`** — Cobra command definitions bridging CLI flags to `pkg/tapper`
  and `pkg/keg`.
- **`pkg/keg/target.go`** — Target parsing for HTTP(S) and keg-reference
  schemes.
- **`pkg/mcp/`** — MCP server exposing the agent-safe Tap surface over
  stdio JSON-RPC, wired by 19 `register*Tools()` functions in `server.go`. See
  [MCP Setup](../ai-coding-agents/mcp-setup.md).

## Key Types And Flow

**Keg** (`pkg/keg/keg_iface.go`) is the single-keg business **interface**:
every method is one logical operation, and implementations own their
orchestration internally (locking discipline, dex/index maintenance, stats
touching). Two implementations exist:

- `*keg.LocalKeg` (`keg.go` + `keg_local_*.go`) orchestrates a `Repository`
  (a server-side repository implementation, or the test-only memory repository)
  and maintains derived state itself.
- `*keg.RemoteKeg` (`keg_remote.go`) speaks the hub's operation-level HTTP
  API — one request per operation; all orchestration happens server-side.

`pkg/tapper` resolves a keg via `keg.NewKegFromTarget`, which returns the
interface; `pkg/tapper` never touches `Repository` directly. All node
operations flow through the Keg interface via two parallel entry points:

```
CLI command   → pkg/cli (Cobra)    → pkg/tapper.Tap → keg.Keg → storage
MCP tool call → pkg/mcp (JSON-RPC) → pkg/tapper.Tap → keg.Keg → storage
```

where Tapper clients always use `RemoteKeg`. `LocalKeg` is what a hub server
runs over its own repository implementation.

Both paths converge at `pkg/tapper.Tap`, sharing the same method and `*Options`
struct for each feature. The CLI path uses `applyKegTargetProfile()` to resolve
Cobra flags into options and writes results to stdout. The MCP path uses
`resolveKegTarget()` with input structs annotated via `jsonschema` tags and
returns `CallToolResult` values. Server wiring in `NewServer()` calls 19
`register*Tools()` functions to expose the full Tap surface over stdio JSON-RPC.

**Repository** (`pkg/keg/repository.go`) is the server-side storage contract;
a Tapper client never implements it — `RemoteKeg` talks to the hub's operation
API instead. A hub supplies the production implementation and owns how it
stores anything. Tapper's concurrency-safe in-memory repository exists only in
`_test.go` for repository-independent `LocalKeg` tests.

**Dex** (`pkg/keg/dex.go`) is the in-memory index aggregator. It holds
NodeIndex, TagIndex, LinkIndex, BacklinkIndex, and ChangesIndex. Written as
deterministic TSV/markdown files under `dex/`.

**KegService** delegates selection to `ConfigService.ResolveTarget` using the
startup directory. KEG precedence is `--keg` → `TAP_KEG` → project `keg`
→ matching `kegMap.keg` → user `keg`. Hub precedence is `--hub` → `TAP_HUB` → project
`hub` → matching `kegMap.hub` → user `hub` → alphabetical configured Hub →
implicit Atlas (unless disabled). A missing KEG is an error. All namespace
references resolve within that one Hub.

## Storage Model

Tapper clients have no KEG storage layout. Every operation targets
`<hub-url>/api/v1/@<namespace>/kegs/<name>`, and the hub's REST contract is the
whole of what Tapper knows about it. How settings, nodes, metadata, indexes,
snapshots, and attachments are persisted behind that endpoint is the hub's
business, not Tapper's.

## Read Next

- [CLI And Command Flow](cli-and-command-flow.md)
- [Service Layer](service-layer.md)
- [Repository Layer](repository-layer.md)
- [Configuration Resolution](configuration-resolution.md)
- [Runtime And Concurrency](runtime-and-concurrency.md)
- [Testing Architecture](testing-architecture.md)
