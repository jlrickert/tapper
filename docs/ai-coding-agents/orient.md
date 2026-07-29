# Orientation Surface

The `orient` surface gives an agent one deterministic bootstrap payload for
operating against tapper KEGs. The same bytes are reachable three ways: the
`mcp__tapper__orient` tool, the `tapper://orient` MCP resource, and the
`tap orient` CLI. All three delegate to `Tap.Orient`.

## Payload Order

The payload always starts with KEG system context:

1. KEG purpose and core rules.
2. Available KEGs, including each canonical reference, title, concise summary,
   role, source, and active flight cap when a flight is selected.
3. Flight title and instructions, when a flight is active.
4. A prompt to request targeted KEG settings before operating.
5. Canonical Tapper agent guidance: linking, snapshots, tool inventory, and
   troubleshooting.

Orient is best-effort. If a configured hub is unreachable or a selected flight
cannot be resolved, the payload still renders the KEG system basics and includes
a warning about what was skipped.

## Parameters

The CLI accepts an optional flight preview. The MCP tool accepts `{}` and
refreshes the server-owned session orientation.

| Parameter | Values | Effect |
| --- | --- | --- |
| `flight` (CLI only) | flight identifier, for example `@acme/+release-42` | Renders flight title/instructions and caps the available KEG list to the flight cover. |

`tap orient` rejects KEG, namespace, and hub targeting flags. Direct CLI KEG
commands ignore flight cover caps and use normal authorization; MCP tools
enforce the most recently published orientation.

## Progressive KEG Guidance

`summary` and `instructions` have distinct roles in a KEG config:

```yaml
kegv: 2025-07
title: Engineering
summary: Architecture, delivery, and operational knowledge for engineering.
instructions: |
  Prefer architecture notes before implementation notes.
  Snapshot any node before changing public API guidance.
```

`summary` is the concise discovery description shown by aggregate orientation.
It should help an agent decide whether the KEG is relevant and is not
automatically truncated. `instructions` is targeted operational guidance.
Aggregate orientation never includes it, even under `full_access`.

After selecting relevant KEGs, call `keg_settings` with either `keg` or
`kegs`. Minimal mode is the default and returns title, summary, updated
metadata, and instructions. Up to 100 canonical references may be expanded
together:

```json
{"kegs":["@foldwise/dev","@foldwise/engineering"]}
```

`keg` and `kegs` are mutually exclusive. Multiple KEGs require minimal mode;
`minimal=false` continues to return the complete config for exactly one KEG.

## MCP Tool

The MCP server registers a single `orient` tool:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "orient",
    "arguments": {}
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
tap orient --flight @acme/+release-42
```

`--flight` is a root persistent CLI flag available on commands that accept
`--keg`. It is free-form and suppresses filesystem completion; it is not part
of any ordinary MCP tool schema. MCP flight selection is fixed by the human
session boundary and cannot be overridden by `orient` or `keg_settings`.

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
