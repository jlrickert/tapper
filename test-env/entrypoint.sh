#!/usr/bin/env bash
# Idempotent startup for the tapper sandbox container. On first boot, build
# tap from the bind-mounted source. Subsequent boots skip the install and
# just exec the CMD.

set -euo pipefail

SENTINEL="${HOME}/.sandbox-initialized"
REPO="/usr/local/src/tapper"

# Disable go.work so the in-container build uses the pinned cli-toolkit
# module version from go.mod instead of chasing ../cli-toolkit out of the
# bind-mount. Matches what tapper's CI does.
#
# `${GOWORK-off}` only falls back when GOWORK is unset, not when it is
# set-but-empty. The work-mode compose overlay sets GOWORK="" on purpose
# so the workspace file at /workspace/tapper/go.work is honored.
export GOWORK="${GOWORK-off}"

if [[ ! -f "${SENTINEL}" && -d "${REPO}" ]]; then
    echo "[sandbox] First boot: installing tap from ${REPO}..."
    # -buildvcs=false: source is bind-mounted ro and on macOS the .git
    # directory's host uid doesn't match the in-container jlrickert,
    # so Go's VCS stamping fails with exit 128 and crashes the
    # entrypoint into a restart loop. Sandbox binaries don't need
    # VCS stamps anyway.
    (cd "${REPO}" && go install -buildvcs=false ./cmd/tap)

    # Drop Cobra-generated completion files into the system zsh site-functions
    # dir (already in default fpath, chown'd to jlrickert in the Dockerfile).
    # task sandbox:rebuild-tap mirrors this regeneration step for binary
    # updates after first boot.
    COMPDIR="/usr/local/share/zsh/site-functions"
    "${HOME}/go/bin/tap" completion zsh > "${COMPDIR}/_tap"

    touch "${SENTINEL}"
    echo "[sandbox] Ready. tap is on PATH; completion installed."
fi

exec "$@"
