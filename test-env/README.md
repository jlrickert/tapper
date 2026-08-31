# tapper sandbox

## Purpose

An isolated Ubuntu 24.04 container for testing tapper as a real user would
encounter it. The tapper source is bind-mounted **read-only** at
`/usr/local/src/tapper` purely as a build input -- the interactive shell
lands in the user's home (`/home/jlrickert`) with no project context, so
remote bootstrap and Hub workflows behave the same as on a vanilla machine.

Go module and build caches live in named volumes for speed. KEG data remains on
the configured remote Hub.

## Prerequisites

- Docker Desktop (or any Docker Engine 20.10+ with the `compose` plugin).
- [go-task](https://taskfile.dev) v3.x on the host.

## Quickstart

```sh
task sandbox:build     # one-time image build
task sandbox:up        # start the container in the background
task sandbox:shell     # drop into an interactive zsh login shell
```

## Task reference

| Task                            | What it does                                              |
| ------------------------------- | --------------------------------------------------------- |
| `task sandbox:build`            | Build or refresh the sandbox image.                       |
| `task sandbox:up`               | Start the container in the background.                    |
| `task sandbox:down`             | Stop and remove the container (keeps named volumes).      |
| `task sandbox:restart`          | Bounce the running container.                             |
| `task sandbox:rebuild`          | Rebuild the image and recreate the container.             |
| `task sandbox:shell`            | Interactive zsh login shell inside the container.         |
| `task sandbox:exec`             | Run an arbitrary command (`task sandbox:exec -- ls -la`). |
| `task sandbox:logs`             | Follow container logs.                                    |
| `task sandbox:status`           | Show container state.                                     |
| `task sandbox:rebuild-tap`      | Reinstall `tap` from the bind-mounted source.             |
| `task sandbox:refresh-dotfiles` | Rebuild image against live dotfiles HEAD; recreate.       |
| `task sandbox:test`             | Run `go test ./...` inside the container.                 |
| `task sandbox:clean`            | Remove container AND named volumes (nukes caches).        |

## Work mode (local cli-toolkit)

The default `sandbox` mirrors CI: builds use the `go.mod`-pinned cli-toolkit
release. To iterate against a local cli-toolkit working tree instead, use
the parallel `sandbox-work` service. It bind-mounts the cli-toolkit checkout
at `/usr/local/src/cli-toolkit` (read-only) and leaves `GOWORK` unset so the
host's `go.work` (referencing `../cli-toolkit`) is honored inside the
container.

| Task                            | What it does                                                    |
| ------------------------------- | --------------------------------------------------------------- |
| `task sandbox:up-work`          | Start the work-mode container; bootstraps `go.work` if missing. |
| `task sandbox:shell-work`       | Interactive zsh login shell in the work-mode container.         |
| `task sandbox:exec-work`        | Run an arbitrary command in the work-mode container (`-- ...`). |
| `task sandbox:test-work`        | `go test ./...` against the local cli-toolkit.                  |
| `task sandbox:rebuild-tap-work` | Reinstall `tap` linking the local cli-toolkit.                  |
| `task sandbox:down-work`        | Stop and remove the work-mode container (keeps named volumes).  |

Prerequisites:

- cli-toolkit checked out at `../../cli-toolkit` relative to `test-env/`
  (i.e., a sibling of the tapper repo). Override with the
  `CLI_TOOLKIT_PATH` host environment variable if it lives elsewhere.
- The work-mode container is **separate** from the default `sandbox` and
  has its own first-boot install of `tap`. Named caches (Go module,
  Go build, tapper state) are shared with `sandbox`.

## Inside the container

- Working directory at shell start: `/home/jlrickert` (the user's home).
- Source (build input only): `/usr/local/src/tapper` (read-only bind mount).
  Host edits appear live; the sandbox cannot mutate the host tree.
- `GOPATH=/home/jlrickert/go`, `GOCACHE=/home/jlrickert/.cache/go-build`
  (both backed by named volumes).
- Tapper state: `~/.local/state/tapper` (named volume).
- `tap` is on `$PATH` after the first boot, with zsh tab completion registered
  automatically (the entrypoint drops a Cobra-generated `_tap` file into
  `/usr/local/share/zsh/site-functions/`, which is in zsh's default
  fpath). The dir is `chown`d to `jlrickert` in the Dockerfile so the
  unprivileged user can write there.
- Personal dotfiles packages installed: `common-shell` (transitive),
  `zsh`, `zellij`. Source lives at
  `/home/jlrickert/.local/state/dots/taps/jlrickert/`. To add or remove
  packages, edit the `dots install` line in `test-env/Dockerfile` and
  `task sandbox:rebuild`.

## Updating dotfiles

The Dockerfile pins the dotfiles checkout via `ARG DOTFILES_REV=<sha>` so
layer caching is deterministic. Two ways to refresh:

- **Track HEAD live:** `task sandbox:refresh-dotfiles` resolves
  `git@github.com/jlrickert/dotfiles HEAD`, rebuilds the image with that
  SHA as `DOTFILES_REV`, and recreates the container. No Dockerfile edit.
- **Pin a specific SHA:** edit `DOTFILES_REV` in `test-env/Dockerfile` to
  the SHA you want, then `task sandbox:rebuild`. Use this when committing
  a known-good dotfiles version alongside other sandbox changes.

The same shape applies to `DOTS_REV` (the `dots` binary itself), bumped
manually when a newer release lands.

## Caveats

- First `task sandbox:up` is slow: downloads Ubuntu base, installs Go, runs
  `dots init`, and performs the initial `go install ./cmd/tap`.
- Host edits appear live under `/usr/local/src/tapper`, but rebuilt
  binaries only land on `$PATH` after `task sandbox:rebuild-tap`.
- `task sandbox:clean` destroys the Go module cache, build cache, and tapper
  state; expect the next `up` to behave like a first boot.
- The source bind is read-only -- write inside the container ends up in
  the container's writable layer, not on the host. Edit on the host.

## Troubleshooting

- `tap: command not found` inside the container -> run
  `task sandbox:rebuild-tap`.
- Stale image after editing the Dockerfile -> `task sandbox:rebuild`.
- Everything wedged -> `task sandbox:clean && task sandbox:up`.
