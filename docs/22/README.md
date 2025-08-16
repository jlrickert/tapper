# Tapper configuration (tapper-config)

Concise summary: specification for Tapper’s configuration and repository-local overrides that tell tooling which KEG to use for a given project. Includes filenames, schemas, precedence rules, CLI behaviors, examples, and security guidance.

## Purpose

This node defines a small, predictable configuration system for Tapper that lets a developer or project declare which KEG instance should be used when running KEG tooling from a repository. It supports:

- developer-local ephemeral overrides (convenient, not committed),
- visible repo-local overrides (optionally committed),
- user/global aliases for known KEG targets,
- deterministic precedence and resolution rules used by Tapper and optionally by keg (or a wrapper).

The design balances convenience (git config) with discoverability and reproducibility (.tapper files / ~/.config).

## Key concepts

- override: a mapping indicating which KEG to use for a repo (alias, local path, or git URL).
- alias: a named target kept in the user config (e.g., `work` → `git@github.com:org/work-keg.git`).
- repo-local config: file placed under the repo (default ignored) to explicitly set the KEG for that repo.
- developer-local override: per-repo git config key (not committed) for quick, private selection.
- resolution: algorithm to pick which KEG target applies in the current directory.

## Locations & filenames

Recommended placement (use XDG where appropriate):

- Developer-local (ephemeral, not committed)
  - git config key: `tap.keg` (local scope, stored in .git/config)
    - set: git -C "$(git rev-parse --show-toplevel)" config --local tap.keg work
    - get: git -C "$repo" config --local --get tap.keg
- Repo-local (visible, optionally committed; ignored by default)
  - repo path: `.tapper/local.yaml` (ignored by default)
  - template: `.tapper/local.example.yaml` (committed)
  - recommended to add `.tapper/local.yaml` to `.gitignore` by default
- User/global (aliases, defaults)
  - path: `$XDG_CONFIG_HOME/tapper/aliases.yaml` or `~/.config/tapper/aliases.yaml`
  - optional global overrides: `$XDG_CONFIG_HOME/tapper/config.yaml`
- Wrapper / runtime override
  - environment variable: `KEG_CURRENT` (highest precedence if set)

## Minimal YAML schema

This is the expected shape for repo-local `.tapper/local.yaml`. Implementations should validate and normalize.

```yaml
# .tapper/local.yaml
updated: 2025-08-14T12:00:00Z # RFC3339 UTC, optional but recommended
keg:
  alias: work # optional: alias resolved via ~/.config/tapper/aliases.yaml
  url: git@github.com:org/work-keg.git # optional explicit git URL or file path
  path: /Users/jlrickert/kegs/work # optional explicit local filesystem path (preferred if present)
  prefer_local: true # if both local path and remote exist, prefer local
note: "Set via `tap repo work`"
```

User-level aliases file format:

```yaml
# ~/.config/tapper/aliases.yaml
updated: 2025-08-14T12:00:00Z
aliases:
  work:
    url: git@github.com:org/work-keg.git
    prefer_local: true
  localdev:
    path: /home/dev/kegs/common-keg
    prefer_local: true
```

Rules:

- `keg` must provide at least one of `alias`, `url`, or `path`.
- `alias` is resolved via the aliases file; resolving an alias produces a `url` or `path`.
- Reject values embedding credentials (e.g., `https://user:token@host`).

## Match criteria (for more advanced mapping entries)

If you choose to allow multiple mappings per repo (useful in global `~/.config/tapper`), each mapping can include match conditions:

- path_prefix: absolute or repo-root-relative path prefix
- path_glob: glob pattern
- git_remote_host / git_remote_owner / repo_regex: parsed from the origin remote URL
- repo_root_file: presence of filename at repo root (e.g., `keg`, `docs/keg`)

Example mapping (global config):

```yaml
mappings:
  - name: "primomed → work"
    match:
      git_remote_owner: primomed
      repo_regex: "^primomed(-.*)?$"
    keg:
      alias: work
    priority: 100
```

## Resolution & precedence

Ordered from highest to lowest priority. The first match wins (unless two candidates tie and tie-breaking rules apply).

1. Explicit CLI flag or environment
   - `--keg` or `KEG_CURRENT` environment variable
2. Per-invocation overrides (command-level / explicit wrapper)
3. Developer-local git config (repo-scoped)
   - `git config --local tap.keg pub`
4. Repo-local file (e.g., `.tapper/local.yaml`) at repo root
5. Project keg file (e.g., `docs/keg` or `./keg` inside the repo)
6. User-level tapper aliases / config (`~/.config/tapper/aliases.yaml`)
7. Fallback defaults (e.g., `~/.config/keg` or built-in behavior)

Tie-breakers among multiple matching mappings:

