# Zeke AI utility (zeke)

Zeke is a small CLI utility for calling AI APIs and composing responses from dynamic contexts. It is built around composable "contexts" that can pull information from files, shell commands, and included context sets. Zeke is designed to integrate smoothly with the [KEG utility](../5).

This document describes how to install, configure, and use zeke, plus developer notes for this Go project.

## Key features

- Composable contexts: combine files, shell outputs, and included context bundles
- Configurable API backends (OpenAI, etc.)
- Context defaults and overrides per-project and per-user
- Integrations with KEG for content flow (pipe zeke output into keg)
- Helper commands for managing contexts and config
- A "doctor" command for quick diagnostics

## Installation

Build and install (Go is required):

- From source:
  - go build ./cmd/zeke
  - or go install github.com/yourorg/zeke/cmd/zeke@latest

After install the binary should be available as `zeke` (or `zk` if you provide an alias wrapper).

Environment variables:

- OPENAI_API_KEY (or whatever your config references) — used when the config references an env var for API keys.

## CLI usage

Basic pattern:

- Use a named context:

  - `zk --context <context> [...prompt words]`
  - `zk -c <context> [...prompt words]`

- Use the default context:

  - `zk [...prompt words]`

- Read input from stdin:
  - `zk <context> [...words] < file`
  - `zk <context> [...words] < <(keg cat 123)` — example piping keg output into zeke

Manager / admin subcommands:

- `zeke context set <context>` — set default context
- `zeke context list` — list available contexts
- `zeke config init` — create starter config at ~/.config/zeke/zeke.yaml or project
- `zeke config edit` — open config for editing
- `zeke doctor` — run checks / diagnostics

Examples:

- `zk -c zeke-config "create a concise commit message for this change"`
- `zk "summarize the package responsibilities" < README.md`
- `zk patch-note "create a patch note that is part of task xyz" | keg create`

Notes:

- The `zk` shorthand is commonly used as the frontend CLI name; your installation may provide `zeke` instead.

## Integration with KEG

Zeke is intended to flow into KEG (Knowledge Exchange Graph) workflows:

- Pipe zeke output into KEG create/commit commands:

  - `zk "draft note about X" | keg create`
  - `zk patch-note "..." | keg c`

- Contexts can run `keg` commands to produce dynamic context content. Example in config:
  - shell:
    - keg cat --tags=zeke

This lets zeke incorporate the current KEG node contents or index as part of its context.

## Configuration

Zeke reads configuration from YAML. Default locations:

- User config: ~/.config/zeke/zeke.yaml
- Project config: GIT_PROJECT/zeke.yaml

Loading order:

1. User config (~/.config/zeke/zeke.yaml)
2. Project config (GIT_PROJECT/zeke.yaml)

Project config values can override the user config. Context definitions can reference included context bundles; when items are included, the included context names are prefixed by default with the include key (to avoid collisions) — see "Include mechanics and merging" below for how to control this.

Important config keys (examples):

```yaml
version: "config file version"
includes:
  files: "define named include sets (file path or glob)"
roles: "named role definitions used by contexts"
default-model: "e.g. gpt-5-mini"
default-context: "default context to use"
apis:
  openai:
    base-url: ""
    api-key-env: "name of environment variable"
    models: "mapping of model aliases"
mcp-servers: "local helper server definitions (e.g., for GitHub MCP server)"
contexts:
  description: "top-level named contexts; each context can include:"
  shell: "list of shell commands to run and include output"
  files: "file globs or paths to include"
  role: "overrides for that context"
  api: "overrides for that context"
  model: "overrides for that context"
  contexts: "references to included context sets (prefixed if from includes)"
```

Sample snippets from a typical zeke.yaml:

```yaml
default-model: gpt-5-mini
apis:
  openai:
    api-key-env: OPENAI_API_KEY
contexts:
  zeke-design
    shell:
      - "keg cat --tags=zeke"
    files:
      - "pkg/"
  zeke-config
    role: gitcommit
    api: openai
    model: gpt-5-mini
    files:
      - "./pkg/zeke/*"
    contexts:
      - "@ecw/go-config" # example of an included context reference
```

### Include mechanics and merging

Zeke supports "includes" — named references to external files or globs that can introduce contexts, roles, and other config. To make inclusion safe and predictable, the loader supports explicit merge controls.

Common include fields:

- includes.<name>.file — a concrete file path
- includes.<name>.glob / find — glob or find pattern
- includes.<name>.key — optional key used when prefixing included context names (e.g., "zeke")
- includes.<name>.merge — list of top-level keys to merge into the root config (e.g., ["contexts", "roles"])
- includes.<name>.merge-prefix — boolean (default: true). If true, included context names are prefixed with the include key (or include name); set to false to flatten names into the root.

Behavior summary:

- If includes.<name>.merge contains "contexts", the loader will locate contexts in the included file and merge them into the project's top-level contexts.
- By default, included context names are prefixed to prevent name collisions. Example: with key: zeke and an included context "design", it becomes "zeke.design".
- Setting merge-prefix: false flattens names into the root (careful — may overwrite existing contexts).
- The loader looks for contexts in common locations inside the included content:
  - top-level `contexts`
  - `zeke.contexts` (KEG-style embedding)
- Be explicit in includes.<name>.merge to avoid accidentally merging unrelated keys. Prefer lists like ["contexts", "roles"] rather than a catch-all "all".

Example: include a KEG file and merge contexts and roles with prefixing (safe default):

```yaml
includes:
  docs:
    key: zeke
    file: ./docs/keg
    merge: ["contexts", "roles"]
    merge-prefix: true
```

