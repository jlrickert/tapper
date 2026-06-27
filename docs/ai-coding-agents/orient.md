# Orientation Surface

The `orient` surface gives an agent a bounded, tiered bootstrap payload for
operating against a tapper KEG. The same bytes are reachable three ways — the
`mcp__tapper__orient` tool, the `tapper://orient/<host>/tier-<n>` MCP resource,
and the `tap orient` CLI — because all three delegate to a single payload
builder in the tap API.

Agents should call tier 0 first to fit within a tight token budget, then
escalate to tier 1 or tier 2 only when more context is required.

## Tiers

| Tier | Content                                                                  | Intended use                                                |
| ---- | ------------------------------------------------------------------------ | ----------------------------------------------------------- |
| 0    | Purpose paragraph, active keg, rules summary                             | Bounded bootstrap (~300 tokens)                             |
| 1    | Tier 0 plus linking conventions and snapshot policy                      | Enough context to read and edit safely                      |
| 2    | Tier 1 plus full canonical agent guidance and the rendered host artifact | Full orientation (SKILL.md for Claude, AGENTS.md for Codex) |

Tier 1 emits a per-keg manifest placeholder when `keg` is supplied (the shape is
wired today; the payload will follow). When `flight` is supplied, tier 1+ injects
the resolved flight: its title, available kegs, and the flight's agent
instructions. See [Flights](../configuration/flights.md).

Requesting an out-of-range tier clamps to the nearest valid tier rather than
erroring.

## Parameters

All three surfaces accept the same four optional parameters.

| Parameter | Values                      | Effect                                                                                                                 |
| --------- | --------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `host`    | `claude`, `codex`           | Tier 2 appends the rendered host artifact. Unknown hosts return an error.                                              |
| `keg`     | keg reference (e.g. `@acme/engineering`) | Pins the active keg in tier 0; tier 1+ emits a per-keg manifest placeholder. |
| `flight`  | flight identifier           | Tier 1+ injects the flight's title, cover, and instructions. Composes with `keg` because a flight is context, not a target selector. |
| `tier`    | `0`, `1`, `2` (default `0`) | Selects payload depth.                                                                                                 |

## MCP tool

The MCP server registers a single `orient` tool on stdio:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "orient",
    "arguments": {
      "host": "claude",
      "tier": 2
    }
  }
}
```

The response body is a single text block containing the payload. At tier 2
with `host` set, the block includes both the canonical agent guidance and the
rendered host artifact (Claude `SKILL.md` or Codex `AGENTS.md`) appended under
a `## Host:` section.

## MCP resources

For hosts that prefer resource fetches over tool calls, the server also
registers one resource per (host, tier) pair:

```
tapper://orient/claude/tier-0
tapper://orient/claude/tier-1
tapper://orient/claude/tier-2
tapper://orient/codex/tier-0
tapper://orient/codex/tier-1
tapper://orient/codex/tier-2
```

A `resources/list` call enumerates all six. `resources/read` on any URI returns
bytes byte-identical to `orient` tool output at the matching host and tier —
the resource is a cacheable mirror of the tool, not a separate code path.

## CLI

`tap orient` is the shell mirror of the tool. It is useful for previewing the
payload an agent will see, scripting orientation into CI, or debugging tier
transitions.

```bash
tap orient                            # tier 0, no host
tap orient --tier 1                   # tier 1, no host
tap orient --host claude --tier 2     # full claude payload
tap orient --host codex --tier 2      # full codex payload
tap orient --keg @acme/engineering --tier 1    # tier 1 with an explicit keg
tap orient --flight @acme/+release-42 --tier 1 # tier 1 scoped to a flight
```

`--flight` is a root persistent flag (available on every command that accepts
`--keg`). It is flight context for orient and MCP — cover caps plus agent
instructions — so it **composes** with `--keg`, `--namespace`, and `--hub`
rather than excluding them. Direct CLI commands ignore flight cover caps and use
normal keg authorization; MCP tools enforce the cover. Flag
completion on `--host` enumerates the hosts the binary knows about; `--tier`
completes `0 1 2`; `--flight` is free-form and suppresses filesystem
completion.

## Host matrix

| Host     | Tier 0 | Tier 1 | Tier 2                                |
| -------- | ------ | ------ | ------------------------------------- |
| (none)   | yes    | yes    | canonical body only                   |
| `claude` | yes    | yes    | canonical body + rendered `SKILL.md`  |
| `codex`  | yes    | yes    | canonical body + rendered `AGENTS.md` |

A new host becomes orientable by registering an adapter and mapping it to a
rendered artifact; the resource list and CLI completion pick it up
automatically.

## Byte-equivalence guarantee

The tool, resources, and CLI all delegate to `Tap.Orient`. Given matching
inputs, every surface returns the same bytes. Tests in `pkg/parity/` enforce
this at CI time, and the embed-integrity test enforces that the rendered
artifacts appended at tier 2 match what ships inside the binary.

## When to use which surface

| You are…                                         | Use                                         |
| ------------------------------------------------ | ------------------------------------------- |
| An agent bootstrapping a new session             | `orient` tool at tier 0, escalate as needed |
| An MCP host that caches resources                | `tapper://orient/<host>/tier-<n>` resources |
| A shell user or CI script previewing the payload | `tap orient`                                |
| Debugging a tier boundary or diffing payloads    | `tap orient --tier N > /tmp/tier-N.md`      |

## See also

- [Claude Code Plugin](claude-code-plugin.md) — one-command install that
  registers the orient surface alongside the MCP server and the bundled skill.
- [Codex Install](codex.md) — one-command install for Codex, including the
  rendered `AGENTS.md` that tier 2 orient serves.
- [MCP Server Setup](mcp-setup.md) — manual MCP registration for hosts that
  do not ship a tapper integration.
- [Agent Conventions](agent-conventions.md) — the invariants the orient
  payload describes in long form.
