# Integration — KEG, Zeke, and Link Resolution

Describes common integration workflows between [keg](../26), [Zeke](../3), and repository indices. Focused examples emphasize safe, repeatable patterns.

## Zeke integration

[Zeke](../3) is used to draft or iterate on node content which then flows into [KEG](../5). Keep all network and destructive actions explicit.

Recommended safe workflow

1. Draft with Zeke and capture to a `.new` file:

   - zk "Draft README for new node about X" | [keg create](../26) draft --id 999 --stdin README.md
     - This produces docs/999/README.md.new (generators never overwrite existing files).

2. Inspect and accept:
   - `keg apply 999/README.md.new`
   - `keg edit 999` # refine locally
   - `keg meta edit 999` (if you need to adjust [meta.yaml](../18))
   - `keg validate]` docs/999
   - `keg gen-index`
   - `keg 999`

Notes

- Always produce `.new` files from automated generators; require `keg apply` to make changes live.
- Use `[keg validate](../26)` and `keg lint` before `[keg gen-index](../26)` in CI pipelines.
- Prefer `[keg create](../26) --template ... --arg=...` for reproducible scaffolds when templates are available; generators may still emit `.new` files for review.

## Link resolution in builds

[keg](../26) resolves `keg:` tokens using the repository keg file ([Keg configuration](../2) or keg.yml). Use `[keg link](../26)` to render tokens during static-site generation or other automation:

- Example:
  - `keg link "keg:owner/123" --format url` → prints resolved URL or returns an error if unresolved.

Best practices

- Resolve tokens in build steps where errors can be surfaced early.
- Do not perform automatic network fetches when resolving tokens; `[keg link](../26)` returns URLs — callers decide to fetch or clone. For the token form and alias resolution rules see [Keg links (keg-link)](../21).
- Validate that resolved [URLs](../9) do not embed credentials.

## Safe automation patterns

- Use `--dry-run` flags for multi-file operations.
- Prefer `[keg gen-index](../26)` then `[keg validate](../26)`.
- Require a manual `[keg apply](../26)` step for any destructive updates in CI (prevent unexpected commits).
- For index regeneration in CI, set environment variables (e.g., `KEG_CURRENT`) and fail the build if indices change unexpectedly.

## Example integration snippets

Create node draft via Zeke and validate:

- zk "Write a short README for a node describing 'Example feature'" | keg create example --id 456 --stdin README.md
- [keg validate](../26) docs/456
- [keg gen-index](../26) --dry-run

Resolve `keg:` tokens in a static-site generator (preferred: call `[keg link](../26)` per unique token):

- Example (pseudo):
  - for token in $(grep -o 'keg:[^)[:space:]]\+' templates/**/*.md | sort -u); do
      url=$([keg link](../26) "$token" --format url)
    # replace token with $url in rendered files
    done

(Prefer a proper templating step that calls `[keg link](../26)` once per token to avoid repeated work.)

## Notes on security and trust

- Never embed API keys or credentials in keg files or [meta.yaml](../18).
- When including external contexts or files (zeke includes), avoid automatic network fetches — require explicit user action.
- Use `[keg doctor](../26)` and CI checks to surface configuration problems (missing API keys, invalid include globs).

## Further reading

- KEG specification (authoritative): [KEG specification (keg-spec)](../5)
- Keg links / token resolution: [Keg links (keg-link)](../21)
- Zeke integration examples: [Zeke AI utility (zeke)](../3)
- Index generation & indices: [Keg index (keg-index)](../6)
