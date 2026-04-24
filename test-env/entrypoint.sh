#!/usr/bin/env bash
# Idempotent startup for the tapper sandbox container. On first boot, build
# tap/keg from the bind-mounted source. Subsequent boots skip the install and
# just exec the CMD.

set -euo pipefail

SENTINEL="${HOME}/.sandbox-initialized"
REPO="/workspace/tapper"

# Disable go.work so the in-container build uses the pinned cli-toolkit
# module version from go.mod instead of chasing ../cli-toolkit out of the
# bind-mount. Matches what tapper's CI does.
export GOWORK=off

if [[ ! -f "${SENTINEL}" && -d "${REPO}" ]]; then
    echo "[sandbox] First boot: installing tap and keg from ${REPO}..."
    (cd "${REPO}" && go install ./cmd/tap ./cmd/keg)
    touch "${SENTINEL}"
    echo "[sandbox] Ready. tap and keg are on PATH."
fi

exec "$@"
