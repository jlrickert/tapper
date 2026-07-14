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
writes the fallback hub and the built-in local hub for you (see below).

> **First run requires `tap bootstrap`.** On the full `tap` surface,
> hub/namespace-dependent commands (`tap keg create <name>`, `tap cat`,
> `tap list`, …) refuse with a clear error until this user config exists — they
> no longer silently create or resolve a keg in a hidden platform directory.
> Explicit local destinations (`tap keg create --project` / `--path`) still work
> without setup, as does the pruned `keg` binary.

## Key Reference

- `fallbackKeg`: last-resort keg reference when no default/map match resolves
- `defaultKeg`: optional keg reference used first when no keg flag is provided.
  A reference is a bare name, `@namespace/name`, `keg:@namespace/name`, or a path
  — resolved through the namespace-centric chain (there is no `kegs` alias map).
- `namespaces`: map of namespace → hosting hub
  (`namespaces[ns].hub`, or the scalar shorthand `ns: hub`). Role: disambiguate
  which **hub** a namespace lives on when it could exist on more than one. An
  entry wins over `defaultHub`/`fallbackHub` during namespace→hub resolution.
- `kegMap`: path-based alias mapping (`pathRegex` first, then longest
  `pathPrefix`). Picks the active flight and default keg from the working dir.
- `fallbackHub`: last-resort hub name used when a reference omits its hub and no
  `defaultHub`/`namespaces[ns]` applies. The user-config convention — set it
  here so references need not specify a hub.
- `fallbackNamespace`: last-resort namespace used when a reference omits its
  namespace and no `defaultNamespace`/per-hub namespace applies.
- `defaultHub` / `defaultNamespace`: high-precedence slots. Usually set in
  project config rather than here; they make `tap keg create example`
  equivalent to `@<defaultNamespace>/example`.
- `disableAtlasHub` / `disableLocalHub`: when `true`, suppress the synthesized
  built-in atlas / local hub. A disabled built-in is not synthesized, is omitted
  from hub listings, and is skipped in resolution; an explicit `hubs` entry of
  the same name is unaffected. `disableAtlasHub` is useful for SOC2-audited
  deployments that must prove no implicit network targets exist.
- `hubs`: name-keyed map of hub definitions (`kind`, `defaultNamespace`, `url`,
  `basePath`, `token`/`tokenEnv`). **User config only** — see the trust boundary
  below.

> Note: `kegSearchPaths` is not a recognized key. A config that carries it is
> parsed but the key is ignored (dropped on the next re-serialize). There is no
> `TAP_KEG_SEARCH_PATHS` env var.

## Hubs

Hubs are a name-keyed map. Each entry's `defaultNamespace` field is that hub's
**default** namespace — a hub hosts many namespaces; this is only the one used
when a reference resolved against the hub omits its own. Two built-ins are
synthesized when not configured explicitly (and not disabled via
`disableAtlasHub` / `disableLocalHub`):

- `local` — the built-in filesystem hub (kind `local`)
- `atlas` — the compiled-in default remote hub (`https://atlas.foldwise.ai`)

An explicit entry always overrides the synthesized built-in.

```yaml
hubs:
  # the machine's local filesystem hub, keyed by hostname (written by `tap bootstrap`)
  my-laptop:
    kind: local
    defaultNamespace: local    # the reserved @local namespace
    basePath: ~/Documents/kegs
  atlas:
    kind: remote
    defaultNamespace: me
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
`defaultNamespace: local` (the reserved `@local`), plus the remote hub for
cloud/enterprise. It writes the **fallback** hub (`fallbackHub`), not the
default slot — the project config owns the high-precedence `default*` slots.

It does **not** write a global `fallbackNamespace` or a per-user `namespaces`
entry. The preferred namespace comes from the resolved hub's own
`defaultNamespace` field: `@local` for the local hub, and your home namespace
for cloud/enterprise
(left empty until `tap auth login` adopts it from the hub's whoami probe). The
only `namespaces` entry written is `local → <local hub>`, pinning `@local` to
this machine.

It is idempotent: re-running only touches the fallback hub, the local namespace
mapping, and the kind's hub entry, leaving your `kegMap` and any
`fallbackNamespace` you set by hand untouched. It also asks for a default keg and
records it as `fallbackKeg` (the global-user slot) so plain `tap` commands
resolve one after setup, while a project's `defaultKeg` or a `kegMap` rule can
still override it.

Interactive bootstrap also discovers flights from only the selected hub and
offers to store one as the user-level `flight` baseline. Existing baselines are
preselected when available, and **Skip for now** leaves the current value
unchanged. For scripts, pass the inherited global flag explicitly, for example
`tap bootstrap --kind local --flight @local/+focused`; bootstrap validates the
flight and stores its canonical `@namespace/+slug` reference. If no baseline is
set, MCP starts in recovery-only mode. A project `flight`, `TAP_FLIGHT`, or an
explicit `--flight` on a later command overrides the bootstrap baseline.

## Hub Resolution Chain

When a keg reference omits its hub, tapper resolves the target hub in this
order, stopping at the first match:

1. explicit `hub` on the reference (or `--hub` flag for `tap auth login`)
2. `defaultHub: NAME` → look up `NAME` in `hubs`
3. `fallbackHub: NAME` → look up `NAME` in `hubs`
4. the sole configured hub (or the alphabetically-first when several exist)
5. `disableAtlasHub: true` (or `TAP_DISABLE_ATLAS_HUB=1`) → error
6. the compiled-in `atlas` hub (`https://atlas.foldwise.ai`)

## Recommended Baseline Config

```yaml
fallbackHub: my-laptop
fallbackNamespace: local
fallbackKeg: pub
kegMap:
  - alias: pub
    pathPrefix: ~/repos/github.com
namespaces:
  # which hub hosts each namespace (the namespace→hub conflict resolver)
  local: my-laptop          # scalar shorthand for {hub: my-laptop}
hubs:
  my-laptop:
    kind: local
    defaultNamespace: local
    basePath: ~/Documents/kegs
```

Here `fallbackKeg: pub` and the `kegMap` alias `pub` are both keg references —
bare name `pub`, resolved via `fallbackNamespace: local` to `@local/pub` at
`~/Documents/kegs/@local/pub`.

## Common Mistakes

- Unresolvable reference: `defaultKeg`, `fallbackKeg`, or `kegMap.alias` is a
  bare name with no `defaultNamespace`/`fallbackNamespace`, or names a keg that
  does not exist on the resolved hub.
- No namespace resolvable: a remote-hub reference with no explicit, per-hub,
  default, or fallback namespace errors out. Local-hub references fall back to
  `@local`.
- Missing fallback: no `defaultKeg` plus no `fallbackKeg` can produce
  `no keg configured`.
