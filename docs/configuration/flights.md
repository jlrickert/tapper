# Flights

A **flight** is the required authorization and instruction context for an MCP
session. Flight manifests live separately from Tapper configuration.
`tap bootstrap` can persist a machine-wide baseline in the user config, while a
project can persist a more specific selection in `.tapper/config.yaml`. The
server resolves fresh orientation during MCP initialization and again whenever
the client explicitly calls `orient`.

## What A Flight Does

A flight carries four details:

1. **A keg cover** (`cover`). MCP tools reject kegs outside the cover. Each
   cover entry has a `viewer`, `editor`, or `admin` cap. Reads require
   `viewer`, node/schema writes require `editor`, and the agent-facing
   `keg_settings_edit` operation requires `admin`. An empty cover denies all
   KEG access. Direct CLI commands ignore these caps and use normal keg
   authorization.
2. **Agent instructions** (`instructions`). These are injected into the `tap
   orient` payload, so an agent that orients under a flight sees the flight's
   guidance before general Tapper guidance.
3. **Visibility** (`visibility`). Hub flights default to `private`; `public`
   flights are anonymously discoverable and may cover only public kegs when
   created or updated.
4. **Capabilities** (`capabilities`). `full_access` supplies admin-class flight
   authority across every KEG the authenticated identity can already access,
   while normal local and Hub authorization still applies. It never raises the
   identity's actual KEG role. `manage_flights` exposes flight mutation tools
   to the session, but Hub still requires the authenticated identity to own or
   administer the target namespace. The capabilities are independent.

Because a flight is not a KEG target selector, `tap mcp --flight` binds only
the process flight identity. `tap mcp --keg` remains an independent default for
subsequent KEG operations. `tap orient` is flight-scoped and rejects
`--keg`, `--namespace`, and `--hub`.

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
    role: admin
  - namespace: acme
    keg: engineering
    role: viewer
instructions: |
  Only touch the release notes and engineering kegs.
  Snapshot every node before editing.
```

A cover entry without an explicit `role` defaults to `viewer` — the same
default applies to `--cover` specs on the CLI and MCP surfaces; `editor` and
`admin` must be requested explicitly. Unknown roles are rejected.

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
well. `flight_edit` is a partial update where omitted fields retain their
current values. A Hub-backed active flight may edit or delete itself:

- a successful self-edit immediately adopts the exact returned manifest,
  cover, instructions, and capabilities before the response is released;
- removing `manage_flights` therefore removes the mutation tools immediately;
- a successful self-delete immediately enters recovery-only mode;
- editing or deleting another flight does not change current session authority.

Local `flights.d` manifests remain MCP read-only. Flight mutations always use
normal Hub authorization in addition to the active flight capability.

## Behavior

- MCP tools reject a keg outside the active flight's cover
  with a "keg … is not available in flight …" error.
- MCP writes against a `viewer` cover row are rejected as viewer-only.
- `keg_settings_edit` replaces the complete validated KEG YAML document and
  requires an `admin` cover (or `full_access`) plus editor/admin identity access
  to that KEG. An admin flight cap never creates a Hub admin identity.
- `full_access` permits admin-class flight operations outside the cover, but
  does not bypass normal identity authorization or implicitly grant
  `manage_flights`.
- Without a selected flight, MCP starts in recovery-only mode and lists only
  `orient`, `list_flights`, `flight_show`, and credential-safe `auth_info`.
  After selecting a flight outside MCP, call `orient` on the same connection.
- Config-driven `tap mcp` reloads user, project, and environment configuration
  on every orientation. A successful orientation atomically replaces session
  authority; configuration changes alone do nothing.
- `tap mcp --flight REF` is launcher-bound: configuration cannot change its
  flight identity, while orientation still refreshes that flight's current
  manifest, cover, and instructions.
- A failed refresh preserves the last valid authority. An intentionally blank
  config selection clears authority and enters recovery mode.
- If a self-edit is persisted but exact orientation rendering fails, the tool
  reports that the update was applied and enters recovery instead of retaining
  stale authority.
- Hosted `/mcp` selects the account-wide MCP flight preference. Local
  initialization and `orient` select explicit `--flight`, then `TAP_FLIGHT`,
  the nearest project config, and finally the user baseline.
- Hosted self-deletion clears the account preference through the flight foreign
  key. A local config that still names a deleted flight remains a stale external
  reference: later `orient` reports it and the session stays in recovery until
  configuration is changed outside MCP.
- In-flight calls finish under the context captured when they began. Calls that
  start after orientation use the newly published context.
- Direct CLI commands such as `tap cat`, `tap edit`, and `tap create` ignore
  flight cover caps; access is governed by normal keg authorization.
- A missing `flights.d` directory means "no flights", not an error.
- `tap orient --flight @namespace/+slug` injects the flight's title, available
  kegs, and instructions into the orientation payload.
- `tap use --flight @namespace/+slug` persists the project default in
  `.tapper/config.yaml`; `tap use +slug` uses the resolved default namespace.
  Config-driven sessions adopt it on their next orientation.
- Flight selection precedence is explicit runtime `--flight`, then
  `TAP_FLIGHT`, then the nearest project config, then the user baseline written
  by `tap bootstrap`. Project selection therefore overrides the machine-wide
  bootstrap choice without changing it.
- MCP tools have no model-visible `flight` input. Humans change config-driven
  selection with `tap use --flight @namespace/+slug` (or `tap use +slug`), then
  the existing session calls `orient`. There is no hidden flight-switch tool.
- KEG-specific instructions belong in each KEG's own config `instructions`
  field, not in flight cover rows.
- Tapper user/project configuration and hosted flight selection remain
  human-controlled. MCP exposes no Tapper config mutation or flight-switch
  tool.
