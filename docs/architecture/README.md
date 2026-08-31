# Architecture Overview

This section documents how tapper is structured internally and how commands move
through the stack.

## Audience

Use these docs when you are:

- adding or changing CLI commands
- changing keg/config resolution behavior
- debugging remote Hub and namespace selection
- extending low-level repository behavior
- writing integration-style CLI tests

## Layered Model

1. CLI entrypoint (`cmd/tap`)
2. Cobra command tree and shared dependencies (`pkg/cli`)
3. Tap client and service layer (`pkg/tapper`)
4. KEG domain and repository abstraction (`pkg/keg`)
5. Hub-side repository implementations (PostgreSQL in Tapper Hub)
6. Remote test servers, repository-independent memory tests, and PostgreSQL integration tests

## Read Next

- [CLI And Command Flow](cli-and-command-flow.md)
- [Service Layer](service-layer.md)
- [Repository Layer](repository-layer.md)
- [Testing Architecture](testing-architecture.md)
