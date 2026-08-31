# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with
code in this repository.

## Coordinated immutable-flight delivery gate

The immutable-flight direct-subflight work is a coordinated change with the sibling
Tapper Hub repository. Until the user explicitly approves delivery after joint
verification:

- Do not merge related Tapper or Tapper Hub changes.
- Do not create or push release tags, GitHub releases, release commits, or
  trigger release workflows.
- Do not permanently update Tapper Hub's Tapper dependency pin.
- Use Tapper Hub's local `go.work` link for cross-repository integration
  testing.
- Stop at reviewable local branches/commits, test evidence, and a coordination
  report. Create or update PRs only when the user requests it.

These restrictions remain in force even when tests pass or either repository
appears independently ready to ship.

## Overview

**tapper** is a Go CLI toolset for managing KEGs (Knowledge Exchange Graphs). A
KEG is a repository of numbered nodes, each containing README.md (content),
meta.yaml (metadata), and stats.json (programmatic stats). The system supports
indexing, tagging, linking between nodes, and snapshot-based revision history.

The `tap` CLI provides the full multi-KEG surface with user/project config
resolution.

## Build & Development Commands

```bash
# Build
go build ./cmd/tap

# Test
go test ./...                              # all tests
go test ./pkg/keg/... -v                   # single package, verbose
go test ./pkg/keg -run TestConcurrentCreate # single test by name
go test -race ./pkg/keg/...               # with race detector

# Install (requires go-task)
task install-tap                           # install tap
task test                                  # cached test run of ./pkg/...

# Lint
go vet ./...
task lint:docs                            # exported interface documentation
```

### Stable interface documentation

Every exported interface declared under `pkg/...` and every exported method it
declares must have an adjacent Go doc comment beginning with the declared
identifier. Deprecated methods still require their contract documentation and
a standard `Deprecated:` paragraph. `task lint:docs` enforces this rule without
a baseline or suppression list and runs explicitly in CI.

## Branching Model

- `main` is the GitHub default branch and the de-facto working branch.
- `dev` exists and is preserved as a dev/main split for embedded-version
  coherence in the Claude plugin, but the supporting rulesets ("Protect main",
  "Force push protection") are currently disabled on this repo, so direct
  pushes and direct-to-main PRs are not blocked.
- The release pipeline is a single workflow: `release.yml` runs on
  `workflow_dispatch` against `main`, writes the changelog commit and tag,
  then runs goreleaser inline.
- Commit messages should follow Conventional Commits (for example `feat:`,
  `fix:`, `docs:`), with summaries no longer than 72 characters.
- Never use Conventional Commits breaking-change syntax (`type!:` or a
  `BREAKING CHANGE:` footer). Tapper stays on `v0.x` until the user explicitly
  authorizes a stable `v1` release. Incompatible changes during `v0.x` use an
  ordinary `feat:` or `refactor:` commit and may produce a minor `v0.x` bump.
- Automatic release versioning must never cross from `v0.x` to `v1.0.0`. A
  stable `v1` release requires explicit user direction and an explicit
  `version` workflow override; a code change or commit message is not release
  authorization.
- When opening a PR, base on `main` unless explicitly asked to route through
  `dev`. If `dev` is reactivated (rulesets re-enabled, default branch
  switched back), revisit this section.

## Architecture

### Package Map

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
  `docs/ai-coding-agents/mcp-setup.md`.

### Key Types and Flow

**Keg** (`pkg/keg/keg_iface.go`) is the single-keg business **interface**:
every method is one logical operation, and implementations own their
orchestration internally (locking discipline, dex/index maintenance, stats
touching). Two implementations exist:

