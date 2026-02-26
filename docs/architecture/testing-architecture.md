# Testing Architecture

tapper uses unit tests and integration-style CLI tests with a sandbox runtime.

## Unit Tests

Unit tests live beside implementation files (for example `pkg/keg/*_test.go`).

They focus on:

- pure behavior of domain and service methods
- deterministic config and resolution behavior
- repository-specific edge cases

## Sandbox Integration Pattern

CLI integration tests use `github.com/jlrickert/cli-toolkit/sandbox`.

Common setup pattern:

1. Build a sandbox with fixture data (`NewSandbox(...)` in test helpers).
2. Build a command process with `tu.NewProcess(...)`.
3. Run commands against sandbox context/runtime.
4. Assert stdout/stderr and filesystem effects.

This creates a close-to-real execution path without shelling out to external
processes.

## Configurable Command Pipelines

A single test usually runs multiple commands sequentially against the same
sandbox runtime, which acts like an in-memory workflow pipeline.

Example sequence:

1. `tap repo init ...`
2. `tap create ...`
3. `tap cat ...`

Because each command runs through `cli.Run(...)`, tests exercise the same
command wiring and service resolution code used in real usage.

## Fixture-Driven Coverage

Fixtures under package test data directories provide:

- known keg layouts
- known repo config files
- expected node/index contents

This keeps tests reproducible and avoids fragile ad hoc setup logic.
