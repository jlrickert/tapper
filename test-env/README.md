# tapper sandbox

## Purpose

An isolated Ubuntu 24.04 container preloaded with the Go toolchain, common dev
tooling, and the user's `dots`-bootstrapped dotfiles. The tapper repo is bind
mounted at `/workspace/tapper` so host edits appear live inside the container,
while Go module and build caches live in named volumes for speed.

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

| Task                       | What it does                                              |
| -------------------------- | --------------------------------------------------------- |
| `task sandbox:build`       | Build or refresh the sandbox image.                       |
| `task sandbox:up`          | Start the container in the background.                    |
| `task sandbox:down`        | Stop and remove the container (keeps named volumes).      |
| `task sandbox:restart`     | Bounce the running container.                             |
| `task sandbox:rebuild`     | Rebuild the image and recreate the container.             |
| `task sandbox:shell`       | Interactive zsh login shell inside the container.         |
| `task sandbox:exec`        | Run an arbitrary command (`task sandbox:exec -- ls -la`). |
| `task sandbox:logs`        | Follow container logs.                                    |
| `task sandbox:status`      | Show container state.                                     |
| `task sandbox:rebuild-tap` | Reinstall `tap` and `keg` from the bind-mounted source.   |
| `task sandbox:test`        | Run `go test ./...` inside the container.                 |
| `task sandbox:clean`       | Remove container AND named volumes (nukes caches).        |

## Work mode (local cli-toolkit)

The default `sandbox` mirrors CI: builds use the `go.mod`-pinned cli-toolkit
release. To iterate against a local cli-toolkit working tree instead, use
the parallel `sandbox-work` service. It bind-mounts the cli-toolkit checkout
at `/workspace/cli-toolkit` and leaves `GOWORK` unset so the host's `go.work`
(referencing `../cli-toolkit`) is honored inside the container.

| Task                            | What it does                                                    |
| ------------------------------- | --------------------------------------------------------------- |
| `task sandbox:up-work`          | Start the work-mode container; bootstraps `go.work` if missing. |
| `task sandbox:shell-work`       | Interactive zsh login shell in the work-mode container.         |
| `task sandbox:exec-work`        | Run an arbitrary command in the work-mode container (`-- ...`). |
| `task sandbox:test-work`        | `go test ./...` against the local cli-toolkit.                  |
| `task sandbox:rebuild-tap-work` | Reinstall `tap`/`keg` linking the local cli-toolkit.            |
| `task sandbox:down-work`        | Stop and remove the work-mode container (keeps named volumes).  |

Prerequisites:

- cli-toolkit checked out at `../../cli-toolkit` relative to `test-env/`
  (i.e., a sibling of the tapper repo). Override with the
  `CLI_TOOLKIT_PATH` host environment variable if it lives elsewhere.
- The work-mode container is **separate** from the default `sandbox` and
  has its own first-boot install of `tap`/`keg`. Named caches (Go module,
  Go build, tapper state) are shared with `sandbox`.

## Inside the container

- Repo: `/workspace/tapper` (bind mount, read-write, host edits appear live).
- Dotfiles: `~/dots-config` with `dots` already initialized.
- `GOPATH=/home/jlrickert/go`, `GOCACHE=/home/jlrickert/.cache/go-build`
  (both backed by named volumes).
- Tapper state: `~/.local/state/tapper` (named volume).
- `tap` and `keg` are on `$PATH` after the first boot.

## Caveats

- First `task sandbox:up` is slow: downloads Ubuntu base, installs Go, runs
  `dots init`, and performs the initial `go install ./cmd/tap ./cmd/keg`.
- Host edits appear live under `/workspace/tapper`, but rebuilt binaries only
  land on `$PATH` after `task sandbox:rebuild-tap`.
- `task sandbox:clean` destroys the Go module cache, build cache, and tapper
  state; expect the next `up` to behave like a first boot.

## Troubleshooting

- `tap: command not found` inside the container -> run
  `task sandbox:rebuild-tap`.
- Stale image after editing the Dockerfile -> `task sandbox:rebuild`.
- Everything wedged -> `task sandbox:clean && task sandbox:up`.
