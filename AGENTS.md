# Repository Guidelines

See [CLAUDE.md](./CLAUDE.md) for comprehensive project documentation including architecture, build commands, testing, and contribution guidelines.

## Coordinated immutable-flight delivery gate

The immutable-flight direct-subflight work is a coordinated change with the sibling
Tapper Hub repository. Until the user explicitly approves delivery after joint
verification:

- Do not merge related Tapper or Tapper Hub changes.
- Do not create or push release tags, GitHub releases, release commits, or
  trigger release workflows.
- Do not permanently update Tapper Hub's Tapper dependency pin.
- Use Tapper Hub's local `go.work` link for cross-repository integration
  testing.
- Stop at reviewable local branches/commits, test evidence, and a coordination
  report. Create or update PRs only when the user requests it.

These restrictions remain in force even when tests pass or either repository
appears independently ready to ship.

Commit messages should follow Conventional Commits.

Do not use Conventional Commits breaking-change syntax (`!` after the type or
scope, or a `BREAKING CHANGE:` footer) in this repository. Tapper remains on
the `v0.x` release line until the user explicitly authorizes a stable `v1`
release. During `v0.x`, describe incompatible changes in ordinary `feat` or
`refactor` commits; they may justify a minor `v0.x` release, but must never
implicitly authorize `v1.0.0`. An intentional `v1` requires both explicit user
direction and an explicit version override in the release workflow.
