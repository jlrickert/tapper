# Runtime And Concurrency

Two hard constraints on any code change in this repository: all I/O goes
through `toolkit.Runtime`, and concurrent access follows the locking model
below. The first is lint-enforced; the second is not, so read it before
touching locking, indexing, or remote operations.

## Dependency: cli-toolkit

The `github.com/jlrickert/cli-toolkit` module (local at `../cli-toolkit`)
provides `toolkit.Runtime` — the explicit dependency container carrying
filesystem, env, clock, logger, hasher, stream, and process identity. All I/O in
tapper flows through Runtime, enabling sandboxed test environments.

## Runtime Abstraction Rule

All I/O in `pkg/keg`, `pkg/tapper`, `pkg/cli`, and `pkg/integrations` must go
through `toolkit.Runtime`. Direct stdlib calls bypass the sandboxed test
environment and break test isolation. Specifically:

- **File I/O**: Use `rt.ReadFile` / `rt.WriteFile` — never `os.ReadFile` /
  `os.WriteFile`.
- **Directories and metadata**: Use `rt.Mkdir` / `rt.Remove` / `rt.Rename` /
  `rt.Stat` / `rt.ReadDir` / `rt.Glob` — never `os.MkdirAll` / `os.RemoveAll` /
  `os.Rename` / `os.Stat` / `os.ReadDir` / `os.DirFS`. The Runtime confines
  paths to a jail; a direct `os` call escapes it.
- **Streams**: Use `rt.Stream().Out` / `rt.Stream().Err` — never `os.Stdout` /
  `os.Stderr` directly.
- **Clock**: Use `rt.Clock().Now()` — never `time.Now()`.
- **Commands**: Use `exec.CommandContext(ctx, ...)` — never bare
  `exec.Command(...)`.
- **Log files use `rt.OpenFile`**: Since cli-toolkit v1.3.0, log file
  initialization goes through `Runtime.OpenFile` instead of `os.OpenFile`. This
  enables sandbox-based log file tests.

`internal/fsdiscipline` enforces the filesystem half of this rule: it parses
every non-test file and fails on a direct call to an `os` filesystem function.
It matches call expressions rather than text, so the `os.ErrNotExist` sentinels,
`os.IsNotExist`, and the `os.O_CREATE` flags passed *into* `rt.OpenFile` remain
allowed. Run it with `task lint:fs`; CI runs it on its own line. One file is
exempt:

- `cmd/render-integrations/main.go` (`os.DirFS`): build-time codegen, invoked by
  `task render-integrations` and the pre-commit hook before any Runtime exists.

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
- `pkg/tapper/invocation_telemetry.go` (flush ticker, request deadline, and
  the in-flight wait at `Close`): coalesces best-effort network uploads and
  bounds their wall-clock impact independently of the frozen domain clock.
  `Close` never starts a send; it waits at most
  `InvocationTelemetryInFlightWait` for one already running and spools the
  rest through `rt.AtomicWriteFile`, into a new file in the spool directory
  for its destination (`telemetry-spool/<key>/`, keyed by Hub URL and login),
  so events are never sent to another Hub or account. Concurrent processes
  never share a file: each writes its own, and a starting process claims a
  file with `rt.Rename` before reading it, so each spooled event is sent by
  exactly one process.

## Concurrency Model

- **Per-node locking**: `Repository.WithNodeLock(ctx, id, fn)` serializes
  operations on a single node. A hub enforces this server-side; the
  concurrency-safe in-memory implementation exists only in tests.
- **Lock context propagation**: `contextWithNodeLock`/`contextHasNodeLock` allow
  re-entrant locking within the same call chain.
- **Bounded keg lock wait**: `keg.WithLockWait(ctx, d)` asks the repository to
  give up acquiring the keg write lock after `d` with `ErrLockTimeout`, and
  `keg.LockWaitFrom(ctx)` reads it back; zero means wait as long as it takes.
  A touched `ReadNodes` or `OpenNode` waits about two seconds. When the lock
  stays busy the touch is skipped and the nodes are read untouched, so a read
  never queues behind a long writer just to record an access.
- **Dex mutex**: `Dex.mu sync.RWMutex` guards index data; `LocalKeg.dexMu`
  guards lazy initialization.
- **Node allocation**: Production allocation is a hub operation;
  repository-independent orchestration tests use the internal in-memory
  repository.
- **KegService cache**: `cacheMu sync.Mutex` guards the shared keg resolution
  cache.
- **Auth store**: `AuthStore.mu sync.RWMutex` guards the in-memory credential
  map, because the token resolver reads it from concurrent hub requests while
  a refresh writes the rotated entry.
- **Remote operations are single-request**: each `RemoteKeg` method is one
  HTTP round trip, and the hub serializes per-node writes server-side. There
  is no client-side lock lease or dex write over HTTP.
- **Orientation reuse in `tap mcp`**: each tool call needs the session's
  resolved orientation, which costs several Hub requests to build. `tap mcp`
  reuses it for up to ten seconds (`ServerOptions.OrientationCacheTTL`);
  `orient`, `session_refresh`, flight mutations, and any failed call discard
  it. Reuse cannot widen access, because the Hub rechecks the orientation
  header and KEG authority on every request. Hosted servers leave it off.
- **Orientation requests overlap**: `tap info`, `tap orient`, and the MCP
  orientation provider issue the Hub requests that do not depend on each other
  (identity, flight, flight graph, KEG catalog) from separate goroutines and
  read the results in the order the sequential code checked them, so errors
  and warnings are reported exactly as before. `tap info` asks for all of it in
  one `GET /api/v1/context` where the Hub supports it (see
  [REST API contract compatibility](../api-compatibility.md#caller-context-route)).
- **Advisory locks are session primitives**: `Keg.Lock`/`Unlock`/
  `LockStatus`/`ForceUnlock` (used by `tap lock` / `tap node edit`) are opt-in
  advisory locks backed by the Hub's `/nodes/{id}/lock` endpoints. Leases carry a TTL
  (`DefaultLockTTL`, 5 minutes) with **no renewal**: a session that outlives
  the TTL loses the lock.

## Read Next

- [Repository Layer](repository-layer.md)
- [Testing Architecture](testing-architecture.md)
- [Development](../development/README.md)
