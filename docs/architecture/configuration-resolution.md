# Configuration Resolution

How the resolver is put together, for contributors changing it.
[Resolution Order](../configuration/resolution-order.md) describes the same
behavior from a user's point of view — keep the two in agreement.

## Config Hierarchy

Tapper config is resolved via a cascade (most specific wins):

| Rank | Source                          | Discovery                                          |
| ---- | ------------------------------- | -------------------------------------------------- |
| top  | CLI flags (`--log-level`, etc.) | Cobra `cmd.Flags().Changed()`                      |
| ↑    | Env vars (`TAP_*`)              | `rt.Env().Get()` prefix scan                       |
| ↑    | Project configs (deepest→…)     | every `.tapper/config.yaml` from cwd up to `/`     |
| ↑    | Matching directory mapping      | one `kegMap` rule supplies `keg`, `hub`, `flight` defaults |
| base | User config                     | `~/.config/tapper/config.yaml`                     |
| —    | Defaults                        | Hardcoded in code                                  |

`ProjectConfig` walks from the workspace root up to the filesystem root
(`WalkConfigsUp`), collecting every `.tapper/config.yaml`, and merges them so a
deeper directory overrides a shallower one. **Trust boundary:** only the user
config may define `hubs{}` and `token`/`tokenEnv`; those fields are stripped
from any walked project config (recorded as a load warning; `--strict` makes it
a hard error). The merged project layer, the user config, and env vars are then
resolved by the centralized selection in `ConfigService.Config()`.

**Hub / namespace resolution** selects one Hub per invocation or MCP connection.
Namespace-qualified KEGs remain supported; a bare KEG requires explicit `--namespace`.
The retired default/fallback Hub and KEG keys, namespace routing, and Hub `kind`
are ignored and preserved during rewrites. Project `keg`, `hub`, and `flight` each override the matching mapping's
corresponding default. Omitted mapping fields independently use user defaults;
exactly one mapping wins, with no inheritance from broader rules. Regexes precede
prefixes, longest directory prefix wins, and ties retain configuration order.

MCP pins the canonical Hub URL, then reloads live authority and credentials for
that URL without retargeting it. Foreign targets and redirects are refused.
Remote discovery and startup authentication probes contact only the active Hub.
Local saved-connection listings can show every configured Hub.

## Command Surface Notes

To **list** available kegs, query a hub: `tap keg list` / the `keg_list` MCP
tool (backed by `GET /api/v1/kegs`).
(`tap hub list` lists configured *hub connections*, not kegs.)

**`tap keg create @namespace/keg`** requires exactly one qualified argument,
resolves the selected Hub, then creates via
`POST /api/v1/@<ns>/kegs` (failing on 409). When nothing is configured, the full
`tap` surface refuses with a "run `tap bootstrap`" error (`ErrNotBootstrapped`)
rather than silently creating local state. `tap init` and the local creation
flags are removed.

**Command groups.** `tap keg` administers kegs on a hub (`list`, `create`,
`grants`/`grant`/`revoke` for ACLs, `visibility`, `rename`, and `settings` for
the keg's own config — formerly `tap settings`). `tap namespace` administers namespaces
and membership roles (`list`, `members`, `add-member`, `set-role`,
`remove-member`, `create`). `tap hub` manages hub *connections* (`list`,
`status`, `add`/`remove` writing user config, `set-default` writing project
config by default, `--user` for user). `tap config edit` defaults to the **project**
config; `--user` targets the user config.

**Keg selection is flag-driven, not positional.** The keg an admin command
operates on comes from the global resolution flags — `--keg` (a bare name or
`@namespace/keg`), with `--namespace`/`--hub` as component
overrides — not a positional. A bare invocation (no `--keg`) targets the
resolved KEG. The on-disk discovery selectors `--project`/`--cwd` are gone;
`--path` is not a KEG target selector.

**`tap use`** records resolution in config: `tap use @ns/keg` sets the project's
`keg` (in `.tapper/config.yaml`); `tap use @ns/keg --user` sets the
user `keg`; `--flight @ns/+slug` sets the project's persisted
flight; bare `tap use` prints the resolved keg/hub/flight and the scope
that set each. A persisted `flight` auto-applies when `--flight` is omitted. Flight precedence
is `--flight` → `TAP_FLIGHT` → project `flight` → matching `kegMap.flight` →
user `flight`. The retired Tapper config `updated` timestamp is ignored on read
and removed on serialization; KEG settings and node timestamps are unchanged.

Supported env vars: `TAP_KEG`, `TAP_FLIGHT`,
`TAP_AGENT`, `TAP_LOG_FILE`, `TAP_LOG_LEVEL`, `TAP_HUB`,
`TAP_DISABLE_ATLAS_HUB`,
`TAP_DISABLE_TELEMETRY` (`1`/`true`/`yes`/`on` for
the disable flags).

Use `tap config --explain FIELD` to see which source set a value, or
`tap config --show-sources` for all fields. Malformed YAML is always an error; `--strict` also makes trust-boundary warnings
into errors.

KEG settings are separate from Tapper user/project config — different schema,
different purpose (KEG metadata vs resolver settings) — and are read or written
through the Hub.

## Read Next

- [Resolution Order](../configuration/resolution-order.md)
- [Service Layer](service-layer.md)
- [Feature Organization](../development/feature-organization.md)
