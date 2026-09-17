# Resolution Order

Tapper selects one Hub for each CLI invocation or MCP connection. A KEG
reference such as `@foldwise/dev` identifies a namespace and KEG within that
Hub; namespaces never route requests to another Hub.

| Selection | Precedence, highest first |
| --- | --- |
| KEG | `--keg` → `TAP_KEG` → project `keg` → matching `kegMap.keg` → user `keg` |
| Hub | `--hub` → `TAP_HUB` → project `hub` → matching `kegMap.hub` → user `hub` → automatic fallback |
| Flight | `--flight` → `TAP_FLIGHT` → project `flight` → matching `kegMap.flight` → user `flight` |

Project configuration overrides each corresponding directory default.
The winning KEG resolves within the winning Hub.

Automatic Hub fallback selects the alphabetically first explicitly configured
Hub. If none exist, it uses Atlas (`https://atlas.foldwise.ai`), unless
`disableAtlasHub: true` or `TAP_DISABLE_ATLAS_HUB=1` disables the built-in.
An explicitly configured Atlas connection remains available. Operations that
require a KEG fail when no KEG is selected; there is no automatic KEG fallback.
An invalid selected value fails instead of falling through.

## Directory mappings

```yaml
hub: atlas
keg: "@foldwise/dev"
hubs:
  atlas:
    url: https://atlas.foldwise.ai
  homelab:
    url: https://hub.example.test
kegMap:
  - pathPrefix: ~/repos/homelab
    hub: homelab
    keg: "@homelab/dev"
  - pathPrefix: ~/repos/bitbucket
    flight: "@work/+development"
```

Mappings use the startup directory. The first matching `pathRegex` wins.
Otherwise, the longest matching `pathPrefix` wins, with ties resolved in
configuration order. A prefix matches that directory and its descendants,
never a partial directory name. Invalid nonmatching expressions do not block
unrelated operations. Environment variables and a leading `~` are expanded.

One rule wins for all three fields. Each rule requires a path selector and at
least one of `keg`, `hub`, or `flight`; any combination is supported. Omitted
fields fall back independently to user configuration, never to a broader rule.
`alias` is retired: its value is preserved but ignored, and alias-only rules
cannot shadow supported mappings. Use `keg` for KEG defaults. A miss uses
project/user defaults. Empty flight strings do
not clear an inherited flight. Descendants of `~/repos/bitbucket` in this
example use `@work/+development` unless a higher-priority source overrides it.

## Configuration layers and trust boundary

Tapper walks from its startup directory to the filesystem root, collecting
`.tapper/config.yaml` files. Deeper project values override shallower ones.
The user configuration is the base layer. `--config` selects an explicit file
instead of the user/project file cascade; its mappings and environment
overrides still apply.

Only user configuration may define `hubs` and their `token` or `tokenEnv`
credentials. Project files can select saved Hub names, but Hub definitions
are stripped from project files with a warning (`--strict` makes it an error).
Unknown and retired fields and comments survive Tapper-owned rewrites, except
for the retired `updated` timestamp: its value is ignored on read and the key
is removed on the next serialization of user or project config. KEG settings,
node timestamps, and flight metadata are unchanged.
Malformed YAML fails; unused entries are not eagerly validated.

The retired keys `defaultKeg`, `fallbackKeg`, `defaultHub`, `fallbackHub`,
`namespaces`, and Hub `kind` are ignored data, not compatibility aliases.
Their old environment overrides no longer select anything.

## Namespaces and Flights

Use qualified `@namespace/keg` references. Existing KEG operations also accept
a bare name with explicit `--namespace`. Creation requires exactly one
`@namespace/keg` argument and rejects `--namespace`. Completions offer qualified
references only. `@local` has no special meaning.

The retired `defaultNamespace`, `fallbackNamespace`, saved Hub namespace defaults,
`TAP_DEFAULT_NAMESPACE`, and `TAP_FALLBACK_NAMESPACE` are ignored without warnings.
Bare flight references use only the active qualified KEG's namespace.

Flight selection remains independent: `--flight`, `TAP_FLIGHT`, project
`flight`, matching `kegMap.flight`, then user `flight`. An agent selects a model and telemetry identity,
never a Flight. See [Flights](flights.md).

## MCP connection lifetime

The canonical Hub URL is pinned for the connection lifetime. Calls reload
live Flight authority and credentials for that URL. Changing configuration
can affect a new connection, but cannot retarget an existing one. Discovery,
authentication probes, and operations stay within the active Hub. Local saved
connection listings may show every configured Hub.

Foreign direct URLs and event streams are rejected before dispatch. Redirects
cannot move requests to another Hub. Same-Hub orientation validation remains
in force; mutations are never automatically replayed after a refusal or an
ambiguous outcome.

Use `tap use` and `tap config --explain keg` or `--explain hub` to inspect the
selection, or `--explain flight` for the flight source. Both project and user
`tap use` write `keg` in their own scope; inspection reports the actual source
for all three defaults.

## Credential precedence

For the selected Hub, `tokenEnv` takes precedence over inline `token`, followed
by saved browser-assisted login. A configured `tokenEnv` that is missing or
empty fails closed; it never falls back to an inline token or saved login.
A rejected configured token also fails without switching credentials. Cached
KEG handles recheck environment credentials for requests and stream reconnects.
Startup refresh skips saved login credentials when a configured token takes
precedence. Browser context selection does not change this credential order.

Explicitly empty `token` or `tokenEnv` fields are invalid. Omit both to use saved
login. Built-in and newly generated Atlas defaults omit credential fields;
configure `tokenEnv: ATLAS_API_KEY` explicitly to require that environment token.
