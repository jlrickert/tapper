# AI Coding Agent Configuration

This section documents how to configure AI coding assistants for a project.
The two primary tools covered are Claude Code (Anthropic) and Codex CLI
(OpenAI). Both read project-level instruction files to understand build
commands, code style, architecture, and workflows.

## Shared Concepts

Both tools follow the same general pattern:

1. A **project instruction file** checked into the repo root tells the agent
   about the project.
2. Instructions are plain **Markdown** with no special schema required.
3. Files are **discovered automatically** by walking up the directory tree.
4. Both support **hierarchical scoping** so subdirectories can override or
   extend root-level instructions.

## Key Differences

| Aspect | Claude Code | Codex CLI |
|--------|------------|-----------|
| Instruction file | `CLAUDE.md` | `AGENTS.md` |
| Global user config | `~/.claude/CLAUDE.md` + `settings.json` | `~/.codex/config.yaml` |
| Custom commands | Skills (`.claude/skills/`) | None (shell commands via instructions) |
| Custom agents | Subagents (`.claude/agents/`) | None |
| Hooks system | Lifecycle hooks in settings | None |
| Approval modes | Permission rules in settings | `suggest`, `auto-edit`, `full-auto` |
| Model | Claude (Anthropic) | OpenAI models (`o4-mini`, `o3`) |

## Guides

- [MCP Server Setup](mcp-setup.md) — configure `tap mcp` for permission-free
  KEG access from any MCP-compatible agent
- [Claude Code Skills And Agents](claude-code.md) — creating skills, subagents,
  hooks, and configuring `CLAUDE.md`
- [Codex CLI Configuration](codex-cli.md) — writing `AGENTS.md` and setting up
  Codex for a project
