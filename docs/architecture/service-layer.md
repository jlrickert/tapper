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

## ConfigService

`pkg/tapper/config_service.go` provides stateful config APIs:

- read user config
- walk and merge project config
- merge effective config via a `cfgcascade.Cascade`
- cache user/project/merged configs
- resolve keg references to concrete keg targets

Notable behavior:

1. `ProjectConfig` (`WalkConfigsUp`) walks from the workspace root up to the
   filesystem root, collecting **every** `.tapper/config.yaml`, and merges them
   so a deeper directory overrides a shallower one.
2. The walked project layers, the user config, and `TAP_*` env vars are then
   resolved by `cfgcascade.Cascade[*Config]` (user = base, project = overlay,
   env = top).
3. **Trust boundary:** `stripUntrustedFields` removes `hubs{}` and
   `token`/`tokenEnv` from any walked project config (user config only). Each
   strip becomes a `ConfigLoadWarning` surfaced by `Config()`; `--strict`
   escalates warnings to errors.
4. Reference resolution (`Config.ResolveRef`) parses the keg
   selector into a reference (`parseKegRef`) and applies the hub and namespace
   default/fallback chains plus the per-hub-kind backend mapping (local →
   `<basePath>/@<ns>/<name>`; remote/readonly → `<hub-url>/api/v1/@<ns>/kegs/<name>`).

## KegService

`pkg/tapper/keg_service.go` resolves and caches active keg handles.

Resolution modes on the full `tap` surface:

1. explicit `--keg`, optionally refined by `--namespace` or `--hub`
2. implicit resolution from config and cwd

Project-local resolution still exists for the pruned `keg` profile and for
explicit local creation destinations.

Default implicit order:

1. `defaultKeg`
2. `kegMap` lookup
3. `fallbackKeg`

Bare names are references, not entries in a `kegs` alias table. They resolve
through the namespace-centric chain.

## Operation aggregation

`keg.Keg` is the command-operation boundary, not a repository primitive
surface. `LocalKeg` owns same-keg orchestration and `RemoteKeg` maps each
aggregate method to exactly one authenticated Hub request. Listing, batch
reads, related links, graph/inspection/doctor, bulk removal/validation,
editor open/save, redirect creation, schema creation, dex reads, create, and
lock acquisition therefore have matching local and hosted semantics without
client fan-out.

The Tap layer groups mixed-keg read and validation arguments by resolved keg,
issues one batch per group, and restores caller order. Interactive editing and
watching remain separate phases. Cross-keg import uses one source export, one
target import, and, when requested, one source redirect batch.

## FlightService and flight gating

`pkg/tapper/flight.go` discovers flights for the active hub. A flight carries
cover caps for MCP sessions plus a block of agent instructions. After a keg
is resolved, `Tap.enforceFlight` rejects MCP access to a keg that falls
outside the flight's cover or tries to write through a `viewer` cap. Direct CLI
commands set `KegTargetOptions.BypassFlightRestrictions`, so they keep normal
keg authorization while preserving `Flight` for orient/instruction rendering.
An empty cover denies every KEG. Local MCP connections pin a flight snapshot
and invalidate it when the normalized manifest hash changes.
`--flight` composes with the single-keg selectors rather than replacing them. See
[Flights](../configuration/flights.md).
