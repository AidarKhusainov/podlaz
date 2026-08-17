#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/exit_trap.sh
source "${SCRIPT_DIR}/lib/exit_trap.sh"

require_cmd awk date git grep ip journalctl mktemp nft python3 runuser sleep sudo systemctl timeout

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"

if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required"
fi

EVIDENCE_FILE="${E2E_ARTIFACT_DIR}/issue247-diagnostics-acceptance.txt"
PROFILE_ID=""
CONNECTED=false
STALE_LINK_CREATED=false
STALE_LINK_INDEX=""
ACTIVE_NFT_MISMATCH_CREATED=false
ACTIVE_NFT_MISMATCH_CHAIN="issue247_e2e_mismatch"

write_evidence() {
  local key="$1" value="$2"
  case "${key}${value}" in
    *$'\n'*|*$'\r'*) fail "invalid normalized issue 247 evidence" ;;
  esac
  printf '%s=%s\n' "${key}" "${value}" >>"${EVIDENCE_FILE}"
}

run_installed_podlaz_bounded() {
  local timeout_seconds="$1"
  shift
  timeout --signal=TERM --kill-after=5s "${timeout_seconds}" \
    sudo -n runuser -u "$(id -un)" -g podlaz -- env \
      LC_ALL=C \
      XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
      XDG_STATE_HOME="${XDG_STATE_HOME}" \
      XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
      /usr/bin/podlaz "$@"
}

remove_stale_test_link() {
  local current_index=""
  [[ "${STALE_LINK_CREATED}" == "true" ]] || return 0

  current_index="$(ip -o link show dev podlaz0 2>/dev/null | awk -F: 'NR == 1 {gsub(/[[:space:]]/, "", $1); print $1}')"
  if [[ -z "${current_index}" ]]; then
    STALE_LINK_CREATED=false
    STALE_LINK_INDEX=""
    return 0
  fi
  if [[ "${current_index}" != "${STALE_LINK_INDEX}" ]]; then
    printf 'issue 247 cleanup refused to delete a replaced podlaz0 identity\n' >&2
    return 1
  fi
  if ! ip tuntap show dev podlaz0 2>/dev/null | grep -Eq '^podlaz0:[[:space:]]+tun([[:space:]]|$)'; then
    printf 'issue 247 cleanup refused to delete a non-TUN podlaz0 identity\n' >&2
    return 1
  fi

  sudo -n ip link del dev podlaz0
  STALE_LINK_CREATED=false
  STALE_LINK_INDEX=""
}

remove_active_nft_mismatch() {
  local output error_output exit_code
  [[ "${ACTIVE_NFT_MISMATCH_CREATED}" == "true" ]] || return 0

  output="$(mktemp "${E2E_TMP_ROOT}/issue247-active-nft-cleanup.stdout.XXXXXX")"
  error_output="$(mktemp "${E2E_TMP_ROOT}/issue247-active-nft-cleanup.stderr.XXXXXX")"
  if sudo -n nft -y list chain inet podlaz "${ACTIVE_NFT_MISMATCH_CHAIN}" >"${output}" 2>"${error_output}"; then
    exit_code=0
  else
    exit_code=$?
  fi

  if (( exit_code != 0 )); then
    if grep -Eqi 'No such (file|table)|does not exist' "${error_output}"; then
      rm -f -- "${output}" "${error_output}"
      ACTIVE_NFT_MISMATCH_CREATED=false
      return 0
    fi
    rm -f -- "${output}" "${error_output}"
    printf 'issue 247 cleanup could not inspect the test nftables chain\n' >&2
    return 1
  fi

  if ! python3 - "${output}" "${ACTIVE_NFT_MISMATCH_CHAIN}" <<'PY'
import sys

path, chain = sys.argv[1], sys.argv[2]
with open(path, encoding="utf-8") as handle:
    lines = [line.strip() for line in handle if line.strip()]
expected = ["table inet podlaz {", f"chain {chain} {{", "}", "}"]
if lines != expected:
    raise SystemExit("test nftables chain identity/content changed")
PY
  then
    rm -f -- "${output}" "${error_output}"
    printf 'issue 247 cleanup refused to delete a changed test nftables chain\n' >&2
    return 1
  fi

  rm -f -- "${output}" "${error_output}"
  sudo -n nft delete chain inet podlaz "${ACTIVE_NFT_MISMATCH_CHAIN}"
  ACTIVE_NFT_MISMATCH_CREATED=false
}

