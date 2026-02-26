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
`cli-toolkit/apppaths` and exposes common paths:

- user config path
- project config path
- project config root

This keeps path derivation in one place instead of spreading path logic across
commands.

## ConfigService

`pkg/tapper/config_service.go` provides stateful config APIs:

- read user config
- read project config
- merge effective config
- cache user/project/merged configs
- resolve aliases and discovered kegs

Notable behavior:

1. Alias resolution checks explicit `kegs:` first.
2. Discovered aliases come from `kegSearchPaths`.
3. Later `kegSearchPaths` entries override earlier entries on collisions.

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
