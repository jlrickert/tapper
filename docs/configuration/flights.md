# Flights

A **flight** is an optional overlay on a tapper session: a restriction on which
kegs are available, plus a block of agent instructions. It is not a config key —
flights live in their own manifests and are selected per-invocation with
`--flight`.

## What A Flight Does

A flight carries two independent things, either of which may be empty:

1. **A keg allow-list** (`allowedKegs`). When non-empty, any keg resolved during
   the session must be on the list or the command is rejected. An empty
   allow-list restricts nothing (an instructions-only flight).
2. **Agent instructions** (`instructions`). These are injected into the `tap
   orient` payload at tiers 1–2, so an agent that orients under a flight sees the
   flight's guidance.

Because a flight is an overlay rather than a target selector, `--flight`
**composes** with `--keg`, `--project`, `--path`, and `--cwd`: those pin which
keg you operate on; the flight gates and annotates the result.

## Manifest Format

Local flights live beside the `@<namespace>` directories of the local hub, in a
reserved `flights.d` directory:

```text
<local-hub-basePath>/flights.d/<name>.yaml
```

(`flights.d` is deliberately not a legal namespace — it contains a dot — so it
can never collide with a keg path.) The file stem is the flight name. Each
manifest has three optional fields:

```yaml
# <basePath>/flights.d/release-42.yaml
title: Release 42 cut
allowedKegs:
  - tapper            # an alias, or…
  - "@me/public"      # a fully qualified @namespace/keg
instructions: |
  Only touch the release notes and changelog kegs.
  Snapshot every node before editing.
```

`allowedKegs` entries are matched either as a config alias or as a qualified
`@namespace/keg` reference, so both naming styles work.

Remote flights (served by the hub API) are **not yet implemented**; only local
filesystem flights are discovered today.

## Commands

| Goal                                  | Command                                   |
| ------------------------------------- | ----------------------------------------- |
| List discovered flights               | `tap flight list`                         |
| Show a flight's allow-list + body     | `tap flight show <name>`                  |
| Run a command under a flight overlay  | `tap --flight <name> <command>`           |

The same surface is exposed over MCP as the `list_flights` and `flight_show`
tools, and the `--flight` parameter flows through `orient`.

## Behavior

- A keg outside the active flight's `allowedKegs` is rejected with a
  "keg … is not available in flight …" error.
- A missing `flights.d` directory means "no flights", not an error.
- `tap orient --flight <name> --tier 1` (or higher) injects the flight's title,
  available kegs, and instructions into the orientation payload.
