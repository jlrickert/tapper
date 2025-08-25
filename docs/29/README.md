# Design patterns in pkg/keg/cmd

A single, focused reference describing the design patterns used across the CLI wiring code under pkg/keg/cmd. This document targets maintainers and contributors who need to understand why things are structured the way they are and how to extend or test the CLI cleanly.

Files considered

- pkg/keg/cmd — command wiring and helpers (NewRootCmd, NewRootCmdWithDeps, Run, RunWithDeps)
- CmdDeps / CmdOption and helpers (WithIO, WithKeg, etc.)
- Edit / EditorRunner and editor-launch helpers
- Run / RunWithDeps and context-aware execution helpers

Summary

- The package applies several complementary patterns to make the CLI testable, predictable, and easy to compose:
  - explicit dependency injection (CmdDeps),
  - functional options for configuration (CmdOption),
  - pure command factory functions (NewRootCmdWithDeps),
  - context-aware entry points (RunWithDeps),
  - small abstractions for editor/IO behavior (EditorRunner, Edit).
- These choices emphasize single-responsibility, testability (no hidden global state), and safe defaults at the application boundary.

## Goals and rationale

Why these patterns were chosen

- Testability: building commands from injected dependencies enables unit/integration tests without touching disk, network, or TTYs.
- Predictability: avoid package init side effects and global mutable state; callers decide when to materialize resources.
- Composability: functional options let callers add or override behavior without exploding constructor signatures.
- Safety: context propagation and conservative defaults reduce surprises in long-running or interactive operations.

Design constraints reflected by the code

- CLI code must be usable in automated tests (CI) and interactive shells.
- Editors and external programs must not run during unit tests.
- Index and repo operations must be cancelable and observable (contexts, clocks).

## Dependency injection: CmdDeps

What it is

- A small dependency bag that holds:
  - Keg service instance (high-level API)
  - IO streams (In, Out, Err)
  - Editor runner (EditorRunner)
  - Clock/other helpers
  - Unexported internal fields for optional wiring

Why it matters

- Centralizes the runtime pieces commands need.
- Makes it easy to replace real implementations (FsRepo-backed Keg) with test doubles (MemoryRepo, FixedClock).
- Keeps constructors simple: pass a single \*CmdDeps instead of many arguments.

Idioms

- Construct a deps object in tests with only the pieces needed:
  - deps := &CmdDeps{ In: bytes.NewBufferString("..."), Out: &buf, Err: &errBuf, Keg: testKeg }
- Provide small helper options (WithIO, WithKeg) to mutate CmdDeps via functional options.

Best practices

- Keep CmdDeps minimal. Only add fields shared across multiple commands.
- Make mutation via CmdOption idempotent where possible.
- Avoid hidden creation of heavy resources inside CmdDeps — prefer explicit ApplyDefaults.

## Functional options: CmdOption and helpers

What they are

- CmdOption is func(\*CmdDeps). Options are small, composable functions that configure CmdDeps.

Benefits

- Avoids combinatorial constructors.
- Callers pick only the options they need.
- Tests can apply only the options relevant to the scenario.

Common helpers

- WithIO(in io.Reader, out io.Writer, errOut io.Writer) CmdOption
- WithKeg(k \*keg.Keg) CmdOption

Patterns

- Implement options as simple closures that mutate the deps struct.
- Document expected semantics (e.g., WithIO should set Err to out when errOut is nil).

Pitfalls

- Options should not perform heavy I/O or create networked clients — they should configure, not execute.
- Avoid conflicting options; document override semantics (last option wins).

## Command construction: NewRootCmd / NewRootCmdWithDeps

Pattern

- Commands are built by pure factory functions that accept dependencies (or options) and return cobra.Command trees.

Why this pattern

- Enables constructing the full CLI in tests with injected dependencies.
- Avoids global variables for commands and flags.
- Encourages small subcommand factory functions (single responsibility per factory).

Recommended approach

- Each subcommand is created by a function that accepts only the deps it needs.
  - func newRepoShowCmd(deps *CmdDeps) *cobra.Command
- NewRootCmdWithDeps composes subcommands and wires global flags by reading CmdDeps fields.

Testing

- In tests, construct the root with NewRootCmdWithDeps(deps), call cmd.SetArgs(...) and ExecuteContext(ctx).

Pitfalls

- Do not reach into package-level globals from subcommands; use closure-captured deps instead.
- Avoid mutating shared deps during command execution in ways that break multiple-run tests.

## Execution entrypoints: Run / RunWithDeps and context propagation

Run / RunWithDeps

- Run is a convenience wrapper that builds the root command and executes it with default behavior.
- RunWithDeps accepts a context and explicit deps, enabling cancellation and timeout in tests and callers.

Context propagation

- Commands that perform IO or long-lived work should accept context and propagate it to repo/clients.
- Use ExecuteContext / cobra’s context-aware execution so cancellation flows naturally.

Benefits

- Tests can set timeouts and force cancellation to exercise cleanup behavior.
- Operations involving the repo, network, or indexing can be aborted safely.

Implementation notes

- Ensure long-running library functions accept context so the whole stack is cancellable.
- Keep main() trivial: construct a context (maybe with signal handling) and call Run/RunWithDeps.

## ApplyDefaults and wiring vs runtime defaults

ApplyDefaults

- A deliberate, conservative helper that ensures CmdDeps has sane defaults but avoids creating heavy resources implicitly.

Rationale

- Tests should be explicit about what they need; automatic creation of real filesystem-backed repos or network clients in ApplyDefaults would make tests brittle.

Guidance

- Provide a small helper for production code that calls ApplyDefaults plus any materialization helpers (e.g., NewFsRepoFromEnvOrSearch) so main() can remain concise while tests remain explicit.

