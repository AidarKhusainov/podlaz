#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd env getent git grep id journalctl mktemp runuser seq sleep sudo systemctl timeout

EVIDENCE_FILE="${E2E_ARTIFACT_DIR}/issue254-remote-client-acceptance.txt"
LOG_READER_USER="nobody"
LOG_READER_PRIMARY_GROUP="nogroup"
LOG_READER_ACCESS_GROUP="podlaz"

write_evidence() {
  local key="$1" value="$2"
  case "${key}${value}" in
    *$'\n'*|*$'\r'*) fail "invalid normalized issue 254 evidence" ;;
  esac
  printf '%s=%s\n' "${key}" "${value}" >>"${EVIDENCE_FILE}"
}

require_test_identity() {
  getent passwd "${LOG_READER_USER}" >/dev/null || fail "issue 254 acceptance requires the standard nobody account"
  getent group "${LOG_READER_PRIMARY_GROUP}" >/dev/null || fail "issue 254 acceptance requires the standard nogroup group"
  getent group "${LOG_READER_ACCESS_GROUP}" >/dev/null || fail "issue 254 acceptance requires the packaged podlaz group"
}

run_as_log_reader() {
  timeout --signal=TERM --kill-after=5s 20s \
    sudo -n runuser \
      -u "${LOG_READER_USER}" \
      -g "${LOG_READER_PRIMARY_GROUP}" \
      -G "${LOG_READER_ACCESS_GROUP}" \
      -- env LC_ALL=C HOME=/nonexistent /usr/bin/podlaz "$@"
}

run_as_outside_user() {
  timeout --signal=TERM --kill-after=5s 20s \
    sudo -n runuser \
      -u "${LOG_READER_USER}" \
      -g "${LOG_READER_PRIMARY_GROUP}" \
      -G "${LOG_READER_PRIMARY_GROUP}" \
      -- env LC_ALL=C HOME=/nonexistent /usr/bin/podlaz "$@"
}

verify_package_and_identity() {
  local build_commit version_output reader_groups
  build_commit="${GITHUB_SHA:-$(git rev-parse HEAD)}"
  version_output="$(mktemp "${E2E_TMP_ROOT}/issue254-version.XXXXXX")"
  /usr/bin/podlaz version >"${version_output}" 2>/dev/null || fail "issue 254 installed CLI version failed"
  grep -F -- "${build_commit}" "${version_output}" >/dev/null || fail "issue 254 installed CLI does not identify the tested commit"
  rm -f -- "${version_output}"

  systemctl is-active --quiet podlazd.service || fail "issue 254 acceptance requires the packaged podlazd service"
  reader_groups="$(sudo -n runuser \
    -u "${LOG_READER_USER}" \
    -g "${LOG_READER_PRIMARY_GROUP}" \
    -G "${LOG_READER_ACCESS_GROUP}" \
    -- id -nG)"
  grep -qw -- "${LOG_READER_ACCESS_GROUP}" <<<"${reader_groups}" || fail "issue 254 log reader is missing the daemon access group"
  if grep -qw -- systemd-journal <<<"${reader_groups}"; then
    fail "issue 254 log reader must not receive broad systemd-journal access"
  fi
  write_evidence package_provenance pass
  write_evidence ordinary_reader_without_systemd_journal pass
}

assert_bounded_tail_and_since() {
  local tail_output since_output
  tail_output="$(mktemp "${E2E_TMP_ROOT}/issue254-tail.XXXXXX")"
  since_output="$(mktemp "${E2E_TMP_ROOT}/issue254-since.XXXXXX")"

  run_as_log_reader status >/dev/null 2>&1 || fail "issue 254 could not generate a daemon status marker"
  sudo -n journalctl --sync

  run_as_log_reader logs --daemon >"${tail_output}" || fail "ordinary podlaz-group user could not read the bounded daemon log tail"
  grep -Fx 'podlaz daemon logs' "${tail_output}" >/dev/null || fail "daemon log tail did not render its stable header"
  grep -F 'status request' "${tail_output}" >/dev/null || fail "daemon log tail did not expose the generated daemon marker"
  write_evidence bounded_tail_as_ordinary_user pass

  run_as_log_reader logs --daemon --since 30s >"${since_output}" || fail "ordinary podlaz-group user could not read a bounded --since window"
  grep -Fx 'podlaz daemon logs' "${since_output}" >/dev/null || fail "daemon --since output did not render its stable header"
  grep -F 'status request' "${since_output}" >/dev/null || fail "daemon --since output did not expose the generated daemon marker"
  write_evidence bounded_since_as_ordinary_user pass

  rm -f -- "${tail_output}" "${since_output}"
}

assert_follow_streams_new_entries() {
  local follow_output follow_error follow_pid initial_count current_count
  follow_output="$(mktemp "${E2E_TMP_ROOT}/issue254-follow.stdout.XXXXXX")"
  follow_error="$(mktemp "${E2E_TMP_ROOT}/issue254-follow.stderr.XXXXXX")"

  timeout --signal=TERM --kill-after=5s 20s \
    sudo -n runuser \
      -u "${LOG_READER_USER}" \
      -g "${LOG_READER_PRIMARY_GROUP}" \
      -G "${LOG_READER_ACCESS_GROUP}" \
      -- env LC_ALL=C HOME=/nonexistent /usr/bin/podlaz logs --daemon --follow \
      >"${follow_output}" 2>"${follow_error}" &
  follow_pid=$!

  for _ in $(seq 1 50); do
    if grep -Fx 'podlaz daemon logs' "${follow_output}" >/dev/null 2>&1; then
      break
    fi
    sleep 0.1
  done
  grep -Fx 'podlaz daemon logs' "${follow_output}" >/dev/null || {
    kill "${follow_pid}" 2>/dev/null || true
    wait "${follow_pid}" 2>/dev/null || true
    fail "daemon follow stream did not start"
  }

  initial_count="$(grep -Fc 'status request' "${follow_output}" || true)"
  run_as_log_reader status >/dev/null 2>&1 || fail "issue 254 could not generate the follow marker"
  sudo -n journalctl --sync

  current_count="${initial_count}"
  for _ in $(seq 1 100); do
    current_count="$(grep -Fc 'status request' "${follow_output}" || true)"
    if (( current_count > initial_count )); then
      break
    fi
    sleep 0.1
  done

  kill "${follow_pid}" 2>/dev/null || true
  wait "${follow_pid}" 2>/dev/null || true
  if (( current_count <= initial_count )); then
    fail "daemon --follow did not stream a newly generated daemon entry"
  fi
  write_evidence follow_streams_new_entry pass

  rm -f -- "${follow_output}" "${follow_error}"
}

assert_outside_group_is_denied() {
  local outside_output outside_error
  outside_output="$(mktemp "${E2E_TMP_ROOT}/issue254-outside.stdout.XXXXXX")"
  outside_error="$(mktemp "${E2E_TMP_ROOT}/issue254-outside.stderr.XXXXXX")"

  if run_as_outside_user logs --daemon >"${outside_output}" 2>"${outside_error}"; then
    rm -f -- "${outside_output}" "${outside_error}"
    fail "user outside the podlaz daemon access group unexpectedly read daemon logs"
  fi
  write_evidence outside_group_denied pass
  rm -f -- "${outside_output}" "${outside_error}"
}

: >"${EVIDENCE_FILE}"
setup_isolated_xdg issue254-remote-client-acceptance
require_test_identity
verify_package_and_identity
assert_bounded_tail_and_since
assert_follow_streams_new_entries
assert_outside_group_is_denied
write_evidence acceptance pass
