# Repository Layer

The repository layer is the low-level data exchange boundary for KEG data.

## Repository Contract

`pkg/keg/repository.go` defines `Repository`, the core interface used by
high-level keg operations.

It covers:

- node lifecycle (next/list/move/delete)
- node data (content/meta/stats)
- indexes (`dex/*`)
- keg config (`keg` file)
- optional capabilities (files/images/snapshots)

Commands and services rely on this contract instead of directly accessing files.

## Implementations

Primary implementations in `pkg/keg`:

- `repo_memory.go` for in-memory repositories
- `repo_filesystem.go` for filesystem-backed repositories

`NewKegFromTarget` in `pkg/keg/keg.go` selects an implementation from a
`kegurl.Target` scheme (`memory` or `file`).

## High-Level KEG Service

`pkg/keg/keg.go` wraps the repository with a stateful API:

- `Init` for keg bootstrap (config + zero node + indexes)
- `Create`, `Read`, `Move`, `Delete` for node lifecycle
- index and query-oriented operations over dex data

This separation allows command code to stay simple while storage behavior stays
centralized and testable.

## Why The Boundary Matters

- storage can change without rewriting command handlers
- tests can run against memory repos for fast behavior checks
- file-backed behavior can be exercised independently in filesystem tests