Pitfalls

- Don’t hide side effects inside ApplyDefaults. If resource initialization is necessary, expose a separate function (e.g., MaterializeDefaults) and document the difference.

## Editor and IO abstractions (Edit, EditorRunner, In/Out/Err)

EditorRunner

- Type alias: type EditorRunner func(path string) error
- CmdDeps exposes an EditorRunner so the editor invocation can be replaced in tests.

Edit helper

- Centralizes editor selection ($VISUAL, $EDITOR, fallback) and launching logic.

IO injection

- CmdDeps.In, Out, Err are io.Reader/io.Writer values that tests replace with buffers.

Why this pattern

- Interactive behavior (spawning editors) must be stubbable in tests.
- IO injection allows deterministic assertions about what the CLI printed or read.

Test patterns

- In tests set deps.Editor to a stub that writes file contents or records calls.
- Set deps.In to strings.NewReader to simulate stdin and deps.Out to bytes.Buffer to capture output.

Edge cases to handle

- Non-TTY environments: Edit helper must handle missing TTYs gracefully.
- Editor failures: non-zero exit codes should surface as errors.

## Testing patterns and recommended doubles

MemoryRepo and FixedClock

- The project provides MemoryRepo and FixedClock to make tests deterministic:
  - MemoryRepo: in-memory KegRepository implementation
  - FixedClock: deterministic Clock for time-dependent behavior

Common test setup

- deps := &CmdDeps{}
- WithIO(...)(deps)
- WithKeg(keg.NewKeg(memoryRepo, resolver))(deps)
- deps.Editor = func(path string) error { /_ stub _/ }
- cmd := NewRootCmdWithDeps(deps)
- cmd.SetArgs(...)
- err := cmd.ExecuteContext(ctx)

Assertions

- Capture and inspect deps.Out and deps.Err buffers.
- Verify repository state via memoryRepo APIs.
- Verify that no real editors or filesystem operations ran.

Best practices

- Keep test helpers to set up common scenarios (WithTestKeg, WithInMemoryIO).
- Ensure tests do not rely on ApplyDefaults behavior.

## Error handling and exit semantics

Command-level errors

- Commands should return errors (RunE) and let a single top-level runner convert them into exit codes and human-friendly messages.

Consistency

- Surface errors from underlying library code (keg package) and let callers apply convenience wrapping/presentation.

Tips

- Do not call os.Exit deep in libraries; call it only in main if needed.
- Use contextual messages for users, but preserve error chains for tests (use fmt.Errorf("%w") or errors.Join).

## Best practices and heuristics

- Single responsibility per constructor: each subcommand factory should only wire what it needs.
- Explicit is better than implicit: prefer passing deps or options instead of using globals.
- Prefer small interfaces for injected collaborators (EditorRunner, Clock) so tests can stub easily.
- Use ExecuteContext and accept context in long-running operations.
- Keep ApplyDefaults conservative; provide a separate production bootstrap helper that creates real resources.
- Centralize editor/exec logic so it is easy to replace/stub and to audit security (no accidental network execs).
- Document option semantics clearly (which options override others).

## Common pitfalls to avoid

- Creating heavy resources implicitly in ApplyDefaults (disk/io/network).
- Subcommands reaching into unexported package globals.
- Tests that accidentally launch real editors or mutate the real FS because they didn't stub EditorRunner or Keg.
- Mutating shared CmdDeps concurrently in ways that break test isolation.
- Not propagating context into repo or index operations leading to non-cancellable behavior.

## Small Go idioms / examples

Production main (pattern)

```go
func main() {
  ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
  defer cancel()
  if err := cmd.Run(ctx, os.Args[1:]); err != nil {
    fmt.Fprintln(os.Stderr, "error:", err)
    os.Exit(1)
  }
}
```

Test harness (pattern)

```go
func TestRepoShow(t *testing.T) {
  out := &bytes.Buffer{}
  errOut := &bytes.Buffer{}
  deps := &CmdDeps{}
  WithIO(ioutil.NopCloser(strings.NewReader("")), out, errOut)(deps)
  mem := NewMemoryRepo()
  WithKeg(keg.NewKeg(mem, nil))(deps)
  deps.Editor = func(path string) error { return nil } // safe stub

  root := NewRootCmdWithDeps(deps)
  root.SetArgs([]string{"repo", "show"})
  if err := root.ExecuteContext(t.Context()); err != nil {
    t.Fatalf("execute failed: %v", err)
  }
  if got := out.String(); !strings.Contains(got, "expected") {
    t.Fatalf("unexpected output: %q", got)
  }
}
```

Editor stub example

```go
deps.Editor = func(path string) error {
  // write expected content to file path to simulate user editing
  return os.WriteFile(path, []byte("# new content\n"), 0644)
}
```

## Cross-cutting checklist for contributors

- [ ] Does the new command accept a \*CmdDeps or options instead of global state?
- [ ] Are external effects (editor, network, FS) stubbed via injected collaborators for tests?
- [ ] Is context accepted and propagated for cancellable work?
- [ ] Are tests using MemoryRepo / FixedClock where appropriate?
- [ ] Does ApplyDefaults remain conservative; heavy initialization is explicit?
- [ ] Does the command return meaningful errors and avoid os.Exit inside library code?

## Further reading and next steps

- Inspect pkg/keg for MemoryRepo, Node, and typed errors to see how command tests can assert behavior via typed errors and memory-backed repositories.
- Consider adding small test helpers in pkg/keg/cmd/testutil to DRY common test setups (WithTestKeg, WithTestIO).
- If you want, I can produce a small test scaffold demonstrating one subcommand tested end-to-end with MemoryRepo and EditorRunner stubbing.
