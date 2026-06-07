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

## 3. Alias Resolution

A selected alias is looked up in the `kegs` map and resolved to a concrete
target. Each `kegs` entry is a `(hub, namespace, name)` triple — an empty `hub`
or `namespace` is filled in by the chains below. A legacy `path` field is an
explicit local-filesystem escape hatch that takes precedence over the triple.

## 4. Hub Precedence

When a keg reference omits its `hub`, tapper picks one in this order, stopping at
the first match:

1. explicit `hub` on the reference
2. `defaultHub` (high-precedence slot — set in project config)
3. `fallbackHub` (last-resort slot — set in user config)
4. the sole configured hub (or the alphabetically-first when several exist)
5. the compiled-in `atlas` remote hub (`https://atlas.foldwise.ai`)

Setting `disableDefaultHub: true` (or `TAP_DISABLE_DEFAULT_HUB=1`) removes step
5: hub-dependent commands then fail with a clear error instead of silently
reaching the compiled-in default.

The reserved `local` namespace is a special case — a reference whose namespace
is `local` and whose hub is empty pins this machine's local (filesystem) hub
rather than walking the chain above.

## 5. Namespace Precedence

Once the hub is known, an omitted `namespace` is resolved in this order:

1. explicit `namespace` on the reference
2. the hub's own `namespace` (its default — a hub hosts many namespaces)
3. `defaultNamespace` (high-precedence slot — set in project config)
4. `fallbackNamespace` (last-resort slot — set in user config)
5. for a local hub, the reserved `local` namespace (addressed as `@local`); for
   a remote hub, an error (no namespace could be resolved)

Namespaces must be a single portable path segment (`[a-z0-9_-]+`, no dots or
slashes).

## 6. On-Disk Layout For Local Kegs

A local-hub keg resolves to a file target at:

```text
<basePath>/@<namespace>/<name>
```

The `@` sigil is part of the directory name. The reserved `@local` namespace
addresses this machine's local hub. Remote and read-only hubs resolve to
`<hub-url>/api/v1/kegs/@<namespace>/<name>` instead.

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
turns the warning into a hard error. Project configs may still set `kegMap`,
`kegs`, and the `default*` / `fallback*` selectors.

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
