#!/usr/bin/env bash
# Seed the sandbox with a keg fixture from test-env/fixtures/.
#
# Fixtures are copied into ~/.local/share/tapper/kegs/<name>/ inside the
# sandbox container. A minimal user config at ~/.config/tapper/config.yaml
# is created if absent so kegSearchPaths includes the fixture root and
# `tap list-kegs` discovers them.

set -euo pipefail

FIXTURE_DIR=/usr/local/src/tapper/test-env/fixtures
KEG_ROOT="${HOME}/.local/share/tapper/kegs"
CFG_DIR="${HOME}/.config/tapper"
CFG_FILE="${CFG_DIR}/config.yaml"

list_fixtures() {
    if [[ ! -d "${FIXTURE_DIR}" ]]; then
        echo "(no fixtures directory at ${FIXTURE_DIR})"
        return
    fi
    local found=0
    for f in "${FIXTURE_DIR}"/*/; do
        [[ -d "${f}" ]] || continue
        echo "  $(basename "${f}")"
        found=1
    done
    if [[ "${found}" -eq 0 ]]; then
        echo "  (none)"
    fi
}

usage() {
    cat <<USAGE
Usage: populate.sh <name|--all|--list>

Available fixtures:
$(list_fixtures)
USAGE
}

# A user that ran 'tap init' has their own config; appending may produce a
# duplicate kegSearchPaths key. Only seed config when absent. If present,
# emit a one-line hint about the path the user can add manually.
ensure_config() {
    if [[ -f "${CFG_FILE}" ]]; then
        if ! grep -q "share/tapper/kegs" "${CFG_FILE}"; then
            echo "[hint] ${CFG_FILE} exists; add '${KEG_ROOT}' to kegSearchPaths to discover fixtures." >&2
        fi
        return
    fi
    mkdir -p "${CFG_DIR}"
    cat > "${CFG_FILE}" <<EOF
kegSearchPaths:
  - ${KEG_ROOT}
EOF
}

populate_one() {
    local name="$1"
    local src="${FIXTURE_DIR}/${name}"
    local dst="${KEG_ROOT}/${name}"

    if [[ ! -d "${src}" ]]; then
        echo "Unknown fixture: ${name}" >&2
        echo "Available:" >&2
        list_fixtures >&2
        return 1
    fi
    if [[ -d "${dst}" ]]; then
        echo "[skip] ${name} already at ${dst}"
        return 0
    fi
    mkdir -p "${KEG_ROOT}"
    cp -r "${src}" "${dst}"
    chmod -R u+w "${dst}"
    echo "[ok]   ${name} -> ${dst}"
}

populate_all() {
    if [[ ! -d "${FIXTURE_DIR}" ]]; then
        echo "No fixtures directory at ${FIXTURE_DIR}" >&2
        return 1
    fi
    local any=0
    for f in "${FIXTURE_DIR}"/*/; do
        [[ -d "${f}" ]] || continue
        populate_one "$(basename "${f}")" || true
        any=1
    done
    if [[ "${any}" -eq 0 ]]; then
        echo "No fixtures to populate."
    fi
}

case "${1:-}" in
    "" | -h | --help | --list)
        usage
        ;;
    --all)
        ensure_config
        populate_all
        ;;
    *)
        ensure_config
        populate_one "$1"
        ;;
esac
