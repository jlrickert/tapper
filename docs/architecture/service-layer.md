# Service Layer

The service layer lives in `pkg/tapper` and is accessed through the `Tap`
client.

## Tap Client

`pkg/tapper/tap.go` defines `Tap` as the high-level coordinator:

- `PathService`
- `ConfigService`
- `KegService`

`NewTap` constructs these once and injects shared runtime dependencies.

## PathService

`pkg/tapper/path_service.go` wraps app path resolution from
`cli-toolkit/appctx` and exposes common paths:

- user config path
- project config path
- project config root

This keeps path derivation in one place instead of spreading path logic across
commands.

## Configuration and KEG services

`ConfigService` walks project files from the startup directory to the filesystem
root, merges user/project/environment layers, and selects one paired mapping.
`KegService` delegates to `ResolveTarget`, so operations share the same KEG and
Hub selection as discovery, authentication, Flight loading, and inspection.

KEG precedence is explicit flag, environment, mapping, project, then user.
Hub precedence is explicit flag, environment, project, mapping, user, then
alphabetical configured Hub or implicit Atlas. See
[Resolution Order](../configuration/resolution-order.md).

Only user configuration can define Hub connections and credentials. Malformed
YAML fails. Unused Hub and mapping entries are validated when selected.

MCP pins the canonical Hub URL while reloading Flight authority and credentials.
Remote discovery stays on that Hub; saved connection listings remain local.
Foreign direct targets and event streams are refused before dispatch, and
redirects cannot forward credentials or replay mutations elsewhere.

## Operation aggregation

`keg.Keg` is the command-operation boundary, not a repository primitive
surface. `LocalKeg` owns same-keg orchestration and `RemoteKeg` maps each
aggregate method to exactly one authenticated Hub request. Listing, batch
reads, related links, info/doctor, bulk removal/validation,
editor open/save, schema creation, dex reads, create, and
lock acquisition therefore have matching local and hosted semantics without
client fan-out.

The Tap layer groups mixed-keg read and validation arguments by resolved keg,
issues one batch per group, and restores caller order. Interactive editing and
watching remain separate phases. Cross-keg import uses one source export, one
target import, and, when requested, one atomic source-stub update batch.

## Mutation preconditions

Protected mutations enforce optimistic concurrency at the `keg.Keg` boundary,
so CLI, hosted-MCP, and browser callers share one rule. Node content
and metadata use the node state hash; schemas and keg settings hash their stored
YAML documents; flights hash their stored manifests. MCP makes these hashes
required in every corresponding mutation schema. Batch node edits, metadata
updates, and removals carry one token per item and preflight every token before
performing any mutation. A conflict returns the current hash and recovery
content when practical, with `operationPerformed=false`, so callers can merge
or refetch and retry without guessing whether the write landed.

Every `Repository` supplies a reentrant keg operation boundary. A read
boundary gives aggregate readers one coherent snapshot; a write boundary
contains the complete canonical-plus-derived mutation, including the dex
reload/mutate/persist cycle. A write may nest reads or writes, a read may nest
reads, and a read-to-write upgrade is rejected. The boundary is acquired
before node locks, whose order must remain deterministic.

A production repository is expected to honor that boundary transactionally:
aggregate reads see one coherent snapshot, and a write serializes against
other writes to the same keg before node locks are taken. Different kegs
remain independently writable; optimistic dex generations/CAS retries are a
possible future throughput optimization. How a hub implements this is its own
concern. The only repository in this tree is the concurrency-safe internal
test helper used for repository-independent `LocalKeg` orchestration tests.

## FlightService and flight gating

`pkg/tapper/flight.go` discovers flights for the active hub. A flight carries
cover caps for MCP sessions plus a block of agent instructions. After a keg
is resolved, `Tap.enforceFlight` rejects MCP access to a keg that falls
outside the flight's cover or tries to write through a `viewer` cap. Direct CLI
commands set `KegTargetOptions.BypassFlightRestrictions`, so they keep normal
keg authorization while preserving `Flight` for orient/instruction rendering.
An empty cover denies every KEG. MCP sessions pin either no-flight identity
authority or one real root at initialization. No-flight calls may use any
identity-accessible real flight explicitly; real-root calls may use that root
or one of its currently accessible transitive descendants. `orient` is a
read-only view and `session_refresh` only activates a repaired, explicitly
configured root. Config and preference changes cannot replace active authority
within a connection. KEG selectors remain independent operation defaults. See
[Flights](../configuration/flights.md).
