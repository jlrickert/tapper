# Utilization of URLs in a KEG

How to use URLs in KEG files — where they appear (top-level url, creator, links), preferred schemes (git SSH / HTTPS), tooling guidance (display, validation, avoid automatic fetch), and security rules (never embed credentials). Examples are in the keg docs. Note: this page duplicates other KEG documentation and may be removed or merged.

## Where URLs appear

- Top-level `url`

  - Canonical location for the keg (repo, project site, docs).
  - Common forms: git SSH (git@github.com:org/repo.git), HTTPS git (https://github.com/org/repo), or a website (https://example.org).
  - Tooling: shown in listings and may be used when the user opts to clone/fetch.

- Top-level `creator`

  - Identifies the keg maintainer (git remote, profile URL, or mailto).
  - Treated as attribution/contact info (not an auth mechanism).

- `links` (array)

  - Each entry typically has:
    - `alias`: short identifier
    - `url`: target URL (http(s), git, file:, mailto:, etc.)
    - optional `title`/`summary`
  - Used by UIs/CLI to expose quick links (open, clone, etc.).

- Other places
  - Index files, README references, and zeke contexts may contain URLs.
  - Includes may reference local globs or remote repos (prefer git URLs).

## Accepted schemes and recommendations

- Preferred:
  - git SSH: git@github.com:owner/repo.git — for workflows that use SSH.
  - HTTPS git: https://github.com/owner/repo — browser-friendly.
  - HTTP/HTTPS: https://example.org — documentation and project websites.
- Acceptable but use with caution:
  - file:, mailto:, ftp: — use where appropriate and portable.
- Avoid embedding credentials (e.g., https://user:token@host/...). Never commit secrets in URLs.

## Top-level `url` — semantics & tooling

- Purpose: the canonical place to find the keg (repo or website).
- Tooling guidance:
  - Display prominently in listings and node headers.
  - Support both SSH and HTTPS forms when offering clone/fetch actions.
  - Don’t automatically perform network actions without user consent; respect the `url` as metadata unless the user asks to act.
- Examples:
  - git SSH: git@github.com:jlrickert/tapper.git
  - HTTPS: https://github.com/jlrickert/tapper
  - Website: https://keg.jlrickert.me/@jlrickert/public

## The `creator` field

- Use it to point to a maintainer or contact:
  - git remote, profile page (https://github.com/jlrickert), or mailto: link.
- Treat it as a hint for attribution and contact, not for access control.

## links: array — structure, semantics, and usage

- Minimal entry:
  - alias: short-key
  - url: https://...
- Example:
  - links:
    - alias: repo
      url: git@github.com:jlrickert/tapper.git
    - alias: site
      url: https://keg.jlrickert.me/@jlrickert/public
- Semantics:
  - `alias` should be unique within the keg.
  - Tooling may validate scheme and optionally check reachability.
  - Provide `title` or `summary` for nicer UI rendering.
- Common uses:
  - CLI: keg open repo → open `links.repo.url`
  - Render link lists in docs or UIs
  - Indexers: produce external link lists or badges

## Relative vs absolute URLs

- Relative (./docs/index.html, ../2) are fine for internal links — prefer them for repository portability.
- Absolute URLs (https://...) are appropriate for canonical or external references (top-level `url`, external links).

## Includes and remote kegs

- `includes` entries usually reference local files or globs.
- For remote kegs prefer git URLs and document fetch behavior:
  - file: git@github.com:org/other-keg.git
  - file: https://github.com/org/other-keg.git
- Tooling should:
  - Make fetching explicit (no silent downloads).
  - Validate and sanitize remote URLs.
  - Cache or vendor remote content if reproducible loads are needed.

## Validation and normalization

- Minimal validation:
  - Non-empty string
  - Recognized scheme (http, https, git, ssh, file, mailto)
  - No spaces or control characters
- Normalization guidance:
  - Trim whitespace
  - Prefer a consistent git URL form (ssh or https) for display
  - Keep the original string for clone operations; removing `.git` is optional and display-only
- Example validator (pseudo):
  - scheme = parseScheme(url)
  - if scheme not allowed → warn/error
  - if url contains credentials → error or redact + warn

## Security and privacy

- NEVER store API keys, tokens, or passwords in URLs.
- Avoid "user:token@" URL forms in committed files.
- Use environment variables, SSH agents, or credential helpers for secrets.
- Treat remote includes as external dependencies — review before merging.

## UI and CLI considerations

- Show canonical `url` in listings and node headers.
- Provide link commands (examples):
  - keg links — list aliases + URLs
  - keg open <alias|url> — open in browser or prompt to clone if git URL
- Resolve relative links when rendering docs for a web UI.

## Examples

Keg file snippet:

```yaml
updated: 2025-08-09 18:12:52Z
title: KEG Worklog for tapper
url: git@github.com:jlrickert/tapper.git
creator: https://github.com/jlrickert

links:
  - alias: repo
    url: git@github.com:jlrickert/tapper.git
    summary: main git repo
  - alias: website
    url: https://keg.jlrickert.me/@jlrickert/public
    summary: project documentation site
  - alias: ci
    url: https://github.com/jlrickert/tapper/actions
    summary: CI pipeline
```

Relative documentation link example:

- In README.md or dex/changes.md use relative links (../2) so internal navigation remains local and portable.

## Best-practice summary

- Use `url` for the canonical keg location (repo or site).
- Use `creator` to identify the maintainer (avoid credentials).
- Populate `links` with stable, unique aliases and meaningful targets.
- Prefer HTTPS or git SSH for repo URLs; use HTTP(S) for websites.
- Don’t embed credentials in URLs; rely on environment/credential helpers.
- Validate scheme and basic format; warn on suspicious patterns.
- Use relative links within the repo and absolute URLs for external references.
