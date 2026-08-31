# Flights

A **flight** is the required authorization and instruction context for an MCP
session. Flight manifests live separately from Tapper configuration.
`tap bootstrap` can persist a machine-wide baseline in the user config, while a
project can persist a more specific selection in `.tapper/config.yaml`. The
server pins the root reference during MCP initialization, then resolves its
live graph and the requested flight before every authority-bearing tool call.

## What A Flight Does

A flight carries five details:

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
   while normal Hub authorization still applies. It never raises the
   identity's actual KEG role. `manage_flights` exposes flight mutation tools
   to the session, but Hub still requires the authenticated identity to own or
   administer the target namespace. `manage_kegs` exposes `keg_create`, and
   Hub still requires the identity to belong to the target namespace. The
   capabilities are independent.
5. **Ordered direct child entries** (`subflights`). Each flight may list up to
   64 canonical children. Runtime flattening is ordered breadth-first, emits a
   shared descendant once, tolerates cycles by deduplicating already loaded and
   expanded flights, and retains the deterministic shortest selection path.
   There is no depth-eight rule. A pinned root may expose at most 256 unique
   accessible descendants at runtime; exceeding that cap refuses the call.
   A selected descendant supplies only its own instructions, capabilities, and
   cover and may be broader or different from its ancestors. Cross-Hub
   relations and duplicate canonical children are rejected; referenced
   children cannot be deleted.

Because a flight is not a KEG target selector, `tap mcp --flight` binds only
the process flight identity. `tap mcp --keg` remains an independent default for
subsequent KEG operations. `tap orient` is flight-scoped and rejects
`--keg`, `--namespace`, and `--hub`.

## Manifest Format

Flights are stored by Tapper Hub and addressed as `@namespace/+slug`. Each
manifest has six optional fields:

