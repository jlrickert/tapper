# Codex CLI Configuration

OpenAI's Codex CLI reads project instructions from `AGENTS.md` files. It does
not have a plugin or skill system — configuration is limited to instruction
files and a global config.

## AGENTS.md

### How Codex Discovers Instructions

Codex automatically discovers `AGENTS.md` files by walking up the directory
tree from the current working directory to the repository root. No
configuration is needed to point Codex at them.

```
repo-root/
  AGENTS.md                    # Loaded for all work in this repo
  pkg/
    AGENTS.md                  # Also loaded when working in pkg/
    keg/
      AGENTS.md                # Also loaded when working in pkg/keg/
```

When working on files in a subdirectory, Codex reads all `AGENTS.md` files from
that directory up to the repo root, so deeper files can extend or override
root-level instructions.

### File Format

`AGENTS.md` is plain Markdown. There is no special schema or frontmatter. Write
natural language instructions describing the project:

```markdown
# Repository Guidelines

## Build And Test Commands

- `go test ./...` — run all tests
- `go build ./cmd/tap` — build the tap CLI
- `task test` — run tests via Taskfile

## Code Style

- Go 1.25+, use gofmt
- Conventional commits: feat:, fix:, refactor:
- Keep summaries under 72 characters

## Architecture

- `cmd/tap` — CLI entry point
- `pkg/keg` — KEG primitives and repository abstraction
- `pkg/cli` — Cobra command tree
- `pkg/tapper` — config resolution and high-level services
- `docs/` — end-user documentation

## Testing

- Place `_test.go` files next to source files
- Use table-driven tests named `Test<Component>`
- Run `go test ./pkg/...` before pushing
```

### Scoping Instructions By Directory

Use directory-level `AGENTS.md` files to provide scoped context. For example, a
`pkg/keg/AGENTS.md` can describe the storage model and repository interface
without cluttering the root instructions:

```markdown
# KEG Package

This package provides the core KEG library. All node I/O goes through the
KegRepository interface.

## Key Types

- Keg — high-level service for node operations
- KegRepository — storage contract (MemoryRepo for tests, FsRepo for disk)
- Node — lightweight struct wrapping a numeric ID
- Dex — in-memory indices rebuilt by scanning all nodes

## Testing

Use NewMemoryRepo() for fast tests. Call keg.Init(ctx) before Create.
```

## Global Configuration

### Config File Location

Codex stores global settings in `~/.codex/`:

```
~/.codex/
  config.yaml              # Global settings
```

### Approval Modes

Codex supports three levels of autonomy:

| Mode | Behavior |
|------|----------|
| `suggest` | Suggests changes but does not execute them |
| `auto-edit` | Edits files automatically, asks before running commands |
| `full-auto` | Edits files and runs commands without asking |

Set the mode in config or via CLI flag.

### Environment Variables

| Variable | Purpose |
|----------|---------|
| `OPENAI_API_KEY` | Required for authentication |
| `CODEX_HOME` | Override the config directory location |

### Model Selection

Select a model via config or the `--model` CLI flag. Common choices include
`o4-mini` and `o3`.

## Sandboxing

Codex uses OS-level sandboxing to restrict file and network access:

- macOS: `sandbox-exec` profiles
- Linux: namespace isolation

The sandbox prevents unintended side effects when running in `full-auto` mode.

## Comparison With Claude Code

| Aspect | Claude Code | Codex CLI |
|--------|------------|-----------|
| Instruction file | `CLAUDE.md` | `AGENTS.md` |
| Discovery | Walks directory tree | Walks directory tree |
| Custom commands | Skills (`.claude/skills/`) | None |
| Custom agents | Subagents (`.claude/agents/`) | None |
| Hooks | Lifecycle hooks in settings | None |
| Format | Markdown | Markdown |

## Recommended AGENTS.md For This Project

The `AGENTS.md` in the tapper repo root already provides build commands,
architecture overview, coding style, testing guidelines, and commit
conventions. When adding new packages or changing CLI behavior, update the
relevant section so both human contributors and Codex stay aligned.
