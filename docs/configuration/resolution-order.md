# Resolution Order

This page describes how tapper chooses a keg target, how it resolves the hub
and namespace a keg lives on, and how the config layers cascade.

## 1. Explicit Target Flags Win First

If you pass explicit flags, they take precedence:

- `--keg` selects a keg by bare name or `@namespace/name`
- `--namespace` resolves a bare `--keg` in a specific namespace
- `--hub` overrides namespace-to-hub resolution
- `--config` bypasses the user/project config cascade

`--flight` is not a keg selector. It is flight context for orient/MCP: agent
instructions plus cover caps enforced by the MCP surface. Direct CLI commands
still use normal keg authorization and do not have access reduced by the flight.
Its precedence at session initialization is explicit `--flight`, `TAP_FLIGHT`,
the nearest project `flight`, then the user baseline optionally written by
`tap bootstrap`. `TAP_AGENT` never selects a flight. The resulting root
reference cannot change within that MCP connection. See [Flights](flights.md).

Filesystem paths, `file://` targets, and the removed local-creation flags are
unsupported. `tap keg create` always calls a configured Hub.

## 2. No Explicit Keg Flow

When no explicit target is supplied, tapper resolves the keg reference in this
order:

1. `defaultKeg`
2. `kegMap` match (`pathRegex` first, then longest `pathPrefix`)
3. `fallbackKeg`

## 3. Namespace-centric model

Resolution flows **keg name -> namespace -> Hub**. A keg is identified
by `@<namespace>/<name>`; the namespace determines which hub hosts it. A keg
selector (`defaultKeg`, `fallbackKeg`, `--keg`, a `kegMap` alias) is a keg
reference — a bare name, `@namespace/name`, or `keg:@namespace/name` —
there is no `kegs` alias map. One config map disambiguates the namespace→hub hop:

- **`namespaces`** maps a namespace to the hub that hosts it — the conflict
  resolver for a namespace that could live on more than one hub. The scalar
  shorthand `myns: atlas` is accepted and normalized to `myns: {hub: atlas}`.

## 4. Namespace Precedence

An omitted `namespace` is resolved **first**, in this order:

1. explicit `namespace` on the reference
2. `defaultNamespace` (high-precedence slot — set in project config)
3. `fallbackNamespace` (last-resort slot — set in user config)
4. once the Hub is known: the Hub's own namespace default; if nothing resolves,
   the reference is an error

Namespaces must be a single portable path segment (`[a-z0-9_-]+`, no dots or
slashes).

## 5. Hub Precedence

The hosting hub is resolved **from the namespace**, in this order:

1. explicit `hub` on the reference
2. `namespaces[ns].hub` (the namespace → hub map)
3. `defaultHub` (high-precedence slot — set in project config)
4. `fallbackHub` (last-resort slot — set in user config)
5. the sole configured Hub (or the alphabetically-first when several exist)
6. the compiled-in `atlas` remote Hub (`https://atlas.foldwise.ai`)

Setting `disableAtlasHub: true` (or `TAP_DISABLE_ATLAS_HUB=1`) removes step
6: Hub-dependent commands then fail with a clear error instead of silently
reaching the compiled-in default.

## 6. KEG references and Hub routes

A keg reference is the `keg` scheme — `keg:@<namespace>/<name>` (the namespace is
optional: `keg:<name>`). The hub is **not** part of the reference; it is
resolved from the namespace via the chains above. A node within a keg appends
the node id: `keg:@<namespace>/<name>/<nodeID>`.

Remote and read-only Hubs resolve to
`<hub-url>/api/v1/@<namespace>/kegs/<name>` (namespace first; only the
namespace segment carries the `@` sigil — keg aliases are bare in the
tapper-hub route layout).

`@local` is not reserved. It behaves like any other namespace if a remote Hub
hosts it.

## 7. Config Cascade

The effective config is assembled from several layers, most specific winning:

| Rank | Source                          | Discovery                                       |
| ---- | ------------------------------- | ----------------------------------------------- |
| top  | CLI flags (`--log-level`, etc.) | Cobra `cmd.Flags().Changed()`                   |
| ↑    | Env vars (`TAP_*`)              | `rt.Env().Get()` prefix scan                    |
| ↑    | Project configs (deepest → …)   | every `.tapper/config.yaml` from cwd up to `/`  |
| base | User config                     | `~/.config/tapper/config.yaml`                  |

The project layer is itself a walk: starting at the workspace root, tapper
collects **every** `.tapper/config.yaml` up to the filesystem root and merges
them so a deeper directory overrides a shallower one. A repository nested inside
another repository therefore inherits — and can override — the outer config.

### Trust boundary

Only the **user** config may define `hubs{}` and the `token` / `tokenEnv`
credentials. Those fields are stripped from any walked project config so a
repository you `cd` into cannot introduce a hub target or harvest a token
environment variable. Each strip is recorded as a load warning; `--strict`
turns the warning into a hard error. Project configs may still set `kegMap` and
the `default*` / `fallback*` selectors.

## 8. Worked Examples

- In a repo with `.tapper/config.yaml` containing `defaultKeg: tapper`,
  `tap info` resolves `tapper` first.
- If `defaultKeg` is empty and `kegMap` matches the current path to alias
  `work`, `tap info` resolves `work`.
- A reference `{name: notes}` with no Hub and no namespace, under a user config
  whose `fallbackHub` has `defaultNamespace: acme`, resolves to
  `keg:@acme/notes` on that Hub.
- A project config that sets `defaultNamespace: acme` makes the same reference
  resolve under `@acme` instead, overriding the user-level fallback.
