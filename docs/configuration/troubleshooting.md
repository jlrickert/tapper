# Troubleshooting

## No KEG configured

Run `tap bootstrap` on a new machine. Set `keg` with
`tap use @namespace/keg --user` or `tap use @namespace/keg` for a project.
You can also pass `--keg @namespace/keg`. A missing KEG has no automatic fallback.

## Unexpected selection

Run `tap use`, `tap config --explain keg`, and `tap config --explain hub`.
A matching `kegMap` overrides project and user `keg`, while project `hub`
overrides the mapping's Hub. Environment variables and explicit flags win.
Mappings use the startup directory, and project files are loaded from every
parent directory. See [Resolution Order](resolution-order.md).

An MCP connection pins its Hub URL. Editing selection or changing a saved
alias's URL affects new connections; it does not retarget an existing one.
Live authority and credentials continue to reload for the pinned URL.

## Invalid Hub or reference

Check that `hub` names an entry in the user `hubs` map and that the entry has a
valid URL. Namespace-qualified KEGs resolve within that selected Hub.
Namespace-to-Hub routing is retired. Bare KEG names need a namespace default;
use `@namespace/name` to make the namespace explicit.

Selected invalid values fail rather than falling through. Fix malformed YAML
before proceeding. Retired `defaultKeg`, `fallbackKeg`, `defaultHub`,
`fallbackHub`, `namespaces`, and Hub `kind` are preserved but ignored.

## Project Hub definitions ignored

Move connection definitions and credentials to `~/.config/tapper/config.yaml`.
Projects may select saved names with `hub`, but cannot define connections.
The trust-boundary warning becomes an error under `--strict`.

## Orientation refusal

Inspect current authority with `orient`. Foreign Hub URLs are refused before
dispatch. A same-Hub stale revision requires a fresh authority check; a denied
operation requires appropriate Flight cover and Hub permissions. Mutations
must never be replayed automatically after a refusal or ambiguous outcome.
