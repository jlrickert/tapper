# User Config

User config defines machine-wide defaults for tapper. It is the **base** layer
of the config cascade and the only layer permitted to define hubs and
credentials.

## Purpose And File Location

- File: `~/.config/tapper/config.yaml`
- Scope: your current user account

## View And Edit

```bash
tap config --user
tap config edit --user
tap config template user
cat config.yaml | tap config edit --user
```

The fastest way to create a sensible starting config is `tap bootstrap`, which
writes the fallback hub + namespace and the built-in local hub for you (see
below).

## Key Reference

- `fallbackKeg`: last-resort alias when no default/map match resolves
- `defaultKeg`: optional alias used first when no keg flag is provided
- `kegs`: explicit map of alias → `(hub, namespace, name)` keg reference
- `kegMap`: path-based alias mapping (`pathRegex` first, then longest `pathPrefix`)
- `fallbackHub`: last-resort hub name used when a reference omits its hub and no
  `defaultHub` applies. The user-config convention — set it here so references
  need not specify a hub.
- `fallbackNamespace`: last-resort namespace used when a reference omits its
  namespace and no `defaultNamespace`/per-hub namespace applies.
- `defaultHub` / `defaultNamespace`: high-precedence slots. Usually set in
  project config rather than here; they make `tap init example` equivalent to
  `@<defaultNamespace>/example`.
- `disableDefaultHub`: when `true`, suppress the compiled-in `DefaultHubURL`
  fallback (`https://atlas.foldwise.ai`) — hub-dependent commands fail with a
  clear error if no other hub is configured. Useful for SOC2-audited
  deployments that need to prove no implicit network targets exist.
- `hubs`: name-keyed map of hub definitions (`kind`, `namespace`, `url`,
  `basePath`, `token`/`tokenEnv`). **User config only** — see the trust boundary
  below.

> Note: `kegSearchPaths` has been removed. A legacy config that still carries it
> is parsed but ignored (dropped on the next re-serialize). There is no
> `TAP_KEG_SEARCH_PATHS` env var.

## Hubs

Hubs are a name-keyed map. Each entry's `namespace` field is that hub's
**default** namespace — a hub hosts many namespaces; this is only the one used
when a reference resolved against the hub omits its own. Two built-ins are
synthesized when not configured explicitly:

- `local` — the built-in filesystem hub (kind `local`)
- `atlas` — the compiled-in default remote hub (`https://atlas.foldwise.ai`)

An explicit entry always overrides the synthesized built-in.

```yaml
hubs:
  # the machine's local filesystem hub, keyed by hostname (written by `tap bootstrap`)
  my-laptop:
    kind: local
    namespace: local           # the reserved @local namespace
    basePath: ~/Documents/kegs
  atlas:
    kind: remote
    namespace: me
    url: https://atlas.foldwise.ai
    tokenEnv: ATLAS_API_KEY
```

A local-hub keg lives on disk at `<basePath>/@<namespace>/<name>` — the `@`
sigil is part of the directory name. The reserved `@local` namespace addresses
this machine's local hub.

### Trust boundary

`hubs{}` and `token`/`tokenEnv` may only be set in this user config. They are
stripped from any walked **project** `.tapper/config.yaml` (with a load warning;
`--strict` makes it a hard error) so a repository you `cd` into cannot introduce
a hub target or harvest a token. See
[Resolution Order](resolution-order.md#trust-boundary).

## `tap bootstrap`

`tap bootstrap` materializes or refreshes this user config around a deployment
kind:

- `local` — only the built-in local filesystem hub
- `cloud` (default) — the compiled-in `atlas` remote hub
- `enterprise --endpoint <url>` — a user-supplied remote HTTP hub

It always writes a local hub **keyed by the machine hostname** with
`namespace: local` (the reserved `@local`), plus the remote hub for
cloud/enterprise. It writes the **fallback** slots (`fallbackHub` /
`fallbackNamespace`), not the default slots — the project config owns the
high-precedence `default*` slots. It is idempotent: re-running only touches the
fallback slots and the kind's hub entry, leaving your `kegs`/`kegMap` untouched.

## Hub Resolution Chain

When a keg reference omits its hub, tapper resolves the target hub in this
order, stopping at the first match:

1. explicit `hub` on the reference (or `--hub` flag for `tap auth login`)
2. `defaultHub: NAME` → look up `NAME` in `hubs`
3. `fallbackHub: NAME` → look up `NAME` in `hubs`
4. the sole configured hub (or the alphabetically-first when several exist)
5. `disableDefaultHub: true` (or `TAP_DISABLE_DEFAULT_HUB=1`) → error
6. the compiled-in `atlas` hub (`https://atlas.foldwise.ai`)

## Recommended Baseline Config

```yaml
fallbackHub: my-laptop
fallbackNamespace: local
fallbackKeg: pub
kegMap:
  - alias: pub
    pathPrefix: ~/repos/github.com
kegs:
  pub:
    namespace: local
    name: public
hubs:
  my-laptop:
    kind: local
    namespace: local
    basePath: ~/Documents/kegs
```

## Common Mistakes

- Alias mismatch: `defaultKeg`, `fallbackKeg`, or `kegMap.alias` points to an
  alias that does not exist in `kegs`.
- No namespace resolvable: a remote-hub reference with no explicit, per-hub,
  default, or fallback namespace errors out. Local-hub references fall back to
  `@local`.
- Missing fallback: no `defaultKeg` plus no `fallbackKeg` can produce
  `no keg configured`.
