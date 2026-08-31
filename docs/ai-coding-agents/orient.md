# Orientation Surface

The `orient` surface gives an agent one deterministic bootstrap payload for
operating against tapper KEGs. The `mcp__tapper__orient` tool and
`tapper://orient` MCP resource return identical session-aware bytes. The
`tap orient` CLI uses the same canonical renderer for a direct flight preview,
but it has no pinned session graph to aggregate.

## Payload Order

The payload always starts with KEG system context:

1. KEG purpose and core rules.
2. Available KEGs, including each canonical reference, title, concise summary,
   highest effective role, source, and every canonical flight granting that
   winning role. Default MCP orientation aggregates the pinned root and its
   accessible transitive descendants; explicit `flight` is exact.
3. Connection-pinned root, selected flight, root-first ordered breadth-first selectable flights,
   selected canonical path, revision, and only the selected flight's title and
   instructions.
4. A prompt to request targeted KEG settings before operating.
5. Canonical Tapper agent guidance: linking, snapshots, tool inventory, and
   troubleshooting.

Orient is best-effort. If a configured hub is unreachable or a selected flight
cannot be resolved, the payload still renders the KEG system basics and includes
a warning about what was skipped.

## Parameters

The CLI accepts an optional flight preview. The MCP tool and every other
authority-bearing tool accept an optional top-level `flight`. Omission keeps
pinned-root operational authority; for `orient` and `keg_list` only, omission
renders graph-wide discovery. Supplying `flight` always selects an exact root
or accessible descendant.

| Parameter | Values | Effect |
| --- | --- | --- |
| `flight` (CLI) | flight identifier, for example `@acme/+release-42` | Previews that flight's title, instructions, and KEG cover. |
| `flight` (MCP) | pinned root, canonical descendant, or root-namespace `+slug` | Selects the root or an identity-accessible transitive descendant for this call. Ancestor instructions and authority are not inherited. |

`tap orient` rejects KEG, namespace, and hub targeting flags. Direct CLI KEG
commands ignore flight cover caps and use normal authorization; MCP tools load
and enforce a fresh call-local orientation independently for every call.

## Progressive KEG Guidance

`summary` and `instructions` have distinct roles in a KEG settings:

```yaml
kegv: 2025-07
title: Engineering
summary: Architecture, delivery, and operational knowledge for engineering.
instructions: |
  Prefer architecture notes before implementation notes.
  Snapshot any node before changing public API guidance.
```

`summary` is the concise discovery description shown by graph orientation and
identity `keg_search`.
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

The MCP server registers read-only `orient` plus zero-argument
`session_refresh`. Initialization pins authority internally but returns only a
minimal directive to call `orient`. `orient` never changes session state:

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

The response body is one markdown text block containing the payload. If live
flight, identity, grant, visibility, KEG, or relation authority changes, the
next call adopts them automatically. A race between Tapper's resolution and
Hub validation returns `ORIENTATION_STALE` without performing the operation.
Fresh permission or selection failures return `ORIENTATION_DENIED`; transient
Hub failures return `ORIENTATION_UNAVAILABLE`; permanent root loss returns
`ORIENTATION_ROOT_UNAVAILABLE`. Mutations are never replayed automatically.

When initialization could not activate an explicitly configured real flight,
repairing that same selection is adopted only by `session_refresh`. It returns
`activated`, `already_active`, or `selection_required` as structured
status. Activation directs the caller to `orient`. An already-active refresh
makes no provider call and cannot replace connection-pinned authority. For a
no-flight connection it reports `nextAction:"new_session"`, because narrowing
access requires reconnecting. A failed refresh returns
`SESSION_REFRESH_FAILED`, keeps the prior mode and tool surface, and reports
`toolsChanged:false`.

`keg_search` is deliberately outside this authority flow. It performs a
case-insensitive literal match over canonical ref, title, and summary for all
identity-accessible KEGs and returns at most 50 canonical rows. Finding a KEG
does not change authority: no-flight calls still use the identity role, while a
real-flight call must also cover it.

## MCP Resource

For MCP hosts that prefer resource fetches over tool calls, the server registers
one resource:

```text
tapper://orient
```

`resources/read` on that URI returns bytes byte-identical to a bare
`mcp__tapper__orient` call. It is read-only in every mode. Resources have no
flight parameter, so no-flight sessions use identity authority, real-root
sessions use root authority and graph-wide discovery, and failed explicit
selections return their published recovery state.

## CLI

`tap orient` is the shell mirror of the tool. It is useful for previewing the
payload an agent will see or scripting orientation into CI.

```bash
tap orient
tap orient --flight @acme/+release-42
```

`--flight` is a root persistent CLI flag available on commands that accept
`--keg`. It is free-form and suppresses filesystem completion. MCP root
selection is fixed by the human session boundary; the optional MCP `flight`
field selects only that root or a flattened descendant for one call and cannot
replace the root.

## MCP Byte-Equivalence Guarantee

The MCP tool and resource capture the same live session candidate and return
the same bytes. The CLI shares payload structure and exact-flight rendering,
while MCP adds pinned-root graph discovery. Tests in `pkg/parity/` and
`pkg/mcp/` enforce these contracts at CI time.

## See Also

- [Claude Code Plugin](claude-code-plugin.md) — one-command install that
  registers the MCP server and bundled skill.
- [Codex Install](codex.md) — one-command install for Codex, including the
  native baseline plugin skill.
- [MCP Server Setup](mcp-setup.md) — manual MCP registration for hosts that
  do not ship a tapper integration.
- [Agent Conventions](agent-conventions.md) — the invariants the orient
  payload describes in long form.
