# Resolution Order

This page describes how tapper chooses a keg target, how it resolves the hub
and namespace a keg lives on, and how the config layers cascade.

## 1. Explicit Target Flags Win First

If you pass explicit flags, they take precedence:

- `--path` resolves a keg from a specific filesystem path
- `--project` resolves from project-local locations
- `--cwd` resolves from the keg in the current working directory
- `--keg` resolves an alias

These flags are mutually exclusive. `--flight` is **not** one of them — a flight
is an overlay (a keg restriction plus agent instructions) that composes with the
selector above. See [Flights](flights.md).

## 2. No Explicit Keg Flow

When no explicit target is supplied, tapper resolves the alias in this order:

1. `defaultKeg`
2. `kegMap` match (`pathRegex` first, then longest `pathPrefix`)
3. `fallbackKeg`

## 3. Namespace-centric model

Resolution flows **keg name → namespace → hub → backend**. A keg is identified
by `@<namespace>/<name>`; the namespace determines which hub hosts it. A keg
selector (`defaultKeg`, `fallbackKeg`, `--keg`, a `kegMap` alias) is a keg
reference — a bare name, `@namespace/name`, `keg:@namespace/name`, or a path —
there is no `kegs` alias map. One config map disambiguates the namespace→hub hop:

- **`namespaces`** maps a namespace to the hub that hosts it — the conflict
  resolver for a namespace that could live on more than one hub. The scalar
  shorthand `myns: atlas` is accepted and normalized to `myns: {hub: atlas}`.

## 4. Namespace Precedence

An omitted `namespace` is resolved **first**, in this order:

1. explicit `namespace` on the reference
2. `defaultNamespace` (high-precedence slot — set in project config)
3. `fallbackNamespace` (last-resort slot — set in user config)
4. once the hub is known: the hub's own `namespace` default, then the reserved
   `local` namespace for a local hub; a remote hub with nothing resolved is an
   error

Namespaces must be a single portable path segment (`[a-z0-9_-]+`, no dots or
slashes).

## 5. Hub Precedence

The hosting hub is resolved **from the namespace**, in this order:

1. explicit `hub` on the reference
2. `namespaces[ns].hub` (the namespace → hub map)
3. the reserved `local` namespace pins this machine's local (filesystem) hub
4. `defaultHub` (high-precedence slot — set in project config)
5. `fallbackHub` (last-resort slot — set in user config)
6. the sole configured hub (or the alphabetically-first when several exist)
7. the compiled-in `atlas` remote hub (`https://atlas.foldwise.ai`)

Setting `disableDefaultHub: true` (or `TAP_DISABLE_DEFAULT_HUB=1`) removes step
7: hub-dependent commands then fail with a clear error instead of silently
reaching the compiled-in default.

## 6. Keg references and on-disk layout

A keg reference is the `keg` scheme — `keg:@<namespace>/<name>` (the namespace is
optional: `keg:<name>`). The hub is **not** part of the reference; it is
resolved from the namespace via the chains above. A node within a keg appends
the node id: `keg:@<namespace>/<name>/<nodeID>`.

A local-hub keg resolves to a file target on disk at:

```text
<basePath>/@<namespace>/<name>
```

The `@` sigil is part of the directory name. The reserved `@local` namespace
addresses this machine's local hub. Remote and read-only hubs resolve to
`<hub-url>/api/v1/@<namespace>/kegs/@<name>` instead (namespace first, both
segments carry the `@` sigil — matching the tapper-hub route layout).

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
- A reference `{name: notes}` with no hub and no namespace, under a user config
  whose `fallbackHub` points at a local hub with `namespace: local`, resolves to
  `<basePath>/@local/notes`.
- A project config that sets `defaultNamespace: acme` makes the same reference
  resolve under `@acme` instead, overriding the user-level fallback.
