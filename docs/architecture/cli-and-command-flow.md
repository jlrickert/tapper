# CLI And Command Flow

This page describes how `tap` executes a command from process start to service
call, and how optional secondary binaries such as `kegv2` reuse the same
machinery with a different profile.

## Entrypoints

- `cmd/tap/tap.go` calls `cli.Run(ctx, rt, os.Args[1:])`
- `cmd/kegv2/keg.go` calls `cli.RunWithProfile(..., cli.KegV2Profile())`

`tap` is the primary binary. `kegv2` is a secondary binary that demonstrates
how the same command framework can be pruned through profile-based behavior.

## Run Wrapper

`pkg/cli/cli.go` is the thin runtime wrapper:

1. Validate or construct the runtime.
2. Apply shorthand behavior for numeric first args (`tap 10` -> `tap cat 10`).
3. Build a shared `Deps` object.
4. Build and execute the root Cobra command.

## Root Command Initialization

`pkg/cli/cmd_root.go` wires common lifecycle logic:

1. `PersistentPreRunE` resolves working directory from runtime.
2. Creates `deps.Tap` with `tapper.NewTap(...)`.
3. Applies optional logger settings.
4. Attaches all subcommands.

Every subcommand receives the same `*Deps`, so command handlers do not
reconstruct core services.

## Command Pattern

Most commands follow this shape:

1. Bind flags into a typed options struct.
2. Apply profile-specific target behavior.
   `tap` uses the full profile.
   `kegv2` uses a pruned profile that forces project resolution and drops
   config/repo command surfaces.
3. Call a single method on `deps.Tap`.
4. Write returned output to stdout.

Example command files:

- `pkg/cli/cmd_cat.go`
- `pkg/cli/cmd_info.go`
- `pkg/cli/cmd_repo_config.go`

## Profile Differences

Profiles are defined in `pkg/cli/profile.go`.

- `TapProfile` enables repo/config commands and alias flags.
- `KegV2Profile` forces project-style resolution and disables alias/config/repo
  command surfaces.
- Snapshot/archive commands (`snapshot`, `archive import`, `archive export`)
  are shared by both profiles. The main difference is target resolution:
  `kegv2` resolves against the active project by default, while `tap` can
  target configured aliases or explicit paths.

## Why The Profile Technique Matters

The command tree is defined once in `pkg/cli/cmd_root.go` and then filtered by
the selected `Profile`.

That gives you:

- one implementation path for shared commands
- one service graph (`deps.Tap`) regardless of binary name
- the ability to publish a narrower binary without forking command logic

In practice, `tap` stays the canonical interface and smaller binaries can be
added later when a focused workflow benefits from a reduced surface area.
