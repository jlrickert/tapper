# Orientation Surface

The `orient` surface gives an agent one deterministic bootstrap payload for
operating against tapper KEGs. The same bytes are reachable three ways: the
`mcp__tapper__orient` tool, the `tapper://orient` MCP resource, and the
`tap orient` CLI. All three delegate to `Tap.Orient`.

## Payload Order

The payload always starts with KEG system context:

1. KEG purpose and core rules.
2. The active or resolved KEG.
3. Available KEGs, including role, source, and the active flight cap when a
   flight is selected.
4. Flight title and instructions, when a flight is active.
5. KEG-level instructions from each available KEG config.
6. Canonical Tapper agent guidance: linking, snapshots, tool inventory, and
   troubleshooting.

Orient is best-effort. If a configured hub is unreachable or a selected flight
cannot be resolved, the payload still renders the KEG system basics and includes
a warning about what was skipped.

## Parameters

The tool and CLI accept two optional context parameters.

| Parameter | Values | Effect |
| --- | --- | --- |
| `keg` | keg reference, for example `@acme/engineering` | Pins active KEG resolution. |
| `flight` | flight identifier, for example `@acme/+release-42` | Renders flight title/instructions and caps the available KEG list to the flight cover. |

`flight` is context, not a target selector, so it composes with `keg`,
`namespace`, and `hub`. Direct CLI commands ignore flight cover caps and use
normal keg authorization; MCP tools enforce the cover.

## KEG Instructions

KEG-specific guidance belongs on the KEG config itself:

```yaml
kegv: 2025-07
title: Engineering
instructions: |
  Prefer architecture notes before implementation notes.
  Snapshot any node before changing public API guidance.
```

When the KEG is available in the active orient context, those instructions
render in the `## KEG Instructions` section before canonical Tapper guidance.

## MCP Tool

The MCP server registers a single `orient` tool:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "orient",
    "arguments": {
      "flight": "@acme/+release-42",
      "keg": "@acme/engineering"
    }
  }
}
```

The response body is one markdown text block containing the payload.

## MCP Resource

For MCP hosts that prefer resource fetches over tool calls, the server registers
one resource:

```text
tapper://orient
```

`resources/read` on that URI returns bytes byte-identical to a bare
`mcp__tapper__orient` call.

## CLI

`tap orient` is the shell mirror of the tool. It is useful for previewing the
payload an agent will see or scripting orientation into CI.

```bash
tap orient
tap orient --keg @acme/engineering
tap orient --flight @acme/+release-42
tap orient --flight @acme/+release-42 --keg @acme/engineering
```

`--flight` is a root persistent flag available on commands that accept `--keg`.
It is free-form and suppresses filesystem completion.

## Byte-Equivalence Guarantee

The tool, resource, and CLI all delegate to `Tap.Orient`. Given matching
inputs, every surface returns the same bytes. Tests in `pkg/parity/` enforce
this at CI time.

## See Also

- [Claude Code Plugin](claude-code-plugin.md) — one-command install that
  registers the MCP server and bundled skill.
- [Codex Install](codex.md) — one-command install for Codex, including the
  native baseline plugin skill.
- [MCP Server Setup](mcp-setup.md) — manual MCP registration for hosts that
  do not ship a tapper integration.
- [Agent Conventions](agent-conventions.md) — the invariants the orient
  payload describes in long form.
