# CLI And Command Flow

This page describes how `tap` and `kegv2` execute a command from process start
to service call.

## Entrypoints

- `cmd/tap/tap.go` calls `cli.Run(ctx, rt, os.Args[1:])`
- `cmd/kegv2/keg.go` calls `cli.RunWithProfile(..., cli.KegV2Profile())`

The two binaries share the same command framework, with profile-based behavior
differences.

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
2. Apply profile-specific target behavior (for example force project mode in
   `kegv2`).
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
