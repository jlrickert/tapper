# Project Config

Project config defines repository-specific defaults.

## Purpose And File Location

- File: `.tapper/config.yaml`
- Scope: current repository

## View And Edit

```bash
tap repo config --project
tap repo config edit --project
tap repo config --template --project
```

## Override Behavior

Project config is merged after user config. For overlapping keys, project values are used.

Typical usage:

- set `defaultKeg` so commands in this repo resolve to the intended project alias
- keep machine-wide fallback behavior in user config

## Team Setup Pattern

- Commit `.tapper/config.yaml` with a project alias.
- Keep project-local keg content under `kegs/<alias>`.
- Use user config for personal/global discovery paths.

## Minimal Project Config Example

```yaml
defaultKeg: tapper
fallbackKeg: tapper
kegMap: []
kegs:
  tapper:
    file: kegs/tapper
defaultRegistry: knut
kegSearchPaths:
  - kegs
```
