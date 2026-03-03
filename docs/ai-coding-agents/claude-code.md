# Claude Code Skills And Agents

Claude Code reads project instructions from `CLAUDE.md` files and supports
custom skills (slash commands), subagents, and lifecycle hooks.

## CLAUDE.md

### File Locations

Claude Code loads instructions from multiple scopes. More specific locations
take precedence over broader ones.

```
~/.claude/CLAUDE.md                  # Personal, all projects
./CLAUDE.md  or  ./.claude/CLAUDE.md # Project, shared with team
./CLAUDE.local.md                    # Personal sandbox, not committed
```

Parent directory `CLAUDE.md` files are loaded automatically. Subdirectory files
load on demand when Claude edits files in that directory.

### Writing Guidelines

Keep each file under 200 lines. Long files consume context and reduce adherence.

```markdown
# Build And Test

- `go test ./...` — run all tests
- `go build ./cmd/tap` — build the CLI

# Code Style

- Go 1.25+, use gofmt
- Conventional commits: feat:, fix:, refactor:

# Project Structure

- `pkg/keg/` — KEG primitives
- `pkg/cli/` — Cobra command tree
- `docs/` — end-user documentation
```

### Importing Other Files

Use `@path` to pull in supporting content without bloating the main file:

```markdown
@README.md
@docs/architecture/README.md
@.claude/rules/testing.md
```

Relative paths resolve from the file containing the import. Maximum depth is
five levels.

### Path-Specific Rules

Place rule files under `.claude/rules/`. Add `paths` frontmatter to scope them
to specific file patterns:

```markdown
---
paths:
  - "pkg/keg/**/*.go"
---

# KEG Package Rules

- All node I/O goes through KegRepository, never the filesystem directly.
- Use MemoryRepo for tests.
```

Rules without `paths` frontmatter load at startup for every session.

## Skills (Custom Slash Commands)

Skills are the primary way to add custom `/commands` to Claude Code.

### File Structure

```
.claude/skills/skill-name/
  SKILL.md              # Required — instructions and frontmatter
  reference.md          # Optional — loaded on demand
  templates/            # Optional — supporting files
```

Project skills live under `.claude/skills/`. Personal skills live under
`~/.claude/skills/`.

### SKILL.md Format

Every skill needs a `SKILL.md` with YAML frontmatter:

```yaml
---
name: test-runner
description: Run tests and report failures. Use after code changes.
disable-model-invocation: true
user-invocable: true
allowed-tools: Bash, Read
model: sonnet
argument-hint: "[package]"
---

Run the test suite for $ARGUMENTS:

1. Execute `go test ./$ARGUMENTS/...`
2. Parse output for failures
3. Report only failing tests with error messages
```

### Frontmatter Fields

| Field | Default | Purpose |
|-------|---------|---------|
| `name` | directory name | Display name for `/name` invocation |
| `description` | — | When Claude should use this skill |
| `disable-model-invocation` | `false` | `true` prevents Claude from auto-invoking |
| `user-invocable` | `true` | `false` hides from `/` menu |
| `allowed-tools` | — | Tools usable without asking (e.g., `Read, Grep`) |
| `model` | `inherit` | `sonnet`, `opus`, `haiku`, or `inherit` |
| `argument-hint` | — | Autocomplete hint (e.g., `"[filename]"`) |
| `context` | — | `fork` to run in isolated subagent context |
| `agent` | — | Subagent type when `context: fork` (e.g., `Explore`) |

### String Substitutions

```
$ARGUMENTS       # All arguments as one string
$ARGUMENTS[0]    # First argument (0-indexed)
$0, $1, $2       # Shorthand positional args
```

### Injecting Shell Output

Prefix a backtick command with `!` to inject its output into the prompt:

```yaml
---
name: pr-summary
description: Summarize a pull request
context: fork
agent: Explore
allowed-tools: Bash(gh *)
---

PR diff: !`gh pr diff`
Changed files: !`gh pr diff --name-only`

Summarize this pull request.
```

## Subagents (Custom Agents)

