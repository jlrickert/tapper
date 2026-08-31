# CLI And Command Flow

This page describes how `tap` executes a command from process start to service
call.

## Entrypoints

- `cmd/tap/tap.go` calls `cli.Run(ctx, rt, os.Args[1:])`

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
2. Merge root remote KEG target defaults.
3. Call a single method on `deps.Tap`.
4. Write returned output to stdout.

Example command files:

- `pkg/cli/cmd_cat.go`
- `pkg/cli/cmd_info.go`
- `pkg/cli/cmd_repo_config.go`

`TapProfile` in `pkg/cli/profile.go` controls host-integration registration,
while KEG operations always resolve through remote Hub targets.
