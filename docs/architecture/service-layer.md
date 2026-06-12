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
- resolve aliases to concrete keg targets

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
4. Reference resolution (`Config.ResolveAlias` → `ResolveRef`) parses the keg
   selector into a reference (`parseKegRef`) and applies the hub and namespace
   default/fallback chains plus the per-hub-kind backend mapping (local →
   `<basePath>/@<ns>/<name>`; remote/readonly → `<hub-url>/api/v1/@<ns>/kegs/<name>`).

## KegService

`pkg/tapper/keg_service.go` resolves and caches active keg handles.

Resolution modes:

1. explicit `--path`
2. project mode (`--project`, optionally `--cwd`)
3. explicit `--keg` alias
4. implicit resolution from config and cwd

Default implicit order:

1. `defaultKeg`
2. `kegMap` lookup
3. `fallbackKeg`

Project-local fallback for aliases is supported at `<project>/kegs/<alias>`
when an alias is not explicitly configured.

## FlightService and flight gating

`pkg/tapper/flight.go` discovers flights for the active hub. A flight is an
optional restriction on which kegs are available plus a block of agent
instructions. After a keg is resolved, an active `--flight` gates the result:
`Tap.enforceFlight` rejects a keg that falls outside the flight's cover or tries
to write through a `viewer` cap. Local instructions-only flights with an empty
cover restrict nothing. `--flight` composes with the single-keg selectors rather
than replacing them. See
[Flights](../configuration/flights.md).
