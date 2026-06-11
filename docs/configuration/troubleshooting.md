# Troubleshooting

## "no keg configured"

Cause:

- `defaultKeg` and `fallbackKeg` are unset, and no explicit target was provided.

Fix:

- Set at least `fallbackKeg` in user or project config.
- Or run commands with an explicit target (`--keg`, `--project`, or `--path`).

## A keg reference fails to resolve

Cause:

- The reference resolves to no namespace/hub — e.g. a bare name with no
  `defaultNamespace`/`fallbackNamespace`, or a name that does not exist on the
  resolved hub. (There is no `kegs` alias map; resolution is namespace-centric,
  so there is no alias table to miss.)

Fix:

- Set `defaultKeg`/`fallbackKeg` to a resolvable reference: a bare name plus a
  `fallbackNamespace`, an explicit `@namespace/name`, or a path.
- Verify the reference in `defaultKeg`, `fallbackKeg`, and `kegMap` entries, and
  that the namespace routes to a hub via `defaultHub`/`namespaces`.
- Run `tap hub list` to see the kegs the configured hubs actually expose.

## "has no namespace and no per-hub, default, or fallback namespace is configured"

Cause:

- A reference against a remote hub omits its namespace and no per-hub
  `namespace`, `defaultNamespace`, or `fallbackNamespace` resolves it.

Fix:

- Use an explicit `@namespace/name` reference, or
- Set the hub's own `namespace` (its default), or
- Set `defaultNamespace` (project) / `fallbackNamespace` (user).

Local-hub references do not hit this — they fall back to the reserved `@local`
namespace.

## "ignored hubs … in project config"

Cause:

- A `.tapper/config.yaml` walked from the project tree defined `hubs{}` or a
  `token` / `tokenEnv`. Those are user-config-only and are stripped at load.

Fix:

- Move the hub definition and any credentials into
  `~/.config/tapper/config.yaml`.
- This is a warning by default; `--strict` turns it into a hard error. See
  [Resolution Order](resolution-order.md#trust-boundary).

## Unexpected Keg Selected

Cause:

- Precedence selected a different target than expected, possibly from a
  `.tapper/config.yaml` in a parent directory.

Fix:

- Check `defaultKeg`, `kegMap`, and `fallbackKeg` values.
- Verify path matches for `kegMap` (`pathRegex` before `pathPrefix`).
- Remember the project layer is a walk: a deeper `.tapper/config.yaml` overrides
  a shallower one. Use `tap config --explain FIELD` to see which source set a
  value.

## "hub … is not configured" / unexpected hub

Cause:

- A reference's resolved hub name is not present in `hubs` and is not a built-in
  (`local`, `atlas`).

Fix:

- Add the hub under `hubs:` in user config, or fix `defaultHub` / `fallbackHub`
  to name an existing hub.

## Debug Checklist

```bash
# Show merged config
tap config

# Inspect user and project configs separately
tap config --user
tap config --project

# See which source set a field (or all fields)
tap config --explain defaultKeg
tap config --show-sources

# Show active keg config (resolved target)
tap settings

# Confirm resolution for a specific alias
tap info --keg <alias>

# Force project-local resolution
tap info --project
```
