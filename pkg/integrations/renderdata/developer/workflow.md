# tapper-dev

Use this optional workflow only when the user asks for software delivery work.
The baseline `tapper` plugin is required: call `mcp__tapper__orient` before the
workflow and stop with an actionable prerequisite error if that tool is not
available.

The active flight and covered KEG instructions remain authoritative. Use
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
