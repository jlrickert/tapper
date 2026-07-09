# Flights

A **flight** is an optional agent context: a cover for MCP sessions plus a
block of agent instructions. It is not a config key — flights live in their own
manifests and are selected per-invocation with `--flight`.

## What A Flight Does

A flight carries two independent things, either of which may be empty:

1. **A keg cover** (`cover`). When non-empty, MCP tools reject kegs outside the
   cover. Each cover entry has a `viewer` or `editor`
   cap; writes require `editor`. An empty cover restricts nothing for local
   instructions-only flights. Direct CLI commands ignore these caps and use
   normal keg authorization.
2. **Agent instructions** (`instructions`). These are injected into the `tap
   orient` payload, so an agent that orients under a flight sees the flight's
   guidance before general Tapper guidance.

Because a flight is not a target selector, `--flight` **composes** with `--keg`,
`--namespace`, and `--hub`: those pin which keg you operate on; the flight adds
context and, for the MCP surface, gates the result.

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
| Run MCP/orient with a flight context  | `tap --flight @namespace/+slug <command>` |
| Create a Hub-backed flight            | `tap flight create @namespace/+slug --cover @namespace/keg=viewer` |
| Edit a Hub-backed flight in $EDITOR   | `tap flight edit @namespace/+slug` (the manifest opens as YAML; every save is applied) |
| Apply a manifest from a script        | `cat manifest.yaml \| tap flight edit @namespace/+slug` |
| Delete a Hub-backed flight            | `tap flight delete @namespace/+slug`      |

`tap flight edit` opens a non-empty YAML document even for an empty flight. The
first line is a `yaml-language-server` schema modeline for
`schemas/flight-manifest.json`, followed by a short comment that the
`@namespace/+slug` ref is immutable. The editable fields are `title`, `cover`,
and `instructions`; comments and the modeline are ignored when deciding whether
the manifest changed.

The same surface is exposed over MCP as the `list_flights`, `flight_show`,
`flight_create`, `flight_edit`, and `flight_delete` tools. `--flight` also
flows through `tap orient` so orientation can render flight instructions, even
though direct CLI reads/writes are not capped by the flight cover. `flight_edit`
(partial update; omitted fields keep current values) is the agent-facing
equivalent of the CLI's piped `flight edit`, since agents cannot open editors.

## Behavior

- MCP tools reject a keg outside the active flight's cover
  with a "keg … is not available in flight …" error.
- MCP writes against a `viewer` cover row are rejected as viewer-only.
- Direct CLI commands such as `tap cat`, `tap edit`, and `tap create` ignore
  flight cover caps; access is governed by normal keg authorization.
- A missing `flights.d` directory means "no flights", not an error.
- `tap orient --flight @namespace/+slug` injects the flight's title, available
  kegs, and instructions into the orientation payload.
- KEG-specific instructions belong in each KEG's own config `instructions`
  field, not in flight cover rows.