- `*keg.LocalKeg` (`keg.go` + `keg_local_*.go`) orchestrates a `Repository`
  (the Hub's server-side `PgRepo`, or the test-only memory repository) and maintains
  derived state itself.
- `*keg.RemoteKeg` (`keg_remote.go`) speaks tapper-hub's operation-level HTTP
  API — one request per operation; all orchestration happens server-side.

`pkg/tapper` resolves a keg via `keg.NewKegFromTarget`, which returns the
interface; `pkg/tapper` never touches `Repository` directly. All node
operations flow through the Keg interface via two parallel entry points:

```
CLI command   → pkg/cli (Cobra)    → pkg/tapper.Tap → keg.Keg → storage
MCP tool call → pkg/mcp (JSON-RPC) → pkg/tapper.Tap → keg.Keg → storage
```

where Tapper clients always use `RemoteKeg`; Tapper Hub uses `LocalKeg` over
its PostgreSQL repository.

Both paths converge at `pkg/tapper.Tap`, sharing the same method and `*Options`
struct for each feature. The CLI path uses `applyKegTargetProfile()` to resolve
Cobra flags into options and writes results to stdout. The MCP path uses
`resolveKegTarget()` with input structs annotated via `jsonschema` tags and
returns `CallToolResult` values. Server wiring in `NewServer()` calls 19
`register*Tools()` functions to expose the full Tap surface over stdio JSON-RPC.

**Repository** (`pkg/keg/repository.go`) is the Hub-side storage contract —
`RemoteKeg` talks to the Hub's operation API instead. Tapper Hub provides the
production PostgreSQL `PgRepo`; Tapper's concurrency-safe in-memory repository
exists only in `_test.go` for repository-independent `LocalKeg` tests.

**Dex** (`pkg/keg/dex.go`) is the in-memory index aggregator. It holds
NodeIndex, TagIndex, LinkIndex, BacklinkIndex, and ChangesIndex. Written as
deterministic TSV/markdown files under `dex/`.

**KegService** (`pkg/tapper/keg_service.go`) resolves which keg to use via
config precedence: explicit `--keg` reference → `defaultKeg` → `kegMap` path
match → `fallbackKeg`, each a keg reference resolved through `ResolveRef`. The
`default*` slots are authoritative (project config sets them) and win over a
`kegMap` path rule; `fallback*` is the global-user last resort that `tap
bootstrap` writes, so anything more specific overrides it. A bare name with no
resolvable remote namespace and Hub is an error. Active
flight cover caps are enforced for the MCP surface; direct CLI commands keep
normal keg authorization and preserve `--flight` only as context for orient and
MCP defaults (`Tap.resolveKeg`).

### Storage Model

Tapper clients have no KEG storage layout. Every operation targets
`<hub-url>/api/v1/@<namespace>/kegs/<name>`. Tapper Hub persists settings,
nodes, metadata, indexes, snapshots, and attachments in PostgreSQL through
`PgRepo`; server-side `LocalKeg` owns orchestration.

### Config Hierarchy

Tapper config is resolved via a cascade (most specific wins):

| Rank | Source                          | Discovery                                          |
| ---- | ------------------------------- | -------------------------------------------------- |
| top  | CLI flags (`--log-level`, etc.) | Cobra `cmd.Flags().Changed()`                      |
| ↑    | Env vars (`TAP_*`)              | `rt.Env().Get()` prefix scan                       |
| ↑    | Project configs (deepest→…)     | every `.tapper/config.yaml` from cwd up to `/`     |
| base | User config                     | `~/.config/tapper/config.yaml`                     |
| —    | Defaults                        | Hardcoded in code                                  |

`ProjectConfig` walks from the workspace root up to the filesystem root
(`WalkConfigsUp`), collecting every `.tapper/config.yaml`, and merges them so a
deeper directory overrides a shallower one. **Trust boundary:** only the user
config may define `hubs{}` and `token`/`tokenEnv`; those fields are stripped
from any walked project config (recorded as a load warning; `--strict` makes it
a hard error). The merged project layer, the user config, and env vars are then
resolved by `cfgcascade.Cascade[*Config]` in `ConfigService.Config()`.

**Hub / namespace resolution** (`Config.ResolveRef`) is namespace-centric:
**keg name → namespace → Hub**. There is no `kegs` alias map — a keg
selector (`defaultKeg`, `fallbackKeg`, `--keg`, a `kegMap` alias) is parsed as a
keg reference by `parseKegRef` and resolved directly. The namespace resolves
first (explicit → `defaultNamespace` → `fallbackNamespace` → per-Hub default /
error), then the Hub is resolved *from* the namespace (explicit →
`namespaces[ns].hub` → `defaultHub` → `fallbackHub` →
sole/alpha hub → compiled-in `atlas`). The `namespaces` map disambiguates
namespace→Hub. `@local` has no special meaning.

A keg reference renders as the `keg` scheme — `keg:@<namespace>/<name>` (and
`keg:@<namespace>/<name>/<nodeID>` for a node). The hub is resolution metadata,
never part of the reference string; there is no `<hub>:@ns/name` form. To pin a
hub explicitly, set `defaultHub`/`namespaces[ns].hub` so the namespace routes to
that hub.

To **list** available kegs, query a hub: `tap keg list` / the `keg_list` MCP
tool (backed by `GET /api/v1/kegs`).
(`tap hub list` lists configured *hub connections*, not kegs.)

**`tap keg create`** is namespace-centric too: a bare `tap keg create <name>`
resolves the default namespace and Hub, then creates via
`POST /api/v1/@<ns>/kegs` (failing on 409). When nothing is configured, the full
`tap` surface refuses with a "run `tap bootstrap`" error (`ErrNotBootstrapped`)
rather than silently creating local state. `tap init` and the local creation
flags are removed.

**Command groups.** `tap keg` administers kegs on a hub (`list`, `create`,
`grants`/`grant`/`revoke` for ACLs, `visibility`, `rename`, and `settings` for
the keg's own config — formerly `tap settings`). `tap namespace` administers namespaces
and membership roles (`list`, `members`, `add-member`, `set-role`,
`remove-member`, `create`). `tap hub` manages hub *connections* (`list`,
`status`, `add`/`remove` writing user config, `set-default` writing project
config by default, `--user` for user). `tap config edit` defaults to the **project**
config; `--user` targets the user config.

**Keg selection is flag-driven, not positional.** The keg an admin command
operates on comes from the global resolution flags — `--keg` (a bare name or
`@namespace/keg`), with `--namespace`/`--hub` as component
overrides — not a positional. A bare invocation (no `--keg`) targets the
resolved KEG. The on-disk discovery selectors `--project`/`--cwd` are gone;
`--path` is not a KEG target selector.

**`tap use`** records resolution in config: `tap use @ns/keg` sets the project's
`defaultKeg` (in `.tapper/config.yaml`); `tap use @ns/keg --user` sets the
user-wide `fallbackKeg`; `--flight @ns/+slug` sets the project's persisted
flight; bare `tap use` prints the resolved keg/flight/fallback and the scope
that set each. A persisted `flight` auto-applies when `--flight` is omitted.

Supported env vars: `TAP_DEFAULT_KEG`, `TAP_FALLBACK_KEG`, `TAP_FLIGHT`,
`TAP_AGENT`, `TAP_LOG_FILE`, `TAP_LOG_LEVEL`, `TAP_DEFAULT_HUB`, `TAP_FALLBACK_HUB`,
`TAP_DEFAULT_NAMESPACE`, `TAP_FALLBACK_NAMESPACE`, `TAP_DISABLE_ATLAS_HUB`,
`TAP_DISABLE_TELEMETRY` (`1`/`true`/`yes`/`on` for
the disable flags).

Use `tap config --explain FIELD` to see which source set a value, or
`tap config --show-sources` for all fields. The `--strict` flag makes config
load warnings (corrupt YAML) into hard errors.

KEG settings are separate from Tapper user/project config — different schema,
different purpose (KEG metadata vs resolver settings) — and are read or written
through the Hub.

### Dependency: cli-toolkit

The `github.com/jlrickert/cli-toolkit` module (local at `../cli-toolkit`)
provides `toolkit.Runtime` — the explicit dependency container carrying
filesystem, env, clock, logger, hasher, stream, and process identity. All I/O in
tapper flows through Runtime, enabling sandboxed test environments.

### Runtime Abstraction Rule

All I/O in `pkg/keg`, `pkg/tapper`, `pkg/cli`, and `pkg/integrations` must go
through `toolkit.Runtime`. Direct stdlib calls bypass the sandboxed test
environment and break test isolation. Specifically:

- **File I/O**: Use `rt.ReadFile` / `rt.WriteFile` — never `os.ReadFile` /
  `os.WriteFile`.
- **Streams**: Use `rt.Stream().Out` / `rt.Stream().Err` — never `os.Stdout` /
  `os.Stderr` directly.
- **Clock**: Use `rt.Clock().Now()` — never `time.Now()`.
- **Commands**: Use `exec.CommandContext(ctx, ...)` — never bare
  `exec.Command(...)`.
- **Log files use `rt.OpenFile`**: Since cli-toolkit v1.3.0, log file
  initialization goes through `Runtime.OpenFile` instead of `os.OpenFile`. This
  enables sandbox-based log file tests.

The `cli-toolkit` `clock.Clock` interface only exposes `Now()`; it does not
provide `After`, `NewTicker`, `AfterFunc`, or similar scheduling primitives.
The following call sites use the standard `time` package directly because each
one is either (a) coalescing real filesystem or
network events whose timing is wall-clock by definition, or (b) a non-time
use of `time.Now()` that cannot be driven by a fake clock:

- `pkg/keg/repo_fs_events.go` (watcher debounce ticker and coalescence
  window): filesystem event delivery is wall-clock; the debounce window must
  measure real elapsed time between bursts.
- `pkg/tapper/editor_live.go` (live-save ticker, `pendingFrom` timestamp, and
  120ms debounce check): debounces real fsnotify events emitted by the user's
  editor subprocess. The 100ms cadence and 120ms settle window are load-bearing
  for editor write-rename cycles and exercised by the existing live-save test,
  which runs an actual shell subprocess.
- `pkg/tapper/tap_edit.go` (500ms settle delay in `reverseSync` via
  `time.After`): a save writes meta and content as separate network requests,
  so the live event for the first write can observe repository state where
  the second hasn't landed yet. The settle window spans real round-trips to
  the hub before deciding a change is genuinely external.
- `pkg/keg/keg_remote_events.go` (websocket reconnect backoff timer): the
  live watch retries real network dials against the hub, so the backoff must
  measure wall-clock time regardless of the local test clock.
- `pkg/keg/node_id.go` (crypto/rand fallback uses `time.Now().UnixNano()`):
  used as an entropy source for a short random code when `crypto/rand` fails,
  not as a time measurement.
- `pkg/tapper/invocation_telemetry.go` (flush ticker and request/shutdown
  deadlines): coalesces best-effort network uploads and bounds their wall-clock
  impact independently of the frozen domain clock.

### Concurrency Model

- **Per-node locking**: `Repository.WithNodeLock(ctx, id, fn)` serializes
  operations on a single node. Production `PgRepo` enforces this at the Hub;
  the concurrency-safe in-memory implementation exists only in tests.
- **Lock context propagation**: `contextWithNodeLock`/`contextHasNodeLock` allow
  re-entrant locking within the same call chain.
- **Dex mutex**: `Dex.mu sync.RWMutex` guards index data; `LocalKeg.dexMu`
  guards lazy initialization.
- **Node allocation**: Production allocation is a Hub operation backed by
  PostgreSQL; repository-independent orchestration tests use the internal
  in-memory repository.
- **KegService cache**: `cacheMu sync.Mutex` guards the shared keg resolution
  cache.
- **Remote operations are single-request**: each `RemoteKeg` method is one
  HTTP round trip, and the hub serializes per-node writes server-side
  (`pg_advisory_xact_lock`). There is no client-side lock lease or dex write
  over HTTP.
- **Advisory locks are session primitives**: `Keg.Lock`/`Unlock`/
  `LockStatus`/`ForceUnlock` (used by `tap lock` / `tap edit`) are opt-in
  advisory locks backed by the Hub's `/nodes/{id}/lock` endpoints. Leases carry a TTL
  (`DefaultLockTTL`, 5 minutes) with **no renewal**: a session that outlives
  the TTL loses the lock.

## Testing

- **Sandbox pattern**: Tests use `sandbox.NewSandbox(t, ...)` from cli-toolkit,
  which creates a jailed temp directory with a test runtime (mock clock, MD5
  hasher, test logger).
- **Fixtures**: `pkg/keg/data/` contains `empty`, `example`, `home` fixtures.
  `pkg/tapper/data/` contains `basic`, `example`, `keep`.
- **Repository fixtures**: repository-independent behavior tests use
  `internal/testkegrepo`, which is imported only by `_test.go` files. SQL,
  transaction, restart, and namespace-isolation behavior stays in Tapper Hub's
  PostgreSQL integration suite.
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
- `schemas/keg-settings.json` — keg settings schema

These schemas are referenced by editors for validation and completion hints. A
config field added without a schema update will lack editor support and
validation.

## Gotchas

- A keg must be initialized (`keg.Init(ctx)`) before Create/SetContent/etc. Init
  writes the config file and zero node.
- The Dex is lazily loaded and cached; direct `k.dex` assignment is guarded by
  `k.dexMu`.
- Node content (README.md), meta (meta.yaml), and stats (stats.json) are
  separate reads **at the Repository layer**. At the business layer,
  `Keg.ReadNode` returns the full node state (content, raw meta, stats, asset
  lists) in one operation — a single round trip on RemoteKeg.
- The keg settings file is named `keg` (no extension), though `keg.yaml` and
  `keg.yml` are also accepted.
- Node IDs are allocated by the Hub. `GET /nodes/next` is only a read-only
  probe; creation uses `POST /nodes` with complete content.
- **Cobra skips PersistentPostRunE when RunE returns an error.** Any cleanup
  or logging that must run on both success and failure paths cannot rely on
  PersistentPostRunE. In tapper, invocation logging and log file cleanup are
  performed in `RunWithProfile` after `ExecuteContext` returns, bypassing this
  Cobra limitation.
- **`duration_ms` in invocation logs includes editor wait time.** For `tap edit`
  and `tap cat` (which open an editor on TTY), the logged duration includes the
  time the user spends in the editor. This is a known limitation of the
  invocation logging system, not a bug. The `interactive` field in the log entry
  can help distinguish interactive from non-interactive invocations.
