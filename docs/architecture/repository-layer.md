# Repository Layer

`pkg/keg/repository.go` defines the storage contract used by `LocalKeg` for
node data, indexes, settings, attachments, snapshots, locks, and archives.

Tapper clients do not construct a repository. `NewKegFromTarget` accepts only
resolved Hub references and HTTP(S) endpoints and returns a `RemoteKeg`. Each
`RemoteKeg` operation maps to one Hub request.

Tapper Hub constructs `LocalKeg` with its PostgreSQL repository. That is the
only production persistence path: orchestration, indexing, validation, and
locking remain in `LocalKeg`, while PostgreSQL supplies durable storage.

The Tapper test suite has a concurrency-safe in-memory implementation in a
`_test.go` file. It exists solely for repository-independent `LocalKeg` tests
and is neither available nor linked in production builds. Repository behavior
itself is verified by Tapper Hub's PostgreSQL integration suite.

This split keeps three boundaries explicit:

- `RemoteKeg` verifies the HTTP contract and one-round-trip behavior.
- `LocalKeg` tests verify repository-independent orchestration quickly.
- Tapper Hub PostgreSQL tests verify durable repository semantics.
