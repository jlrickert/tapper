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
- `pkg/tapper/invocation_telemetry.go` (flush ticker and request/shutdown
  deadlines): coalesces best-effort network uploads and bounds their wall-clock
  impact independently of the frozen domain clock.

## Concurrency Model

- **Per-node locking**: `Repository.WithNodeLock(ctx, id, fn)` serializes
  operations on a single node. A hub enforces this server-side; the
  concurrency-safe in-memory implementation exists only in tests.
- **Lock context propagation**: `contextWithNodeLock`/`contextHasNodeLock` allow
  re-entrant locking within the same call chain.
- **Dex mutex**: `Dex.mu sync.RWMutex` guards index data; `LocalKeg.dexMu`
  guards lazy initialization.
- **Node allocation**: Production allocation is a hub operation;
  repository-independent orchestration tests use the internal in-memory
  repository.
- **KegService cache**: `cacheMu sync.Mutex` guards the shared keg resolution
  cache.
- **Remote operations are single-request**: each `RemoteKeg` method is one
  HTTP round trip, and the hub serializes per-node writes server-side. There
  is no client-side lock lease or dex write over HTTP.
- **Advisory locks are session primitives**: `Keg.Lock`/`Unlock`/
  `LockStatus`/`ForceUnlock` (used by `tap lock` / `tap edit`) are opt-in
  advisory locks backed by the Hub's `/nodes/{id}/lock` endpoints. Leases carry a TTL
  (`DefaultLockTTL`, 5 minutes) with **no renewal**: a session that outlives
  the TTL loses the lock.

## Read Next

- [Repository Layer](repository-layer.md)
- [Testing Architecture](testing-architecture.md)
- [Development](../development/README.md)
