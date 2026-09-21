# Conventions

Branching, commits, versioning, and error handling.
[AGENTS.md](../../AGENTS.md) carries the short version.

## Branching Model

- `main` is the GitHub default branch and the de-facto working branch.
- `dev` exists and is preserved as a dev/main split for embedded-version
  coherence in the Claude plugin, but the supporting rulesets ("Protect main",
  "Force push protection") are currently disabled on this repo, so direct
  pushes and direct-to-main PRs are not blocked.
- The release pipeline is a single workflow: `release.yml` runs on
  `workflow_dispatch` against `main`, writes the changelog commit and tag,
  then runs goreleaser inline.
- Commit messages should follow Conventional Commits (for example `feat:`,
  `fix:`, `docs:`), with summaries no longer than 72 characters.
- Never use Conventional Commits breaking-change syntax (`type!:` or a
  `BREAKING CHANGE:` footer). Tapper stays on `v0.x` until the user explicitly
  authorizes a stable `v1` release. Incompatible changes during `v0.x` use an
  ordinary `feat:` or `refactor:` commit and may produce a minor `v0.x` bump.
- Automatic release versioning must never cross from `v0.x` to `v1.0.0`. A
  stable `v1` release requires explicit user direction and an explicit
  `version` workflow override; a code change or commit message is not release
  authorization.
- When opening a PR, base on `main` unless explicitly asked to route through
  `dev`. If `dev` is reactivated (rulesets re-enabled, default branch
  switched back), revisit this section.
- Create PRs only when requested. Merges, tags, releases, and publishing
  workflows remain separate actions requiring user authorization.

## Error Handling

- Sentinel errors in `pkg/keg/errors.go`: `ErrNotExist`, `ErrExist`, `ErrLock`,
  `ErrLockTimeout`, `ErrDestinationExists`, etc.
- Typed errors: `BackendError` (with Retryable), `RateLimitError`,
  `TransientError`.
- Check with `errors.Is()` for sentinels, `errors.As()` for typed errors.

## Read Next

- [Commands](commands.md)
- [Gotchas](gotchas.md)
