# Flights

A **flight** is the required authorization and instruction context for a local
full MCP session. Flight manifests live separately from Tapper configuration.
`tap bootstrap` can persist a machine-wide baseline in the user config, while a
project can persist a more specific selection in `.tapper/config.yaml`. The
server resolves and pins the effective flight when the MCP connection starts.

## What A Flight Does

A flight carries four details:

1. **A keg cover** (`cover`). MCP tools reject kegs outside the cover. Each
   cover entry has a `viewer` or `editor` cap; writes require `editor`. An empty
   cover denies all KEG access. Direct CLI commands ignore these caps and use
   normal keg authorization.
2. **Agent instructions** (`instructions`). These are injected into the `tap
   orient` payload, so an agent that orients under a flight sees the flight's
   guidance before general Tapper guidance.
3. **Visibility** (`visibility`). Hub flights default to `private`; `public`
   flights are anonymously discoverable and may cover only public kegs when
   created or updated.
4. **Capabilities** (`capabilities`). `full_access` bypasses the flight cover
   and its viewer/editor caps for KEG operations, while normal local and Hub
   authorization still applies. `manage_flights` exposes flight mutation tools
   to the session, but Hub still requires the authenticated identity to own or
   administer the target namespace. The capabilities are independent.

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
manifest has five optional fields:

```yaml
# <basePath>/flights.d/release-42.yaml
title: Release 42 cut
visibility: private
capabilities:
  - full_access
  - manage_flights
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
| Start MCP with an explicit flight     | `tap mcp --flight @namespace/+slug`       |
| Preview orientation for a flight      | `tap orient --flight @namespace/+slug`    |
| Persist the project flight            | `tap use --flight @namespace/+slug` or `tap use +slug` |
| Persist the user bootstrap baseline   | `tap bootstrap --flight @namespace/+slug` |
| Create a Hub-backed flight            | `tap flight create @namespace/+slug --cover @namespace/keg=viewer` |
| Edit a Hub-backed flight in $EDITOR   | `tap flight edit @namespace/+slug` (the manifest opens as YAML; every save is applied) |
| Apply a manifest from a script        | `cat manifest.yaml \| tap flight edit @namespace/+slug` |
| Delete a Hub-backed flight            | `tap flight delete @namespace/+slug`      |

`tap flight edit` opens a non-empty YAML document even for an empty flight. The
first line is a `yaml-language-server` schema modeline for
`schemas/flight-manifest.json`, followed by a short comment that the
`@namespace/+slug` ref is immutable. The editable fields are `title`,
`visibility`, `capabilities`, `cover`, and `instructions`; comments and the
modeline are ignored when deciding whether the manifest changed.

MCP always exposes `list_flights` and `flight_show`. It exposes
`flight_create`, `flight_edit`, and `flight_delete` only while the session's
active flight grants `manage_flights`; direct calls are checked server-side as
well. A session can never edit or delete its own active flight. `flight_edit`
is a partial update where omitted fields retain their current values.

## Behavior

- MCP tools reject a keg outside the active flight's cover
  with a "keg … is not available in flight …" error.
- MCP writes against a `viewer` cover row are rejected as viewer-only.
- `full_access` permits KEG reads and writes outside the cover, but does not
  bypass normal identity authorization or implicitly grant `manage_flights`.
- Without a selected flight, the local MCP server starts in recovery-only mode
  and lists only `orient`, `list_flights`, `flight_show`, `auth_status`, and
  `config`. KEG tools remain locked until a human switches the live Claude
  session or the host reconnects after `tap use --flight @namespace/+slug`.
- A selected flight that is missing or invalid still fails MCP startup.
- The active flight's cover, instructions, capabilities, and normalized
  manifest hash are pinned per connection. Project config changes do not
  change an existing connection.
- If that manifest hash changes, the connection is invalidated
  for KEG and flight mutations until a human switches it or the host reconnects.
- Direct CLI commands such as `tap cat`, `tap edit`, and `tap create` ignore
  flight cover caps; access is governed by normal keg authorization.
- A missing `flights.d` directory means "no flights", not an error.
- `tap orient --flight @namespace/+slug` injects the flight's title, available
  kegs, and instructions into the orientation payload.
- `tap use --flight @namespace/+slug` persists the project default in
  `.tapper/config.yaml`; `tap use +slug` uses the resolved default namespace.
  Newly opened Codex or Claude sessions inherit it.
- Flight selection precedence is explicit runtime `--flight`, then
  `TAP_FLIGHT`, then the nearest project config, then the user baseline written
  by `tap bootstrap`. Project selection therefore overrides the machine-wide
  bootstrap choice without changing it.
- MCP tools have no model-visible `flight` input. In Claude Code, a human may
  run `/tapper:tapper-flight-switch @namespace/+slug`; Claude asks for
  confirmation and changes only that MCP connection without a model turn or a
  config write. Codex users run `tap use --flight @namespace/+slug` (or `tap use
  +slug`) and open a new thread so the plugin reconnects.
- KEG-specific instructions belong in each KEG's own config `instructions`
  field, not in flight cover rows.
