# Repository Guidelines

See [CLAUDE.md](./CLAUDE.md) for comprehensive project documentation including architecture, build commands, testing, and contribution guidelines.

Commit messages should follow Conventional Commits.

Do not use Conventional Commits breaking-change syntax (`!` after the type or
scope, or a `BREAKING CHANGE:` footer) in this repository. Tapper remains on
the `v0.x` release line until the user explicitly authorizes a stable `v1`
release. During `v0.x`, describe incompatible changes in ordinary `feat` or
`refactor` commits; they may justify a minor `v0.x` release, but must never
implicitly authorize `v1.0.0`. An intentional `v1` requires both explicit user
direction and an explicit version override in the release workflow.