To merge contexts without prefixing (flatten):

```yaml
includes:
  docs:
    key: zeke
    file: ./docs/keg
    merge: ["contexts"]
    merge-prefix: false
```

Loader recommendations:

- Resolve include file/glob paths, parse YAML, and look for candidate merge targets (contexts, roles, etc.).
- When merging contexts, preserve structure and warn on name collisions. Do not silently overwrite local contexts; require explicit override or a clear merge-prefix:false decision.
- Add tests for merging behavior (with and without prefixing and for KEG-style zeke: blocks).

## Contexts and how they work

A context is the unit zeke uses to build the prompt it sends to the AI. A context can gather content from:

- files: read file contents, file glob patterns, or directories
- shell: run shell commands and include their stdout
- other contexts: include previously defined context bundles

When composing the prompt:

- Zeke will prefix included items with the include name by default to avoid collisions (configurable via merge-prefix)
- Files and shell outputs are concatenated (subject to length limits) and sent with the user's instruction

Context precedence:

- Values defined directly on the context take precedence over defaults in roles or API sections
- Per-run flags (like --context) override the default context

## Development notes (Go project)

Project type: go

This repo is a Go project. Useful notes for contributors:

- Layout:
  - cmd/zeke — main CLI entry
  - pkg/ — core packages (context handling, config, api clients)
  - internal/ — internal helpers (if present)
- To build: go build ./cmd/zeke
- To run locally: go run ./cmd/zeke --help
- Unit tests: go test ./...
- The config uses YAML; tests and examples under docs/ and pkg/ will help when implementing contexts and includes.

Implementation pointers for include merging

- The loader should:
  - resolve include file/glob paths and parse YAML
  - locate candidate nodes under `contexts` or `zeke.contexts`
  - for each requested merge target (e.g., contexts, roles), merge items into the root namespace
  - apply prefixing if merge-prefix is true (use includes.<name>.key when present, otherwise the include name)
  - warn or require explicit confirmation before overwriting existing items
- Add tests for:
  - merging with prefix (default)
  - merging with merge-prefix: false (flatten)
  - merging from KEG-style files where zeke.\* sits under a nested key

Paths referenced in zeke.yaml (for this repo):

- contexts reference files under ./pkg/ and ./pkg/zeke/\* — those are good places to look for code that assembles prompt contexts.

## Troubleshooting

- zeke doctor — run the built-in diagnostics (checks config syntax, API key presence, basic connectivity)
- Ensure OPENAI_API_KEY (or other referenced env vars) is set when using the OpenAI backend
- If contexts are missing or empty:
  - verify includes are resolving (glob and file paths)
  - check loading order (user vs project config)
  - run zeke context list to enumerate available contexts
  - verify includes.<name>.merge contains "contexts" if you expect contexts from the included file to be merged
  - check merge-prefix setting if you can't find a context by the expected name

## Commands quick reference

- `zk [-c|--context <context>] [prompt]` — run zeke with a context
- `zeke context set <context>` — set default context
- `zeke context list` — list defined contexts
- `zeke config init` — generate a starter config
- `zeke config edit` — edit active config
- `zeke doctor` — diagnostics
- `zk ... | keg create / keg c` — pipe AI output into KEG

## Contributing

- Follow repository CONTRIBUTING.md (if present)
- Add tests for new features
- Keep config compatibility in mind (version field in zeke.yaml)
- When changing context merging rules, update docs and tests

---

Appendix — example files referenced in this repo

docs/keg (example excerpt — shows includes.merge usage):

```text
updated: 2025-08-08 04:46:24Z
kegv:    2023-01

title:   KEG Worklog for tapper
url:     git@github.com:jlrickert/tapper.git
creator: git@github.com:jlrickert/jlrickert.git
state:   living

summary: |
  👋 Hey there! The KEG community welcomes you. This is an initial
  sample `keg` file. ...

links:
  - alias: jlrickert
    url: keg.jlrickert.me/@jlrickert/public

zeke:
  includes:
    docs:
      key: zeke
      find: **/meta.yaml
      merge: ["contexts", "roles"]
      merge-prefix: true

indexes:
  - file: dex/changes.md
    summary: latest changes
  - file: dex/nodes.tsv
    summary: all nodes by id
```

zeke.yaml (project example):

```yaml
version: 1
includes:
  docs:
    key: zeke
    file: ./docs/keg
    merge: ["contexts", "roles"]
    merge-prefix: true
  files:
    user:
      file: ~/.config/zeke/zeke.yaml
      merge: ["contexts", "roles"]
      merge-prefix: user
    ecw:
      glob: ~/repos/bitbucket.org/jared52/zet/docs/**/meta.yaml
roles:
  gitcommit:
    - git comment agent
default-model: gpt-5-mini
apis:
  openai:
    base-url: https://api.openai.com/v1
    api-key-env: OPENAI_API_KEY
    models:
      gpt-5-mini:
        alias:
          - gpt-5-mini
mcp-servers:
  github:
  command: github-mcp-server
  args:
    - stdio
    - --read-only
contexts:
  zeke-design:
    shell:
      - keg cat --tags=zeke
    files:
      - pkg/
  zeke-config:
    role: gitcommit
    api: openai
    model: gpt-5-mini
    files:
      - ./pkg/zeke/*
    contexts:
      - "@ecw/go-config"
```

If you'd like, I can output this as a patch suitable for git or produce a minimal unit-test sketch demonstrating the include/merge behavior. Which would you prefer?