cleanup() {
  local saved=$? cleanup_failed=0
  set +e
  if [[ "${CONNECTED}" == "true" ]]; then
    run_installed_podlaz_bounded 60s disconnect >/dev/null 2>&1 || cleanup_failed=1
    CONNECTED=false
  fi
  remove_active_nft_mismatch || cleanup_failed=1
  remove_stale_test_link || cleanup_failed=1
  if (( saved == 0 && cleanup_failed == 0 )); then
    write_evidence acceptance pass || cleanup_failed=1
  fi
  finish_exit_trap "${saved}" "${cleanup_failed}"
}
trap cleanup EXIT

first_profile_uri() {
  if [[ -n "${PODLAZ_E2E_PROFILE_URI}" ]]; then
    printf '%s\n' "${PODLAZ_E2E_PROFILE_URI}"
    return
  fi
  while IFS= read -r uri; do
    [[ -n "${uri}" ]] || continue
    printf '%s\n' "${uri}"
    return
  done <<<"${PODLAZ_E2E_PROFILE_URI_LIST}"
}

verify_package_provenance() {
  local build_commit version_output
  build_commit="${GITHUB_SHA:-$(git rev-parse HEAD)}"
  version_output="$(mktemp "${E2E_TMP_ROOT}/issue247-version.XXXXXX")"
  /usr/bin/podlaz version >"${version_output}" 2>/dev/null || fail "issue 247 installed CLI version failed"
  grep -F -- "${build_commit}" "${version_output}" >/dev/null || fail "issue 247 installed CLI does not identify the tested commit"
  rm -f -- "${version_output}"
  systemctl is-active --quiet podlazd.service || fail "issue 247 requires the packaged podlazd service"
  write_evidence package_provenance pass
}

import_profile_privately() {
  local uri="$1" output error_output
  output="$(mktemp "${E2E_TMP_ROOT}/issue247-profile-import.stdout.XXXXXX")"
  error_output="$(mktemp "${E2E_TMP_ROOT}/issue247-profile-import.stderr.XXXXXX")"
  if ! run_installed_podlaz_bounded 30s profile import "${uri}" >"${output}" 2>"${error_output}"; then
    rm -f -- "${output}" "${error_output}"
    fail "issue 247 profile import failed"
  fi
  PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${output}")"
  rm -f -- "${output}" "${error_output}"
  assert_nonempty "${PROFILE_ID}" "issue 247 imported profile id"
  mask_value "${PROFILE_ID}"
  write_evidence profile_import pass
}

assert_inactive_doctor_clean() {
  local phase="$1" output exit_code
  output="$(mktemp "${E2E_TMP_ROOT}/issue247-${phase}-inactive-doctor.XXXXXX")"
  set +e
  run_installed_podlaz_bounded 20s doctor >"${output}" 2>&1
  exit_code=$?
  set -e
  if (( exit_code != 0 )); then
    rm -f -- "${output}"
    fail "${phase}: inactive doctor returned non-zero"
  fi
  grep -Fx '[OK] stale-resources: no podlaz-owned resources found' "${output}" >/dev/null || {
    rm -f -- "${output}"
    fail "${phase}: inactive doctor does not report clean managed resources"
  }
  grep -Fx '[OK] startup-recovery-scan: startup recovery scan: clean inactive state' "${output}" >/dev/null || {
    rm -f -- "${output}"
    fail "${phase}: inactive doctor startup scan is not clean inactive"
  }
  if grep -F '[WARN] stale-resources:' "${output}" >/dev/null; then
    rm -f -- "${output}"
    fail "${phase}: inactive doctor unexpectedly reports stale resources"
  fi
  rm -f -- "${output}"
  write_evidence "inactive_doctor_${phase}" pass
}

