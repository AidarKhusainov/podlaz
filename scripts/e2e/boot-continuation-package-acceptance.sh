#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/boot_continuation.sh
source "${SCRIPT_DIR}/lib/boot_continuation.sh"

require_cmd awk curl dpkg grep mktemp python3 runuser seq sleep sudo systemctl

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"
: "${PODLAZ_DEB_ARCH:=$(dpkg --print-architecture)}"

if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required"
fi
if [[ "${PODLAZ_DEB_ARCH}" != "$(dpkg --print-architecture)" ]]; then
  fail "boot-continuation package acceptance requires a native .deb"
fi

DEV_DEB="dist/podlaz_0.0.0~dev-1_linux_${PODLAZ_DEB_ARCH}.deb"
EVIDENCE="${E2E_ARTIFACT_DIR}/boot-continuation-acceptance.txt"
CONNECTED=false

boot_continuation_mask_multiline_sensitive "${PODLAZ_E2E_PROFILE_URI}"
boot_continuation_mask_multiline_sensitive "${PODLAZ_E2E_PROFILE_URI_LIST}"

write_evidence() {
  local key="$1"
  case "${key}" in
    *[!A-Za-z0-9_.-]*) fail "invalid boot-continuation evidence key" ;;
  esac
  printf '%s=pass\n' "${key}" >>"${EVIDENCE}"
}

reset_fixture_state() {
  if [[ "${CONNECTED}" == "true" ]]; then
    boot_continuation_run_podlaz disconnect >/dev/null 2>&1 || true
    CONNECTED=false
  fi
  boot_continuation_wait_for_inactive
  boot_continuation_run_podlaz autostart disable >/dev/null 2>&1 || true
  sudo -n rm -f -- \
    "${BOOT_CONTINUATION_ATTEMPT_PATH}" \
    "${BOOT_CONTINUATION_PRODUCT_REASON_PATH}"
}

assert_no_attempt() {
  if sudo -n test -e "${BOOT_CONTINUATION_ATTEMPT_PATH}"; then
    fail "boot autostart attempt exists when no attempt should be admitted"
  fi
}

cleanup() {
  local code=$?
  if [[ -x /usr/bin/podlaz ]]; then
    boot_continuation_run_podlaz disconnect >/dev/null 2>&1 || true
    boot_continuation_run_podlaz autostart disable >/dev/null 2>&1 || true
  fi
  sudo -n rm -f -- \
    "${BOOT_CONTINUATION_ATTEMPT_PATH}" \
    "${BOOT_CONTINUATION_PRODUCT_REASON_PATH}" >/dev/null 2>&1 || true
  exit "${code}"
}
trap cleanup EXIT

setup_isolated_xdg "boot-continuation-package-acceptance"
: >"${EVIDENCE}"
[[ -f "${DEV_DEB}" ]] || fail "boot-continuation candidate package is missing: ${DEV_DEB}"

sudo -n systemctl daemon-reload
sudo -n systemctl reset-failed podlazd.service || true
sudo -n systemctl start podlazd.service
boot_continuation_wait_for_daemon
boot_continuation_import_real_profile
boot_continuation_import_terminal_failure_profile

# Autostart disabled is pure future-boot policy: daemon replacement in this boot
# must remain cleanly disconnected and must not create an attempt record.
reset_fixture_state
boot_continuation_assert_autostart_line "Autostart: Disabled"
boot_continuation_restart_daemon
boot_continuation_wait_for_inactive
assert_no_attempt
write_evidence autostart_disabled_same_boot

# Enabling during this boot is intentionally next-boot-only. A same-boot daemon
# restart must not treat configuration time as a boot boundary.
boot_continuation_run_podlaz autostart enable --mode tun "${BOOT_CONTINUATION_PROFILE_ID}" >/dev/null
boot_continuation_assert_autostart_line "Autostart: Enabled for next boot"
boot_continuation_restart_daemon
boot_continuation_wait_for_inactive
assert_no_attempt
write_evidence autostart_enabled_next_boot

