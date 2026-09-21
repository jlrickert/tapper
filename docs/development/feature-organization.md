# Feature Organization

Features are vertical slices: each capability cuts through every layer from the
Tap API down to tests. CLI and MCP are peer surfaces — both must expose the same
features at parity.

## Feature anatomy

| Layer             | Location pattern        | Purpose                                                  |
| ----------------- | ----------------------- | -------------------------------------------------------- |
| **Tap API**       | `pkg/tapper/tap_*.go`   | Business logic method + `*Options` struct                |
| **CLI command**   | `pkg/cli/cmd_*.go`      | Cobra command wiring flags to the Tap method             |
| **Completions**   | `pkg/cli/cmd_*.go`      | `ValidArgsFunction` and custom completers for flags/args |
| **MCP tool**      | `pkg/mcp/tools_*.go`    | JSON-RPC tool with input struct and `jsonschema` tags    |
| **Tests**         | `*_test.go` in each pkg | Unit, integration, completion, and MCP tool tests        |
| **Documentation** | `docs/`                 | User-facing docs for the capability                      |

**Example — `create`:** `Tap.Create()` in `tap_create.go` → `createCmd` in
`cmd_create.go` (with node-ID completions) → `registerCreateTools()` in
`tools_create.go` → tests in each package.

## Parity rules

- CLI and MCP must expose the same features. A missing surface is a bug.
- Both accept equivalent parameters that map 1:1 to the shared `*Options`
  struct.
- Tests verify both surfaces produce equivalent results for the same input.
- Docs document features, not surfaces — one description covers both CLI and MCP
  usage.

## Checklist

When adding or modifying a feature, update each of these:

1. **Tap API** (`pkg/tapper/tap_*.go`) — business logic method with tests
2. **CLI command** (`pkg/cli/cmd_*.go`) — Cobra command wiring flags to the Tap
   method
3. **Shell completions** — register `ValidArgsFunction` and custom completers
   for all flags and positional arguments (node IDs, keg aliases, tags, etc.).
   Verify with `go test ./pkg/cli/... -run Completion`
4. **MCP tool** (`pkg/mcp/tools_*.go`) — MCP tool exposing the same capability
   over JSON-RPC, with input struct and `jsonschema` tags
5. **Documentation** — user-facing docs for the new capability
6. **Tests** — unit tests for the Tap method, CLI integration tests, MCP tool
   tests, and completion tests

**Configuration changes:** Any change to configuration structure must also
update the JSON Schema files under `schemas/`:

- `schemas/tap-config.json` — tap user/project config schema
- `schemas/keg-settings.json` — keg settings schema

These schemas are referenced by editors for validation and completion hints. A
config field added without a schema update will lack editor support and
validation.

## Read Next

- [Testing](testing.md)
- [Configuration Resolution](../architecture/configuration-resolution.md)