assert_active_doctor_clean() {
  local phase="$1" output exit_code
  output="$(mktemp "${E2E_TMP_ROOT}/issue247-${phase}-active-doctor.XXXXXX")"
  set +e
  run_installed_podlaz_bounded 30s doctor >"${output}" 2>&1
  exit_code=$?
  set -e
  if (( exit_code != 0 )); then
    rm -f -- "${output}"
    fail "${phase}: active doctor returned non-zero"
  fi
  grep -Fx '[OK] stale-resources: managed resources match active lifecycle' "${output}" >/dev/null || {
    rm -f -- "${output}"
    fail "${phase}: active doctor does not accept exact transaction-owned resources"
  }
  grep -Fx '[OK] startup-recovery-scan: startup recovery scan: clean for active connection' "${output}" >/dev/null || {
    rm -f -- "${output}"
    fail "${phase}: active doctor startup scan does not use active lifecycle wording"
  }
  if grep -F 'clean inactive state' "${output}" >/dev/null || grep -F '[WARN] stale-resources:' "${output}" >/dev/null; then
    rm -f -- "${output}"
    fail "${phase}: active doctor contains a false inactive/stale diagnostic"
  fi
  rm -f -- "${output}"
  write_evidence "active_doctor_${phase}" pass
}

assert_active_status() {
  local phase="$1" output
  output="$(mktemp "${E2E_TMP_ROOT}/issue247-${phase}-active-status.XXXXXX")"
  if ! run_installed_podlaz_bounded 20s status >"${output}" 2>&1; then
    rm -f -- "${output}"
    fail "${phase}: active status returned non-zero"
  fi
  grep -Fx 'Connection: active' "${output}" >/dev/null || fail "${phase}: connection is not active"
  grep -Fx 'Transaction: committed' "${output}" >/dev/null || fail "${phase}: TUN transaction is not committed"
  grep -Fx 'Stale state: none' "${output}" >/dev/null || fail "${phase}: active status reports stale state"
  rm -f -- "${output}"
  write_evidence "active_status_${phase}" pass
}

assert_active_nft_mismatch_warns() {
  local output error_output exit_code
  output="$(mktemp "${E2E_TMP_ROOT}/issue247-active-nft-precheck.stdout.XXXXXX")"
  error_output="$(mktemp "${E2E_TMP_ROOT}/issue247-active-nft-precheck.stderr.XXXXXX")"
  if sudo -n nft list chain inet podlaz "${ACTIVE_NFT_MISMATCH_CHAIN}" >"${output}" 2>"${error_output}"; then
    rm -f -- "${output}" "${error_output}"
    fail "issue 247 active mismatch chain already exists"
  else
    exit_code=$?
  fi
  if (( exit_code == 0 )); then
    rm -f -- "${output}" "${error_output}"
    fail "issue 247 active mismatch precheck unexpectedly succeeded"
  fi
  if ! grep -Eqi 'No such (file|table)|does not exist' "${error_output}"; then
    rm -f -- "${output}" "${error_output}"
    fail "issue 247 could not prove the active mismatch chain is absent"
  fi
  rm -f -- "${output}" "${error_output}"

  sudo -n nft add chain inet podlaz "${ACTIVE_NFT_MISMATCH_CHAIN}"
  ACTIVE_NFT_MISMATCH_CREATED=true

  output="$(mktemp "${E2E_TMP_ROOT}/issue247-active-nft-mismatch-doctor.XXXXXX")"
  set +e
  run_installed_podlaz_bounded 30s doctor >"${output}" 2>&1
  exit_code=$?
  set -e
  if (( exit_code != 0 )); then
    rm -f -- "${output}"
    fail "active nftables mismatch doctor returned unexpected non-zero"
  fi
  grep -F '[WARN] stale-resources:' "${output}" >/dev/null || {
    rm -f -- "${output}"
    fail "active nftables mismatch did not produce a stale-resource warning"
  }
  grep -F 'nft table inet podlaz does not match active transaction exact composition' "${output}" >/dev/null || {
    rm -f -- "${output}"
    fail "active nftables mismatch was not classified by exact composition"
  }
  rm -f -- "${output}"

  remove_active_nft_mismatch || fail "issue 247 could not safely remove its active nftables mismatch"
  write_evidence active_nft_mismatch pass
}

