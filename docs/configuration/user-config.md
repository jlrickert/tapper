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
writes a cloud or enterprise fallback Hub (see below).

> **First run requires `tap bootstrap`.** On the full `tap` surface,
> hub/namespace-dependent commands (`tap keg create <name>`, `tap cat`,
> `tap list`, …) refuse with a clear error until this user config exists — they
> no longer silently create or resolve a KEG on local storage.

## Key Reference

- `fallbackKeg`: last-resort keg reference when no default/map match resolves
- `defaultKeg`: optional keg reference used first when no keg flag is provided.
  A reference is a bare name, `@namespace/name`, or `keg:@namespace/name`
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
- `disableAtlasHub`: when `true`, suppress the synthesized built-in atlas Hub.
  A disabled built-in is not synthesized, is omitted
  from hub listings, and is skipped in resolution; an explicit `hubs` entry of
  the same name is unaffected. `disableAtlasHub` is useful for SOC2-audited
  deployments that must prove no implicit network targets exist.
- `disableTelemetry`: when `true`, disables privacy-minimized CLI and MCP
  invocation reporting. `TAP_DISABLE_TELEMETRY=1` is the environment opt-out.
- `hubs`: name-keyed map of Hub definitions (`kind`, `defaultNamespace`, `url`,
  `token`/`tokenEnv`). **User config only** — see the trust boundary
  below.

Tapper configuration is extensible. Unknown top-level fields and unknown fields
inside hubs, namespaces, agents, and kegMap entries load without warnings and
survive Tapper-driven rewrites. A known-field update changes only Tapper-owned
values; explicitly removing an object removes that complete object.

## Hubs

Hubs are a name-keyed map. Each entry's `defaultNamespace` field is that Hub's
**default** namespace — a hub hosts many namespaces; this is only the one used
when a reference resolved against the Hub omits its own. The `atlas` remote Hub
is synthesized when not configured explicitly and not disabled with
`disableAtlasHub`.

An explicit entry always overrides the synthesized built-in.

```yaml
hubs:
  atlas:
    kind: remote
    defaultNamespace: me
    url: https://atlas.foldwise.ai
    tokenEnv: ATLAS_API_KEY
```

Supported kinds are `remote` and `readonly`. A namespace named `local` has no
special meaning; it resolves like any other namespace when a remote Hub hosts it.

### Trust boundary

`hubs{}` and `token`/`tokenEnv` may only be set in this user config. They are
stripped from any walked **project** `.tapper/config.yaml` (with a load warning;
`--strict` makes it a hard error) so a repository you `cd` into cannot introduce
a hub target or harvest a token. See
[Resolution Order](resolution-order.md#trust-boundary).

## Invocation Telemetry

Tap reports privacy-minimized invocation telemetry by default. Reporting is
best-effort and independent of `logLevel`: upload failures, timeouts, queue
pressure, or an older Hub never change a CLI exit code or MCP tool result.

Each event contains only the surface (`cli` or `mcp`), the exact Cobra command
path or MCP tool name, duration, success, optional CLI interactivity, and the
Tap client version. Arguments, errors, paths, node or keg identifiers, content,
credentials, and MCP session identifiers are never uploaded.

Events go only to `/api/v1/telemetry/invocations` on the authenticated remote
Hub selected by the user config's login-hub default/fallback chain, using the
existing AuthStore token. Tap silently skips reporting when it is not
bootstrapped, not authenticated, or connected to a Hub version without the
endpoint. The Hub writes accepted events to its
structured logs rather than PostgreSQL; Atlas currently inherits the standard
30-day Loki retention.

Opt out persistently in user config:

```yaml
disableTelemetry: true
```

Or opt out for a process/environment:

```bash
export TAP_DISABLE_TELEMETRY=1
```

## `tap bootstrap`

`tap bootstrap` materializes or refreshes this user config around a deployment
kind:

- `cloud` (default) — the compiled-in `atlas` remote hub
- `enterprise --endpoint <url>` — a user-supplied remote HTTP hub

It writes the selected **fallback** Hub (`fallbackHub`), not the
default slot — the project config owns the high-precedence `default*` slots.

It does **not** write a global `fallbackNamespace` or a per-user `namespaces`
entry. The preferred namespace comes from the resolved Hub's own
`defaultNamespace` field and is left empty until `tap auth login` adopts the
home namespace from the Hub's whoami probe.

It is idempotent: re-running only touches the fallback Hub and its entry,
leaving extension fields, `kegMap`, and any
`fallbackNamespace` you set by hand untouched. It also asks for a default keg and
records it as `fallbackKeg` (the global-user slot) so plain `tap` commands
resolve one after setup, while a project's `defaultKeg` or a `kegMap` rule can
still override it.

Interactive bootstrap also discovers flights from only the selected hub and
offers to store one as the user-level `flight` baseline. Existing baselines are
preselected when available, and **Skip for now** leaves the current value
unchanged. For scripts, pass the inherited global flag explicitly, for example
`tap bootstrap --kind cloud --flight @team/+focused`; bootstrap validates the
flight and stores its canonical `@namespace/+slug` reference. If no baseline is
set, MCP starts with identity-authorized full access; pin a least-privilege
flight and start a new MCP connection to narrow it. A project `flight`,
`TAP_FLIGHT`, or an explicit `--flight` on a later command overrides the
bootstrap baseline. Agent entries select models only and never select flights.

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
fallbackHub: atlas
fallbackNamespace: me
fallbackKeg: pub
kegMap:
  - alias: pub
    pathPrefix: ~/repos/github.com
hubs:
  atlas:
    kind: remote
    defaultNamespace: me
    url: https://atlas.foldwise.ai
    tokenEnv: ATLAS_API_KEY
```

Here `fallbackKeg: pub` and the `kegMap` alias `pub` are both remote KEG
references, resolved through the configured namespace and Hub.

## Common Mistakes

- Unresolvable reference: `defaultKeg`, `fallbackKeg`, or `kegMap.alias` is a
  bare name with no `defaultNamespace`/`fallbackNamespace`, or names a keg that
  does not exist on the resolved hub.
- No namespace resolvable: a Hub reference with no explicit, per-hub, default,
  or fallback namespace errors out.
- Missing fallback: no `defaultKeg` plus no `fallbackKeg` can produce
  `no keg configured`.
