# Keg links (keg-link)

A brief guide to the "keg:" link form used in this KEG and how link aliases declared in a keg file (or repo config) are resolved. This page documents the expected formats, resolution rules, and CLI behaviors, and implementation notes (Go) for tooling that wants to interpret or act on keg links.

## Purpose

- Provide a compact alias-based reference to other KEG instances or commonly-used targets.
- Make it easy to express "open this keg" or "refer to that keg" in README/meta and index files.
- Allow tooling (CLI, indexers, Zeke contexts) to resolve short references (keg:owner/<nodeid> or keg:alias) into a concrete URL or local repo path using the keg config.

## Recognized forms

- keg:<owner>/<nodeid>
  - Example: keg:jlrickert/123
  - Interpreted as a reference to the KEG instance owned by `owner` and a numeric node id `123` in that KEG. The second component is a NodeID (numeric).
- keg:<alias>
  - Example: keg:repo
  - Interpreted as a lookup of an alias defined in the local keg file (see "keg" file / ReadConfig).
- Plain alias or slug (best-effort)
  - Example: jlrickert
  - Tools may try to resolve this by searching the keg config links array for matching alias or by other heuristics.

Notes:

- The `keg:` prefix is explicit and unambiguous. Prefer it where you want tooling to definitely recognize the reference.
- When authoring, use the `links` array in your keg file to supply alias → URL mappings for local resolution.

## Where aliases live (keg file)

A keg file (repo-level config) commonly contains a links array where aliases are declared:

```yaml
links:
  - alias: repo
    url: git@github.com:jlrickert/tapper.git
  - alias: site
    url: https://keg.jlrickert.me/@jlrickert/public
  - alias: jlrickert
    url: https://keg-host.example/jlrickert/ # optional base for owner -> keg instance
```

Resolution rules:

- If a `keg:<alias>` is used and the local keg file has `links.alias == alias`, return `links.url`.
- If `keg:<owner>/<nodeid>` is used:
  - If the local keg file contains a link for the owner (an alias whose key is the owner name or another mapping for that owner), tooling can combine the owner's base URL with a node path (for example, append `docs/<nodeid>` or another project-specific path) to form a node URL. The exact concatenation depends on the target KEG hosting conventions; indexers/tooling should document their chosen mapping.
  - If no owner mapping is found, tooling may:
    - Fall back to a configured default pattern (if your tool supports one), or
    - Treat the reference as unresolved and surface an error to the user.
- If no local mapping is found for an alias, tooling may:
  - Fall back to a default pattern for owner→KEG mapping (if configured), or
  - Treat the reference as unresolved and surface an error to the user.

## CLI behavior and examples

Suggested CLI ergonomics for a `keg` command set:

- `keg links` — list alias → URL mappings from the local keg file
- `keg open <alias|keg:owner/<nodeid>|url>` — open the target in the browser or show clone/visit instructions
  - `keg open repo` # resolves via links array
  - `keg open keg:jlrickert/123` # resolves to the owner jlrickert and node id 123 (requires owner mapping or fallback)
  - `keg open https://example.org`
- `keg resolve <token>` — print the resolved URL (useful for scripts)

Examples:

- Resolve an alias then open:
  - `open $(keg resolve repo)`
- Use in markdown:
  - Link text: `[Node 123 on jlrickert's KEG](keg:jlrickert/123)`

CLI should:

- Prefer local keg links first.
- Validate resolved URLs (scheme sanity; no credentials embedded).
- Avoid automatic network operations — ask user to confirm clones/fetches.

## Security & validation

- Never accept or retain URLs that embed credentials (user:token@host). If such a URL is encountered, warn and redact.
- Validate scheme: permit git, ssh, http, https, file, mailto where appropriate.
- When resolving remote include sources, never fetch automatically — require explicit user action.

## Best practices

- Use `keg:` prefix where you want deterministic resolution by tooling.
- Populate links in your keg file with stable aliases and canonical URLs.
- Use descriptive alias names (repo, site, docs, or owner names) and keep them unique in the keg file.
- Prefer git SSH or HTTPS for repo URLs; use HTTPS for websites.
- Keep the keg file's `updated` timestamp current when changing links so indexers notice.
- When referencing a remote node via `keg:owner/<nodeid>`, ensure you have a mapping for `owner` in your links (for example, a base URL) or document the expected host pattern so tooling can construct a sensible node URL.

## Go implementation notes (projectType: go)

If you are implementing support for keg links in Go tooling, consider the following helper types and functions. Keep behavior deterministic and testable.

Suggested types:

```go
package keg

type KegLink struct {
    Alias string `yaml:"alias"`
    URL   string `yaml:"url"`
    Title string `yaml:"title,omitempty"`
}

type KegFile struct {
    Updated string    `yaml:"updated"`
    Links   []KegLink `yaml:"links"`
}
```

Suggested functions:

```go
// ParseKegFile parses a keg file (keg content) and returns a KegFile.
func ParseKegFile(data []byte) (*KegFile, error)

// ResolveAlias resolves a token like "repo" or "keg:owner/123" to a URL.
// repoLinks is the links loaded from the local keg file; fallbackFunc is used to construct default URLs
// when owner->base mappings exist or a default host pattern is configured.
func ResolveAlias(token string, repoLinks []KegLink, fallbackFunc func(owner string, nodeID string) (string, error)) (string, error)

// LoadAndResolve uses a repository config loader (KegRepository.ReadConfig) to resolve a token.
func LoadAndResolve(repo KegRepository, token string) (string, error)
```

Behavioral suggestions:

- Normalize aliases (trim, lowercase) when matching.
- When token starts with `keg:`:
  - If the second component is numeric, treat it as `owner/<nodeid>` and attempt to resolve to a node URL using:
    - a direct owner alias mapping from the local links, or
    - a configured fallback pattern (provided via fallbackFunc) that knows how to construct a node URL for a given owner and node id.
  - If the second component is non-numeric, treat it as an owner alias or plain alias lookup depending on your conventions.
- Keep networking out of `ResolveAlias`; return URLs and let caller decide to fetch/clone/open.

Error handling:

- Return typed errors (see pkg/keg/errors) such as:
  - ErrAliasNotFound
  - ErrInvalidKegLink
- Make resolution deterministic and well-tested.

Testing ideas:

- Unit tests for ParseKegFile with various link shapes.
- ResolveAlias tests for:
  - Exact alias match
  - `keg:owner/<nodeid>` form resolving via owner mapping
  - Unknown alias → error
  - Rejection of credential-embedded URLs
- Integration tests that ensure keg links listed in the keg file are printed by `keg links`.

## Examples

- In docs or README:
  - Use an explicit mapping: `keg:repo` and declare `repo` in the keg file.
  - Use direct node reference: `keg:jlrickert/123` — tooling can map that to a remote node URL if an owner mapping or fallback pattern is available.

## Minimal resolution pseudocode (Go-like)

- Load local keg file: cfg := repo.ReadConfig()
- Build map alias → url from cfg.Links
- Parse token:
  - if token starts with `keg:`:
    - strip prefix
    - if token matches `owner/<numeric>`:
      - if owner in map: return buildNodeURL(map[owner], nodeid)
      - else if fallbackFunc configured: return fallbackFunc(owner, nodeid)
      - else return ErrAliasNotFound
    - else if token is a bare alias: lookup alias in map
- If unresolved return ErrAliasNotFound
