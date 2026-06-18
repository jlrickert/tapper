# Project Config

Project config defines repository-specific defaults. It layers on top of the
user config and owns the high-precedence `default*` slots.

## Purpose And File Location

- File: `.tapper/config.yaml`
- Scope: current repository (and any nested directory beneath it)

## View And Edit

```bash
tap config --project
tap config edit --project
tap config template project
cat config.yaml | tap config edit --project
```

## Multi-Level Walk

Project config is not a single file. Starting at the workspace root, tapper
walks **up to the filesystem root** collecting every `.tapper/config.yaml` it
finds, then merges them so a **deeper directory overrides a shallower one**. A
repository nested inside another therefore inherits the outer config and can
override individual keys. The merged project layer then sits above the user
config in the cascade. For overlapping keys, project values win over user
values.

Typical usage:

- set `defaultKeg` so commands in this repo resolve to the intended team or
  project keg
- set `defaultHub` / `defaultNamespace` to pin this project's hub and namespace
- keep machine-wide fallback behavior (`fallbackHub`, `fallbackNamespace`,
  `hubs`) in user config

## What A Project Config May / May Not Set

| Allowed in project config | Forbidden (user config only) |
| ------------------------- | ---------------------------- |
| `defaultKeg`, `fallbackKeg` | `hubs{}`                    |
| `defaultHub`, `defaultNamespace` | `token` / `tokenEnv`   |
| `fallbackHub`, `fallbackNamespace` |                      |
| `kegMap`                  |                              |

**Trust boundary:** `hubs{}` and the `token` / `tokenEnv` credentials are
stripped from any walked project config (recorded as a load warning; `--strict`
makes it a hard error) so a repository you `cd` into cannot introduce a hub
target or harvest a token environment variable. See
[Resolution Order](resolution-order.md#trust-boundary).

> Note: `kegSearchPaths` is not a recognized key and is silently ignored if
> present.

## Team Setup Pattern

- Commit `.tapper/config.yaml` with the repository's shared `defaultKeg` and,
  when needed, `defaultNamespace`.
- Prefer a shared hub keg for team memory. Use a local project keg only when the
  knowledge should live with the repository.
- Use user config for personal/global hubs, credentials, and fallbacks.

## Minimal Project Config Example

```yaml
defaultKeg: engineering
defaultNamespace: acme
kegMap: []
```

`defaultKeg: engineering` is a keg reference: the bare name `engineering`
resolves its namespace from `defaultNamespace: acme`, then that namespace routes
to the configured hub.
