#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/ci/validate-installed-status.sh
source "${script_dir}/validate-installed-status.sh"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT

fake_status="${tmp_dir}/fake-status"
cat >"${fake_status}" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ] || [ "$1" != status ]; then
  exit 2
fi

printf '%s\n' "${FAKE_STATUS_STDOUT:-}"
if [ -n "${FAKE_STATUS_STDERR:-}" ]; then
  printf '%s\n' "${FAKE_STATUS_STDERR}" >&2
fi
exit "${FAKE_STATUS_EXIT_CODE:-0}"
EOF
chmod +x "${fake_status}"

expect_failure() {
  if "$@"; then
    echo "expected command to fail: $*" >&2
    exit 1
  fi
}

FAKE_STATUS_STDOUT='Daemon: running' \
  FAKE_STATUS_EXIT_CODE=0 \
  validate_installed_daemon_status "${fake_status}" "${tmp_dir}/healthy"

FAKE_STATUS_STDOUT='Daemon: running' \
  FAKE_STATUS_STDERR='podlaz: status found stale or incomplete local state' \
  FAKE_STATUS_EXIT_CODE=3 \
  validate_installed_daemon_status "${fake_status}" "${tmp_dir}/stale"

expect_failure env \
  FAKE_STATUS_STDOUT='Connection: inactive' \
  FAKE_STATUS_EXIT_CODE=3 \
  bash -c 'source "$1"; validate_installed_daemon_status "$2" "$3"' _ \
  "${script_dir}/validate-installed-status.sh" \
  "${fake_status}" \
  "${tmp_dir}/missing-daemon"

expect_failure env \
  FAKE_STATUS_STDOUT='Daemon: running' \
  FAKE_STATUS_EXIT_CODE=2 \
  bash -c 'source "$1"; validate_installed_daemon_status "$2" "$3"' _ \
  "${script_dir}/validate-installed-status.sh" \
  "${fake_status}" \
  "${tmp_dir}/unexpected-exit"
