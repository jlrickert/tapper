# Testing Architecture

Tapper uses three complementary test layers.

## Repository-independent orchestration

`pkg/keg` tests construct `LocalKeg` over a concurrency-safe in-memory
repository defined only in test files. These tests cover orchestration,
validation, indexes, snapshots, locks, attachments, and archives without
making a filesystem repository part of the product.

The memory repository must satisfy `Repository` and every optional capability
that a test exercises. Compile-time interface assertions catch contract drift,
and race/concurrency tests exercise its locking behavior. It is a test double,
not a persistence implementation.

## Remote client and command surfaces

`RemoteKeg`, CLI, and MCP tests use Hub-compatible `httptest` servers. They
assert request paths, authentication, conditional hashes, serialization,
errors, and remote-only resolution. Filesystem paths and `file://` targets are
negative cases.

## Durable storage

A hub owns the production repository and tests it in its own suite:
transactions, locks, snapshots, schemas, attachments, archives, and concurrent
mutation semantics. None of that is this repository's to run or document — the
`httptest` servers above are how Tapper verifies its half of the contract.