assert_inactive_foreign_link_warns() {
  local output exit_code
  if ip link show dev podlaz0 >/dev/null 2>&1; then
    fail "issue 247 stale-resource acceptance requires podlaz0 to be absent before creating the test link"
  fi

  sudo -n ip tuntap add dev podlaz0 mode tun
  STALE_LINK_CREATED=true
  STALE_LINK_INDEX="$(ip -o link show dev podlaz0 | awk -F: 'NR == 1 {gsub(/[[:space:]]/, "", $1); print $1}')"
  assert_nonempty "${STALE_LINK_INDEX}" "issue 247 stale test link index"

  output="$(mktemp "${E2E_TMP_ROOT}/issue247-inactive-stale-doctor.XXXXXX")"
  set +e
  run_installed_podlaz_bounded 20s doctor >"${output}" 2>&1
  exit_code=$?
  set -e
  if (( exit_code != 0 )); then
    rm -f -- "${output}"
    fail "inactive stale-resource doctor returned unexpected non-zero"
  fi
  grep -F '[WARN] stale-resources: found interface podlaz0 exists' "${output}" >/dev/null || {
    rm -f -- "${output}"
    fail "inactive foreign-looking podlaz0 was not reported as stale"
  }
  rm -f -- "${output}"

  remove_stale_test_link || fail "issue 247 could not safely remove its exact stale test link"
  write_evidence inactive_stale_resource pass
}

assert_logs_since_mode_36h() {
  local mode="$1" header="$2" key="$3" output error_output exit_code
  output="$(mktemp "${E2E_TMP_ROOT}/issue247-logs-${mode}-36h.stdout.XXXXXX")"
  error_output="$(mktemp "${E2E_TMP_ROOT}/issue247-logs-${mode}-36h.stderr.XXXXXX")"
  set +e
  run_installed_podlaz_bounded 20s logs "--${mode}" --since 36h >"${output}" 2>"${error_output}"
  exit_code=$?
  set -e
  if (( exit_code != 0 )); then
    rm -f -- "${output}" "${error_output}"
    fail "installed podlaz logs --${mode} --since 36h failed"
  fi
  grep -Fx "${header}" "${output}" >/dev/null || {
    rm -f -- "${output}" "${error_output}"
    fail "installed podlaz logs --${mode} --since 36h did not render the expected header"
  }
  if grep -Eqi 'failed to parse timestamp|invalid argument.*since' "${error_output}"; then
    rm -f -- "${output}" "${error_output}"
    fail "installed podlaz logs --${mode} --since 36h leaked backend timestamp grammar"
  fi
  rm -f -- "${output}" "${error_output}"
  write_evidence "logs_since_36h_${key}" pass
}

assert_logs_lookback_is_bounded() {
  local baseline output error_output invocation_start
  baseline="$(mktemp "${E2E_TMP_ROOT}/issue247-lookback-baseline.XXXXXX")"
  output="$(mktemp "${E2E_TMP_ROOT}/issue247-lookback.stdout.XXXXXX")"
  error_output="$(mktemp "${E2E_TMP_ROOT}/issue247-lookback.stderr.XXXXXX")"

  for _ in 1 2 3; do
    run_installed_podlaz_bounded 10s status >/dev/null 2>&1 || fail "issue 247 could not create a bounded-lookback daemon journal marker"
  done
  sudo -n journalctl --sync
  sudo -n env LC_ALL=C journalctl --system --unit podlazd.service --since -10s --no-pager --output short >"${baseline}" 2>/dev/null || \
    fail "issue 247 could not inspect the private journal baseline"
  grep -F 'status request' "${baseline}" >/dev/null || fail "issue 247 journal baseline did not contain the generated status request"

  sleep 5
  invocation_start="$(date +%s)"
  run_installed_podlaz_bounded 20s logs --daemon --since 1s >"${output}" 2>"${error_output}" || \
    fail "installed podlaz logs --daemon --since 1s failed"
  grep -Fx 'podlaz daemon logs' "${output}" >/dev/null || fail "bounded lookback did not render the daemon header"

  if ! python3 - "${output}" "${invocation_start}" <<'PY'
import datetime as dt
import re
import sys
import time

path = sys.argv[1]
start = float(sys.argv[2])
# journalctl short timestamps have one-second resolution. Allow two seconds for
# scheduling/format conversion, but reject the deliberately generated marker
# that is at least five seconds old.
threshold = start - 3.0
pattern = re.compile(r"^(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+(\d{1,2})\s+(\d{2}):(\d{2}):(\d{2})\s")
months = {name: index for index, name in enumerate(("Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"), 1)}
now = dt.datetime.fromtimestamp(start)

with open(path, encoding="utf-8") as handle:
    for raw in handle:
        match = pattern.match(raw)
        if match is None:
            continue
        month, day, hour, minute, second = match.groups()
        candidates = []
        for year in (now.year - 1, now.year, now.year + 1):
            try:
                value = dt.datetime(year, months[month], int(day), int(hour), int(minute), int(second))
            except ValueError:
                continue
            candidates.append(time.mktime(value.timetuple()))
        if not candidates:
            raise SystemExit("could not interpret journal timestamp")
        timestamp = min(candidates, key=lambda candidate: abs(candidate - start))
        if timestamp < threshold:
            raise SystemExit(f"journal line predates bounded lookback: {raw.strip()}")
PY
  then
    rm -f -- "${baseline}" "${output}" "${error_output}"
    fail "podlaz logs did not enforce the requested short lookback"
  fi

  rm -f -- "${baseline}" "${output}" "${error_output}"
  write_evidence logs_since_lookback_bounded pass
}