- higher numeric `priority`
- then specificity (longer `path_prefix`, more match criteria)
- then declaration order

## Integration strategies

Two ways to make keg use the override:

A) Modify keg discovery (recommended if you control keg)

- Add a step in repo discovery (NewFsRepoFromEnvOrSearch) to check:
  - if `git config --local tap.keg` exists
  - if `.tapper/local.yaml` exists in repo root
- If an override is found, treat it as the active KEG (resolve alias → url/path) and instantiate the repo accordingly (or set `KEG_CURRENT` internally).

B) Wrapper shim (no change to keg)

- Install a small `tap-keg` wrapper (or advise users to add an alias) that:
  - resolves the KEG target using the precedence rules, sets `KEG_CURRENT`, and execs the real `keg` binary with forwarded args.
- This avoids modifying keg internals and keeps the behavior opt-in.

## Examples

Set a quick developer-local override with git config:

```bash
# in primomed repo
git -C "$(git rev-parse --show-toplevel)" config --local tap.keg work
# confirm
git -C "$(git rev-parse --show-toplevel)" config --local --get tap.keg
```

Persist to visible repo file:

```bash
repo_root="$(git rev-parse --show-toplevel)"
mkdir -p "$repo_root/.tapper"
cat > "$repo_root/.tapper/local.yaml" <<EOF
updated: $(date -u +"%Y-%m-%dT%H:%M:%SZ")
keg:
  alias: work
  prefer_local: true
note: "persisted override"
EOF
```

Example aliases file:

```yaml
# ~/.config/tapper/aliases.yaml
aliases:
  work:
    url: git@github.com:org/work-keg.git
    prefer_local: true
```

Resolving in a wrapper:

```bash
#!/usr/bin/env bash
# tap-keg wrapper (simplified)
# resolves keg target and execs keg with KEG_CURRENT set

resolve_keg() {
  # check KEG_CURRENT
  [ -n "$KEG_CURRENT" ] && echo "$KEG_CURRENT" && return
  # check git config
  if repo_root="$(git rev-parse --show-toplevel 2>/dev/null)"; then
    val="$(git -C "$repo_root" config --local --get tap.keg || true)"
    [ -n "$val" ] && { echo "$val"; return; }
    # check .tapper/local.yaml (omitted parsing for brevity)
  fi
  # fallback
  echo ""
}

target="$(resolve_keg)"
export KEG_CURRENT="$target"
exec /usr/local/bin/keg "$@"
```

---

## Validation & safety

- Always validate values before storing:
  - URLs: parseable, allowed scheme (git, ssh, https, file)
  - Paths: absolute or repo-root-relative; check existence only if `prefer_local` is true
  - Aliases: token pattern `[a-z0-9._-]+` (config may choose to be stricter)
  - Reject and warn if a URL contains embedded credentials (`user:pass@host`)
- Never store secrets in config files. Use environment variables or system credential helpers for auth.
- For repo-local files, write atomically (write to temp, fsync if possible, then rename).

Atomic write example (bash):

```bash
repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
mkdir -p "$repo_root/.tapper"
tmp="$(mktemp "$repo_root/.tapper/local.yaml.tmp.XXXX")"
cat >"$tmp" <<EOF
updated: $(date -u +"%Y-%m-%dT%H:%M:%SZ")
keg:
  alias: work
  prefer_local: true
note: "set by tap repo set"
EOF
mv "$tmp" "$repo_root/.tapper/local.yaml"
```

## Testing & CI

- Provide unit tests for the resolver:
  - multiple candidate sources (git config + .tapper) — ensure precedence works
  - alias resolution via aliases file
  - validation rejects credentials
  - fallback behavior when files / git not present
- In CI, don’t rely on developer git configs. Either set `KEG_CURRENT` explicitly in the CI environment or commit a project-level override (.tapper/local.yaml) intentionally.

## UX & discovery recommendations

- `tap repo show` should always print:
  - the effective target,
  - which source set it (git-config / .tapper / alias / env),
  - and, if applicable, how to change it (commands).
- Provide `tap repo set --persist` to write `.tapper/local.yaml`.
- Provide `tap repo set --local` to write git config (default).
- Always confirm destructive actions (unset, delete).
- Document this behavior in CONTRIBUTING.md and README to avoid surprise.

## Migration & compatibility

- If you previously used a different scheme (e.g., only global aliases), support both old and new formats during a transition period. Emit warnings encouraging users to move to the new precedence model.
- Provide `tap repo migrate` if you need to transform older settings into the new expected format.

## Example node meta (suggested)

If you add a meta.yaml for this node, use:

```yaml
updated: 2025-08-14T12:00:00Z
title: Tapper configuration (tapper-config)
summary: Specification for developer and repo-level overrides that map projects to KEG targets.
tags:
  - tapper
  - config
  - keg
```
