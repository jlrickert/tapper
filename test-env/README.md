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