assert_logs_follow_cancels_cleanly() {
  local output error_output exit_code
  output="$(mktemp "${E2E_TMP_ROOT}/issue247-follow.stdout.XXXXXX")"
  error_output="$(mktemp "${E2E_TMP_ROOT}/issue247-follow.stderr.XXXXXX")"

  set +e
  timeout --preserve-status --signal=INT --kill-after=3s 2s \
    sudo -n runuser -u "$(id -un)" -g podlaz -- env \
      LC_ALL=C \
      XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
      XDG_STATE_HOME="${XDG_STATE_HOME}" \
      XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
      /usr/bin/podlaz logs --daemon --since 1s --follow >"${output}" 2>"${error_output}"
  exit_code=$?
  set -e

  if (( exit_code != 130 )); then
    rm -f -- "${output}" "${error_output}"
    fail "logs --follow did not terminate cleanly on SIGINT (exit ${exit_code})"
  fi
  grep -Fx 'podlaz daemon logs' "${output}" >/dev/null || {
    rm -f -- "${output}" "${error_output}"
    fail "logs --follow did not start the requested initial lookback"
  }
  rm -f -- "${output}" "${error_output}"
  write_evidence logs_follow_cancel pass
}

assert_logs_since_invalid() {
  local value="$1" key="$2" output error_output exit_code
  output="$(mktemp "${E2E_TMP_ROOT}/issue247-invalid-since.stdout.XXXXXX")"
  error_output="$(mktemp "${E2E_TMP_ROOT}/issue247-invalid-since.stderr.XXXXXX")"
  set +e
  run_installed_podlaz_bounded 10s logs --since "${value}" >"${output}" 2>"${error_output}"
  exit_code=$?
  set -e
  if (( exit_code != 2 )); then
    rm -f -- "${output}" "${error_output}"
    fail "invalid logs --since value did not return usage exit code 2"
  fi
  if [[ -s "${output}" ]] || ! grep -F 'invalid logs --since duration' "${error_output}" >/dev/null; then
    rm -f -- "${output}" "${error_output}"
    fail "invalid logs --since value did not fail clearly before journal output"
  fi
  rm -f -- "${output}" "${error_output}"
  write_evidence "logs_since_invalid_${key}" pass
}

: >"${EVIDENCE_FILE}"
setup_isolated_xdg issue247-package-acceptance
verify_package_provenance
assert_inactive_doctor_clean initial

PROFILE_URI="$(first_profile_uri)"
assert_nonempty "${PROFILE_URI}" "issue 247 profile URI"
mask_value "${PROFILE_URI}"
import_profile_privately "${PROFILE_URI}"
unset PROFILE_URI

if ! run_installed_podlaz_bounded 90s connect --mode tun "${PROFILE_ID}" >/dev/null 2>&1; then
  fail "issue 247 TUN connect failed"
fi
CONNECTED=true
assert_active_status initial
assert_active_doctor_clean initial
assert_active_nft_mismatch_warns
assert_active_status restored
assert_active_doctor_clean restored

if ! run_installed_podlaz_bounded 60s disconnect >/dev/null 2>&1; then
  fail "issue 247 disconnect failed"
fi
CONNECTED=false
assert_inactive_doctor_clean after-disconnect

assert_inactive_foreign_link_warns
assert_logs_since_mode_36h daemon 'podlaz daemon logs' daemon
assert_logs_since_mode_36h core 'podlaz core logs' core
assert_logs_lookback_is_bounded
assert_logs_follow_cancels_cleanly
assert_logs_since_invalid '1h30m' compound
assert_logs_since_invalid '-1h' signed
assert_logs_since_invalid '0h' zero
assert_logs_since_invalid '721h' excessive
