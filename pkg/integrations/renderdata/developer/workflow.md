# tapper-dev

Use this optional workflow only when the user asks for software delivery work.
The baseline `tapper` plugin is required: call `mcp__tapper__orient` before the
workflow and stop with an actionable prerequisite error if that tool is not
available.

The active flight and explicitly loaded, targeted KEG instructions remain authoritative. Use
lifecycle types, fields, and state transitions only when the active flight,
KEG instructions, and current schemas support them. Never invent a schema or
embed a project-specific KEG target.

## Plan

Clarify the outcome, inspect the current implementation, discover applicable
instructions and schemas, identify touched scopes and validation gates, and
record a plan when the active KEG supports one. Planning owns intent, risks,
interfaces, and verification cover; it does not claim implementation evidence.

## Code

Implement the smallest coherent change using repository conventions. Preserve
unrelated user work. Run focused checks while iterating and distinguish
temporary scaffolding from artifacts that have a durable final-state consumer.
Record evidence against the exact candidate tree.

## Review

Review the integrated final state, not an intermediate patch. Confirm behavior,
tests, documentation, configuration, generated output, active decisions, and
applicable invariants agree. A failed or incomplete gate returns work to the
earliest stage that can correct it. Do not turn `checking` or `conflict` into
success through prose.

Before assigning a verdict, recompute knowledge discovery against the final
tree rather than trusting only the plan's original cover. Start from the
touched KEG subjects and the vocabulary and behavior changed by the diff. Use
targeted `mcp__tapper__backlinks`, `mcp__tapper__links`, and
`mcp__tapper__grep` calls in the active flight's covered KEGs to find plausible
decisions, patterns, research, incidents, interfaces, and verifications, then
read only the relevant notes. Informative knowledge guides interpretation;
only applicable active or stale interfaces and verifications enter gating
cover. A relevant contract discovered late expands the cover and sends the
work back through any newly required gates.

Audit every retained comment, test, document, configuration value, fixture,
telemetry hook, and generated artifact that refers to removed, deprecated, or
legacy behavior. Each needs a surviving subject or consumer such as current
observable behavior, an accepted decision, a compatibility or migration
boundary, a security or protocol prohibition, or an active invariant. If its
only owner is the removal or obsolete implementation, treat it as orphaned
noise and return the work for correction. Rewrite artifacts around a broader
surviving invariant when that is the real contract; do not reject an artifact
merely because it uses the word `legacy`.

## Commit

Commit only the reviewed tree and follow the repository's commit convention.
If formatting, regeneration, rebasing, or any other mutation changes the tree
after review, rerun the affected validation. When supported by the active KEG,
record the final patch evidence and complete task/plan state only after the
verified result is coherent.

## Handoffs

Carry the same task identity, touched scopes, validation gates, candidate tree,
and evidence across stages. Make disagreements explicit. The recording stage
preserves the review verdict; it does not reinterpret it.
