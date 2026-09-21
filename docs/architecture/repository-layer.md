# Repository Layer

`pkg/keg/repository.go` defines the storage contract used by `LocalKeg` for
node data, indexes, settings, attachments, snapshots, locks, and archives.

Tapper clients do not construct a repository. `NewKegFromTarget` accepts only
resolved Hub references and HTTP(S) endpoints and returns a `RemoteKeg`. Each
`RemoteKeg` operation maps to one Hub request.

A hub server constructs `LocalKeg` with its own repository implementation.
That is the only production persistence path: orchestration, indexing,
validation, and locking remain in `LocalKeg`, while the hub supplies durable
storage. Which storage engine it uses is the hub's business — Tapper reaches it
only through the REST endpoint.

The Tapper test suite has a concurrency-safe in-memory implementation in a
`_test.go` file. It exists solely for repository-independent `LocalKeg` tests
and is neither available nor linked in production builds. Repository behavior
itself is verified by the hub's own integration suite.

This split keeps three boundaries explicit:

- `RemoteKeg` verifies the HTTP contract and one-round-trip behavior.
- `LocalKeg` tests verify repository-independent orchestration quickly.
- The hub's own tests verify durable repository semantics.
