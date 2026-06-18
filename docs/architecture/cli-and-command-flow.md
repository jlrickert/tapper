# CLI And Command Flow

This page describes how `tap` executes a command from process start to service
call, and how optional secondary binaries such as `keg` reuse the same
machinery with a different profile.

## Entrypoints

- `cmd/tap/tap.go` calls `cli.Run(ctx, rt, os.Args[1:])`
- `cmd/keg/keg.go` calls `cli.RunWithProfile(..., cli.KegProfile())`

`tap` is the primary binary. `keg` is a secondary binary that demonstrates
how the same command framework can be pruned through profile-based behavior.

## Run Wrapper

`pkg/cli/cli.go` is the thin runtime wrapper:

1. Validate or construct the runtime.
2. Apply shorthand behavior for numeric first args (`tap 10` -> `tap cat 10`).
3. Build a shared `Deps` object.
4. Build and execute the root Cobra command.

## Root Command Initialization

`pkg/cli/cmd_root.go` wires common lifecycle logic:

1. Root persistent flags register shared KEG targeting options for `tap`
   (`--keg`, `--namespace`, `--hub`, `--flight`, and `--config`).
2. `PersistentPreRunE` resolves working directory from runtime.
3. Creates `deps.Tap` with `tapper.NewTap(...)`.
4. Registers root-level keg completion after `deps.Tap` exists.
5. Applies optional logger settings.
6. Attaches all subcommands.

Every subcommand receives the same `*Deps`, so command handlers do not
reconstruct core services.

## Command Pattern

Most commands follow this shape:

1. Bind command-specific flags into a typed options struct.
2. Merge root KEG target defaults and apply profile-specific behavior. `tap`
   uses the full namespace/hub-aware profile. `keg` uses a pruned project-local
   profile.
3. Call a single method on `deps.Tap`.
4. Write returned output to stdout.

Example command files:

- `pkg/cli/cmd_cat.go`
- `pkg/cli/cmd_info.go`
- `pkg/cli/cmd_repo_config.go`

## Profile Differences

Profiles are defined in `pkg/cli/profile.go`.

- `TapProfile` enables the full command surface and namespace/hub targeting.
- `KegProfile` forces project-style resolution and disables configuration
  command surfaces that do not fit the narrower workflow.
- Snapshot/archive commands (`snapshot`, `archive import`, `archive export`)
  are shared by both profiles. The main difference is target resolution:
  `keg` resolves against the active project by default, while `tap` resolves
  through `@namespace/keg` references, config defaults, and hub routing.

## Why The Profile Technique Matters

The command tree is defined once in `pkg/cli/cmd_root.go` and then filtered by
the selected `Profile`.

That gives you:

- one implementation path for shared commands
- one service graph (`deps.Tap`) regardless of binary name
- the ability to publish a narrower binary without forking command logic

In practice, `tap` stays the canonical interface and smaller binaries can be
added later when a focused workflow benefits from a reduced surface area.
