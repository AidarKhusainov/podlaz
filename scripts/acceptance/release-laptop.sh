#!/usr/bin/env bash
set -Eeuo pipefail

if [[ "${EUID}" -ne 0 ]]; then
  printf 'release-laptop: must be run with sudo/root\n' >&2
  exit 2
fi
if [[ -z "${SUDO_USER:-}" || "${SUDO_USER}" == "root" ]]; then
  printf 'release-laptop: SUDO_USER must identify the original non-root user\n' >&2
  exit 2
fi

SOURCE="${BASH_SOURCE[0]}"
if [[ -L "${SOURCE}" ]]; then
  printf 'release-laptop: symlinked entrypoint is not allowed\n' >&2
  exit 2
fi
SCRIPT_DIR="$(cd -- "$(dirname -- "${SOURCE}")" && pwd -P)"
export PYTHONDONTWRITEBYTECODE=1
export PYTHONPATH="${SCRIPT_DIR}${PYTHONPATH:+:${PYTHONPATH}}"
exec python3 -m release_acceptance.cli "$@"
