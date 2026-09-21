# Testing

How tests are set up in this repository. [Testing Architecture](../architecture/testing-architecture.md)
covers the layering; this page covers the working rules.

- **Sandbox pattern**: Tests use `sandbox.NewSandbox(t, ...)` from cli-toolkit,
  which creates a jailed temp directory with a test runtime (mock clock, MD5
  hasher, test logger).
- **Fixtures**: `pkg/keg/data/` contains `empty`, `example`, `home` fixtures.
  `pkg/tapper/data/` contains `basic`, `example`, `keep`.
- **Repository fixtures**: repository-independent behavior tests use
  `internal/testkegrepo`, which is imported only by `_test.go` files. Durable
  storage behavior — transactions, restarts, namespace isolation — belongs to
  the hub's own integration suite, not this repository's.
- **Testify**: Uses `github.com/stretchr/testify/require` for assertions.
- **Race detection**: Run `go test -race ./pkg/keg/...` and
  `go test -race ./pkg/tapper/...` to verify concurrent safety.
- **Parity tests**: `pkg/parity/` contains table-driven tests that verify CLI
  commands and MCP tools produce equivalent results for the same Tap API
  operations. The coverage test (`TestCoverage_AllTapMethodsHaveBothSurfaces`)
  uses reflection to check that every exported Tap method has both a CLI command
  and MCP tool registered. When adding a new feature, add a parity test case to
  the appropriate file (`parity_read_test.go`, `parity_write_test.go`, or
  `parity_utility_test.go`). Run with `go test ./pkg/parity/...` and
  `go test -race ./pkg/parity/...`.

## Read Next

- [Feature Organization](feature-organization.md)
- [Testing Architecture](../architecture/testing-architecture.md)