Subagents are specialized AI assistants that run in isolated contexts, keeping
the main conversation focused.

### File Location

```
.claude/agents/agent-name.md      # Project scope, shared
~/.claude/agents/agent-name.md    # User scope, all projects
```

### Agent File Format

```yaml
---
name: code-reviewer
description: Review code changes for quality and security issues.
tools: Read, Grep, Glob, Bash
model: sonnet
maxTurns: 10
memory: project
---

You are a code reviewer. When invoked:

1. Run `git diff` to see recent changes
2. Review for clarity, security, and test coverage
3. Report findings organized by priority
```

### Agent Frontmatter Fields

| Field | Purpose |
|-------|---------|
| `name` | Unique identifier (lowercase, hyphens) |
| `description` | When Claude should delegate to this agent |
| `tools` | Allowed tools (e.g., `Read, Grep, Bash`) |
| `disallowedTools` | Tools to deny from the inherited set |
| `model` | `sonnet`, `opus`, `haiku`, or `inherit` |
| `maxTurns` | Maximum agentic turns before stopping |
| `memory` | Persistent memory: `user`, `project`, or `local` |
| `background` | `true` to run as a background task |
| `isolation` | `worktree` for isolated git worktree |
| `skills` | Skills to preload as context |
| `hooks` | Lifecycle hooks scoped to this agent |

### Agent Memory

Subagents can persist knowledge across sessions:

- `user` scope: `~/.claude/agent-memory/<name>/`
- `project` scope: `.claude/agent-memory/<name>/`
- `local` scope: `.claude/agent-memory-local/<name>/`

### Built-in Subagents

Claude Code includes these by default:

- **Explore** — fast read-only agent for codebase exploration
- **Plan** — research agent for gathering context before planning
- **general-purpose** — capable agent for complex multi-step tasks

## Hooks

Hooks run shell commands at specific lifecycle points. Configure them in
settings files.

### Available Events

| Event | When It Fires |
|-------|---------------|
| `PreToolUse` | Before a tool executes (can block it) |
| `PostToolUse` | After a tool succeeds |
| `UserPromptSubmit` | When you submit a prompt |
| `SessionStart` | When a session begins |
| `Stop` | When Claude finishes responding |
| `Notification` | When Claude needs attention |

### Configuration

Add hooks to any settings file (`.claude/settings.json`,
`.claude/settings.local.json`, or `~/.claude/settings.json`):

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit|Write",
        "hooks": [
          {
            "type": "command",
            "command": "gofmt -w $(jq -r '.tool_input.file_path' < /dev/stdin)"
          }
        ]
      }
    ]
  }
}
```

### Exit Codes

- **0** — action proceeds; stdout is added to context
- **2** — action is blocked; stderr becomes feedback to Claude
- **other** — action proceeds; stderr is logged

## Settings Files

### Locations And Precedence (Highest First)

1. Managed policy (organization-wide)
2. `.claude/settings.local.json` (personal, not committed)
3. `.claude/settings.json` (project, shared)
4. `~/.claude/settings.json` (user, all projects)

### Key Settings

```json
{
  "permissions": {
    "allow": [
      "Bash(go test:*)",
      "Bash(go build:*)",
      "Read(pkg/**)"
    ],
    "deny": [
      "Read(.env*)"
    ]
  },
  "hooks": { },
  "model": "claude-sonnet-4-6",
  "env": {
    "GOFLAGS": "-count=1"
  },
  "autoMemoryEnabled": true
}
```

## Project File Layout Reference

```
.claude/
  CLAUDE.md                # Project instructions (alternative to ./CLAUDE.md)
  settings.json            # Shared project settings
  settings.local.json      # Personal settings (gitignored)
  rules/                   # Modular instruction files
    code-style.md
    testing.md
  skills/                  # Custom slash commands
    skill-name/
      SKILL.md
  agents/                  # Custom subagents
    agent-name.md
  hooks/                   # Hook scripts
    protect-files.sh
  agent-memory/            # Subagent memory (project scope)
    agent-name/
```
