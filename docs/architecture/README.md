# Architecture Overview

This section documents how tapper is structured internally and how commands move
through the stack.

## Audience

Use these docs when you are:

- adding or changing CLI commands
- changing keg/config resolution behavior
- debugging selection logic between config and project-local kegs
- extending low-level repository behavior
- writing integration-style CLI tests

## Layered Model

1. CLI entrypoints (`cmd/tap`, `cmd/kegv2`)
2. Cobra command tree and shared dependencies (`pkg/cli`)
3. Tap client and service layer (`pkg/tapper`)
4. KEG domain and repository abstraction (`pkg/keg`)
5. Filesystem or memory-backed storage implementations (`pkg/keg`)
6. Sandbox-backed integration tests (`pkg/cli/*_test.go`, `pkg/tapper/*_test.go`)

## Read Next

- [CLI And Command Flow](cli-and-command-flow.md)
- [Service Layer](service-layer.md)
- [Repository Layer](repository-layer.md)
- [Testing Architecture](testing-architecture.md)