```yaml
title: Release 42 cut
visibility: private
capabilities:
  - full_access
  - manage_flights
subflights:
  - "@acme/+release-notes"
  - "@acme/+verification"
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

Older Hub manifests that use `allowedKegs` still load; each bare entry is
treated as an `editor` cover row for wire compatibility, while an entry
with an explicit `=viewer` suffix keeps its viewer cap.

`tap flight create/edit/delete` manage Hub-backed flights exclusively.

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
`visibility`, `capabilities`, `subflights`, `cover`, and `instructions`; comments and the
modeline are ignored when deciding whether the manifest changed.

MCP always exposes `list_flights` and `flight_show`. In an active session it
also keeps `flight_create`, `flight_edit`, `flight_delete`, and `keg_create`
visible because a selectable descendant may grant their capability even when
the root does not. Dispatch checks `manage_flights` or `manage_kegs` against
the flight selected for that call, then applies normal Hub authorization.
`flight_edit` is a partial update where omitted fields retain their current
values. Call `flight_show` first and pass its manifest hash as the required
`expected_hash` for both edits and deletes. On conflict, merge or refetch and
retry with the returned current hash. Graph and authority edits are adopted on
the next call automatically. Mutations are never replayed. A referenced
subflight cannot be deleted.

Flight mutations always use normal Hub authorization in addition to the
selected flight capability.

## Behavior

- MCP tools reject a keg outside the call-selected flight's cover
  with a "keg … is not available in flight …" error.
- MCP writes against a `viewer` cover row are rejected as viewer-only.
- A fresh selection, cover, capability, or role refusal reports
  `ORIENTATION_DENIED`, `reorientRequired=false`, and
  `operationPerformed=false`. A change racing between call resolution and Hub
  validation reports `ORIENTATION_STALE`; transient graph or identity failures report
  `ORIENTATION_UNAVAILABLE`; permanent loss of the connection-pinned root reports
  `ORIENTATION_ROOT_UNAVAILABLE` and requires a new session.
- `keg_settings_edit` replaces the complete validated KEG YAML document and
  requires an `admin` cover (or `full_access`) plus editor/admin identity access
  to that KEG. Read the full document with `keg_settings` and pass its hash as
  the required `expected_hash`; merge or refetch after conflicts and retry with
  the returned current hash. An admin flight cap never creates a Hub admin identity.
- `full_access` permits admin-class flight operations outside the cover, but
  does not bypass normal identity authorization or implicitly grant
  `manage_flights`.
- Without a selected flight, MCP publishes the complete tool inventory and bare
  calls use normal identity-authorized full access. Every accessible KEG appears
  at the caller's real role; this never raises Hub ACLs or namespace membership.
  An explicit `flight` selects any listed identity-accessible real flight for
  that call and uses only its cover, capabilities, and instructions.
- No-flight authority is pinned for the connection lifetime. Creating a KEG or
  flight does not replace it, and `session_refresh` returns `already_active`
  with `nextAction:"new_session"`. Create a least-privilege flight, pin it
  outside MCP, and start a new connection to narrow access. Newly created
  flights are immediately available for explicit call-local selection.
- Recovery-only mode applies only when an explicitly configured root is
  missing, inaccessible, invalid, or unavailable. Seeing only `orient`,
  `session_refresh`, `list_flights`, `flight_show`, `auth_info`, and
  `keg_search` means configured authority failed to initialize. Repair the
  configured root outside MCP, then call `session_refresh` and `orient`.
- Every MCP connection pins either no-flight authority or one real root at
  initialization. Configuration and account-preference changes cannot change
  that state mid-session. Every
  authority-bearing tool accepts an optional top-level `flight`; omission uses
  identity authority in no-flight state or the real root otherwise. From
  no-flight state, an explicit value may name any listed real flight; from a
  real root, it may name only that root or an accessible transitive descendant.
  The selected flight's authority is never inherited or combined.
- Graph, cover, capability, role, relation, and identity changes are loaded on
  the next call without an explicit refresh. A transient load failure refuses
  that call with `ORIENTATION_UNAVAILABLE`; it never falls back to cached
  authority. A remote resolution obtains the accessible manifests from one
  fresh `GET /api/v1/flights` response and performs canonical lookup and
  bounded flattening locally.
- Hosted `/mcp` uses the account-wide MCP flight preference only at connection
  initialization. Stdio initialization selects explicit `--flight`, then
  `TAP_FLIGHT`, then the nearest project config, and finally the user baseline.
- Hosted deletion of the launch root clears the account preference through the
  flight foreign key, but cannot replace the root of an existing connection.
  That connection reports `ORIENTATION_ROOT_UNAVAILABLE`; a new launch is
  required. A local config that still names a deleted flight remains an
  external stale reference until configuration is changed outside MCP.
- Each in-flight call uses its own immutable orientation context. Concurrent
  root, child, sibling, and grandchild calls cannot change one another's
  selection. MCP resources have no `flight` parameter and use the pinned root.
- Direct CLI commands such as `tap cat`, `tap edit`, and `tap create` ignore
  flight cover caps; access is governed by normal keg authorization.
- `tap orient --flight @namespace/+slug` injects the flight's title, available
  kegs, and instructions into the orientation payload.
- `tap use --flight @namespace/+slug` persists the project default in
  `.tapper/config.yaml`; `tap use +slug` uses the resolved default namespace.
  A connection that started with no flight remains fully authorized until it
  ends; the new selection takes effect only on a new connection.
- Flight selection precedence is explicit runtime `--flight`, then
  `TAP_FLIGHT`, then the nearest project config, then the user baseline written
  by `tap bootstrap`. Project selection
  therefore overrides the machine-wide bootstrap choice without changing it.
- `tap launch --agent NAME` uses the agent only for model selection and
  telemetry. Launch requires a Hub-backed root and exports its canonical
  reference once as `TAP_FLIGHT`. Legacy `agents[NAME].flight`
  values are ignored.
- MCP tools have no model-visible root-switch input. Their optional `flight`
  makes only a call-local selection. To change the connection's default
  authority, the user starts a new session after changing configuration;
  there is no hidden root-switch tool.
- KEG-specific instructions belong in each KEG's own config `instructions`
  field, not in flight cover rows.
- Tapper user/project configuration and hosted flight selection remain
  human-controlled. MCP exposes no Tapper config mutation or flight-switch
  tool.
