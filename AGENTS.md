# Repository Guidelines

See [CLAUDE.md](./CLAUDE.md) for comprehensive project documentation including architecture, build commands, testing, and contribution guidelines.

## Coordinated development and dependency pins

Tapper and the sibling Tapper Hub repository may evolve together. Keep Hub's
ignored `go.work` for local integration development. Whenever Hub needs newer
Tapper functionality, commit and push Tapper first, then resolve that exact
pushed SHA with Go tooling and update Hub's `go.mod` and `go.sum` using
`GOWORK=off go get github.com/jlrickert/tapper@<sha>` and `GOWORK=off go mod tidy`.
Commit the dependency bump with the ordinary Hub implementation that needs it.
Never manufacture a pseudo-version or commit a local replacement. Verify Hub
with `GOWORK=off` against the pinned module before delivery.

Dependency bumps are ordinary development work and do not require a release or
a special pin-only commit. Merges, tags, releases, and publishing workflows
remain separate actions requiring user authorization. Create PRs only when requested.

Commit messages should follow Conventional Commits.

Do not use Conventional Commits breaking-change syntax (`!` after the type or
scope, or a `BREAKING CHANGE:` footer) in this repository. Tapper remains on
the `v0.x` release line until the user explicitly authorizes a stable `v1`
release. During `v0.x`, describe incompatible changes in ordinary `feat` or
`refactor` commits; they may justify a minor `v0.x` release, but must never
implicitly authorize `v1.0.0`. An intentional `v1` requires both explicit user
direction and an explicit version override in the release workflow.
