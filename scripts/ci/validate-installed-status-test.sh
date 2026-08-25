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

expect_status_failure() {
  if validate_installed_daemon_status "$@"; then
    echo "expected packaged status validation to fail: $*" >&2
    exit 1
  fi
}

FAKE_STATUS_STDOUT=$'Status: Disconnected\nAutostart: Disabled' \
  FAKE_STATUS_EXIT_CODE=0 \
  validate_installed_daemon_status "${fake_status}" "${tmp_dir}/healthy"

FAKE_STATUS_STDOUT='Status: Disconnected' \
  FAKE_STATUS_EXIT_CODE=0 \
  expect_status_failure "${fake_status}" "${tmp_dir}/missing-autostart"

FAKE_STATUS_STDOUT=$'Status: Unknown\nAutostart: Disabled' \
  FAKE_STATUS_STDERR='podlaz: status found unhealthy lifecycle, stale, or incomplete state' \
  FAKE_STATUS_EXIT_CODE=3 \
  expect_status_failure "${fake_status}" "${tmp_dir}/unknown"

FAKE_STATUS_STDOUT=$'Status: Disconnected\nAutostart: Disabled\nDaemon: running' \
  FAKE_STATUS_EXIT_CODE=0 \
  expect_status_failure "${fake_status}" "${tmp_dir}/operator-detail"

FAKE_STATUS_STDOUT=$'Status: Disconnected\nAutostart: Disabled' \
  FAKE_STATUS_EXIT_CODE=2 \
  expect_status_failure "${fake_status}" "${tmp_dir}/unexpected-exit"
