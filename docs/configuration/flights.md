# Flights

A **flight** is an optional overlay on a tapper session: a restriction on which
kegs are available, plus a block of agent instructions. It is not a config key —
flights live in their own manifests and are selected per-invocation with
`--flight`.

## What A Flight Does

A flight carries two independent things, either of which may be empty:

1. **A keg cover** (`cover`). When non-empty, any keg resolved during the
   session must be covered or the command is rejected. Each cover entry has a
   `viewer` or `editor` cap; writes require `editor`. An empty cover restricts
   nothing for local instructions-only flights.
2. **Agent instructions** (`instructions`). These are injected into the `tap
   orient` payload at tiers 1–2, so an agent that orients under a flight sees the
   flight's guidance.

Because a flight is an overlay rather than a target selector, `--flight`
**composes** with `--keg`, `--namespace`, and `--hub`: those pin which keg you
operate on; the flight gates and annotates the result.

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
cover:
  - namespace: acme
    keg: release-notes
    role: editor
  - namespace: acme
    keg: engineering
    role: viewer
instructions: |
  Only touch the release notes and engineering kegs.
  Snapshot every node before editing.
```

A cover entry without an explicit `role` defaults to `viewer` — the same
default applies to `--cover` specs on the CLI and MCP surfaces; `editor` must
be requested explicitly.

Older local manifests that use `allowedKegs` still load; each bare entry is
treated as an `editor` cover row for backward compatibility, while an entry
with an explicit `=viewer` suffix keeps its viewer cap.

Remote flights are served by Hub and addressed canonically as
`@namespace/+slug`. `tap flight create/edit/delete` manage Hub-backed flights;
local `flights.d` manifests remain read-only files.

## Commands

| Goal                                  | Command                                   |
| ------------------------------------- | ----------------------------------------- |
| List discovered flights               | `tap flight list`                         |
| Show a flight's cover + body          | `tap flight show @namespace/+slug`        |
| Run a command under a flight overlay  | `tap --flight @namespace/+slug <command>` |
| Create a Hub-backed flight            | `tap flight create @namespace/+slug --cover @namespace/keg=viewer` |
| Edit a Hub-backed flight in $EDITOR   | `tap flight edit @namespace/+slug` (the manifest opens as YAML; every save is applied) |
| Apply a manifest from a script        | `cat manifest.yaml \| tap flight edit @namespace/+slug` |
| Delete a Hub-backed flight            | `tap flight delete @namespace/+slug`      |

The same surface is exposed over MCP as the `list_flights`, `flight_show`,
`flight_create`, `flight_update`, and `flight_delete` tools, and the
`--flight` parameter flows through `orient`. `flight_update` (partial update;
omitted fields keep current values) is the agent-facing equivalent of the
CLI's piped `flight edit`, since agents cannot open editors.

## Behavior

- A keg outside the active flight's cover is rejected with a
  "keg … is not available in flight …" error.
- A write against a `viewer` cover row is rejected as viewer-only.
- A missing `flights.d` directory means "no flights", not an error.
- `tap orient --flight @namespace/+slug --tier 1` (or higher) injects the flight's title,
  available kegs, and instructions into the orientation payload.