# The self-hosted Actions job cannot reboot mid-run. Change only the private
# manifest boot fence, with the daemon inactive, to model a later boot. Startup
# still executes the real packaged daemon and canonical Connect lifecycle.
boot_continuation_prepare_simulated_later_boot "boot-continuation-simulated-boot-b"
boot_continuation_restart_daemon
boot_continuation_wait_for_verified_active
CONNECTED=true
boot_continuation_assert_attempt succeeded
write_evidence autostart_enabled_next_boot

# A service restart must continue the same ordinary Network Session and consume
# no second boot autostart authority.
SUCCEEDED_FINGERPRINT="$(boot_continuation_attempt_fingerprint)"
boot_continuation_restart_daemon
boot_continuation_wait_for_verified_active
[[ "$(boot_continuation_attempt_fingerprint)" == "${SUCCEEDED_FINGERPRINT}" ]] || fail "daemon restart changed consumed autostart authority"
write_evidence daemon_restart_preserved_session

# Reinstall the candidate package while connected. Debian maintainer scripts
# replace the daemon process; the same Network Session must return verified and
# the consumed boot attempt must remain unchanged.
OLD_PID="$(boot_continuation_main_pid)"
sudo -n dpkg -i "${DEV_DEB}" >/dev/null
boot_continuation_wait_for_daemon
NEW_PID="$(boot_continuation_main_pid)"
[[ "${OLD_PID}" != "${NEW_PID}" ]] || fail "package upgrade did not replace podlazd"
boot_continuation_wait_for_verified_active
[[ "$(boot_continuation_attempt_fingerprint)" == "${SUCCEEDED_FINGERPRINT}" ]] || fail "package upgrade changed consumed autostart authority"
write_evidence package_upgrade_preserved_session

# Explicit disconnect is user intent, not permission to retry autostart. A later
# daemon restart in the same boot must remain disconnected.
boot_continuation_run_podlaz disconnect >/dev/null
CONNECTED=false
boot_continuation_wait_for_inactive
boot_continuation_restart_daemon
boot_continuation_wait_for_inactive
[[ "$(boot_continuation_attempt_fingerprint)" == "${SUCCEEDED_FINGERPRINT}" ]] || fail "explicit disconnect reset boot attempt authority"
write_evidence explicit_disconnect_no_restart_reconnect

# Build a fresh simulated boot around a deterministic unreachable example
# profile. The admitted attempt must converge to terminal, publish a concise
# typed reason, and remain consumed across a same-boot daemon restart.
boot_continuation_run_podlaz autostart disable >/dev/null
sudo -n rm -f -- "${BOOT_CONTINUATION_ATTEMPT_PATH}" "${BOOT_CONTINUATION_PRODUCT_REASON_PATH}"
boot_continuation_run_podlaz autostart enable --mode tun "${BOOT_CONTINUATION_FAILURE_PROFILE_ID}" >/dev/null
boot_continuation_prepare_simulated_later_boot "boot-continuation-simulated-boot-c"
boot_continuation_restart_daemon
boot_continuation_wait_for_inactive
boot_continuation_assert_attempt terminal connect_failed
boot_continuation_assert_product_reason "VPN connection could not be established safely"
write_evidence terminal_autostart_failure

TERMINAL_FINGERPRINT="$(boot_continuation_attempt_fingerprint)"
boot_continuation_restart_daemon
boot_continuation_wait_for_inactive
[[ "$(boot_continuation_attempt_fingerprint)" == "${TERMINAL_FINGERPRINT}" ]] || fail "terminal boot attempt changed after same-boot restart"
boot_continuation_assert_product_reason "VPN connection could not be established safely"
write_evidence terminal_no_same_boot_retry

# Disable affects future boots only and leaves the terminal attempt as the
# current-boot no-retry authority until the real next boot.
boot_continuation_run_podlaz autostart disable >/dev/null
boot_continuation_assert_autostart_line "Autostart: Disabled"
boot_continuation_assert_attempt terminal connect_failed

printf 'boot_continuation_acceptance=pass\n' >>"${EVIDENCE}"
