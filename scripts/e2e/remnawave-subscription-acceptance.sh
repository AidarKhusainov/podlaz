#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/private_command.sh
source "${SCRIPT_DIR}/lib/private_command.sh"
# shellcheck source=lib/package_provenance.sh
source "${SCRIPT_DIR}/lib/package_provenance.sh"
# shellcheck source=remnawave-fixture.sh
source "${SCRIPT_DIR}/remnawave-fixture.sh"

require_cmd apt cmp cp dpkg dpkg-deb jq sha256sum stat sudo systemctl

EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-${GITHUB_SHA:-}}"
CANDIDATE_DEB=""
CANDIDATE_SHA256=""
PACKAGE_INSTALLED=0
LOGIN_USER="$(id -un)"
FIRST_XDG="${E2E_TMP_ROOT}/remnawave-client-primary"
SECOND_XDG="${E2E_TMP_ROOT}/remnawave-client-secondary"
REPORT="${E2E_ARTIFACT_DIR}/remnawave-subscription.txt"
EVIDENCE_KEYS=(
  remnawave.subscription_import
  remnawave.subscription_refresh
  remnawave.hwid_registration
  remnawave.hwid_stable
  remnawave.hwid_device_limit
  remnawave.rejected_state_preserved
  fixture.cleanup
)
FAILURE_CLASS=diagnostic_unknown
FAILURE_STEP=bootstrap
REPORT_FINALIZED=false

mark_failure() {
  local class="$1" step="$2"
  case "${class}" in
    product|remnawave|fixture|infrastructure|capability|diagnostic_unknown|none) ;;
    *) class=diagnostic_unknown ;;
  esac
  FAILURE_CLASS="${class}"
  FAILURE_STEP="${step//[^A-Za-z0-9_.-]/_}"
}

evidence_recorded() {
  grep -q "^$1=" "${REPORT}" 2>/dev/null
}

record_evidence() {
  local key="$1" value="$2"
  [[ "${key}" =~ ^[a-z0-9_.-]+$ ]] || fail "invalid Remnawave subscription evidence key"
  case "${value}" in pass|fail) ;; *) fail "invalid Remnawave subscription evidence value" ;; esac
  ! evidence_recorded "${key}" || fail "duplicate Remnawave subscription evidence key: ${key}"
  printf '%s=%s\n' "${key}" "${value}" >>"${REPORT}"
}

record_if_missing() {
  evidence_recorded "$1" || record_evidence "$1" "$2"
}

finalize_report() {
  local key
  [[ "${REPORT_FINALIZED}" == false ]] || return 0
  for key in "${EVIDENCE_KEYS[@]}"; do
    record_if_missing "${key}" fail
  done
  printf 'failure.class=%s\n' "${FAILURE_CLASS}" >>"${REPORT}"
  printf 'failure.step=%s\n' "${FAILURE_STEP}" >>"${REPORT}"
  REPORT_FINALIZED=true
}

validate_candidate() {
  local path="$1" arch
  [[ -f "${path}" && ! -L "${path}" ]] || fail "candidate package must be a regular non-symlink file"
  [[ "$(dpkg-deb --field "${path}" Package)" == podlaz ]] || fail "candidate package is not podlaz"
  arch="$(dpkg-deb --field "${path}" Architecture)"
  [[ "${arch}" == "$(dpkg --print-architecture)" ]] || fail "candidate package architecture does not match runner"
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "PODLAZ_E2E_CANDIDATE_COMMIT must be an exact 40-hex commit"
  CANDIDATE_DEB="$(readlink -f -- "${path}")"
}

prepare_xdg() {
  local root="$1"
  install -d -m 0700 "${root}" "${root}/config" "${root}/state" "${root}/cache"
}

run_client() {
  local root="$1"
  shift
  sudo -n runuser -u "${LOGIN_USER}" -- env \
    XDG_CONFIG_HOME="${root}/config" \
    XDG_STATE_HOME="${root}/state" \
    XDG_CACHE_HOME="${root}/cache" \
    SSL_CERT_FILE="${CA_CERT}" \
    /usr/bin/podlaz "$@"
}

client_state_dir() {
  printf '%s/state/podlaz' "$1"
}

client_id_path() {
  printf '%s/client-id' "$(client_state_dir "$1")"
}

subscriptions_path() {
  printf '%s/subscriptions.json' "$(client_state_dir "$1")"
}

profiles_path() {
  printf '%s/profiles.json' "$(client_state_dir "$1")"
}

read_client_id() {
  local path
  path="$(client_id_path "$1")"
  [[ -f "${path}" && ! -L "${path}" ]] || fail "client identity file is missing"
  [[ "$(stat -c '%a' "${path}")" == 600 ]] || fail "client identity file mode is not 0600"
  tr -d '\r\n[:space:]' <"${path}"
}

read_subscription_id() {
  local path
  path="$(subscriptions_path "$1")"
  jq -er '.schema_version == "v1" and (.subscriptions | length) == 1' "${path}" >/dev/null
  jq -er '.subscriptions[0].id' "${path}"
}

assert_one_profile_and_subscription() {
  local root="$1" subscriptions profiles
  subscriptions="$(subscriptions_path "${root}")"
  profiles="$(profiles_path "${root}")"
  jq -e '.schema_version == "v1" and (.subscriptions | length) == 1' "${subscriptions}" >/dev/null || \
    fail "subscription store does not contain exactly one committed subscription"
  jq -e '.schema_version == "v1" and (.profiles | length) == 1' "${profiles}" >/dev/null || \
    fail "profile store does not contain exactly one committed profile"
}

fetch_server_devices() {
  local output="$1"
  api GET "/api/hwid/devices/${USER_ID}" "" "${output}"
}

assert_single_server_hwid() {
  local expected="$1" output="${STATE_DIR}/server-hwid-single.json"
  fetch_server_devices "${output}"
  jq -e --arg hwid "${expected}" \
    '.response.total == 1 and (.response.devices | length) == 1 and .response.devices[0].hwid == $hwid' \
    "${output}" >/dev/null || fail "server-side HWID state does not contain exactly the expected device"
}

assert_two_server_hwids() {
  local first="$1" second="$2" output="${STATE_DIR}/server-hwid-two.json"
  fetch_server_devices "${output}"
  jq -e --arg first "${first}" --arg second "${second}" \
    '.response.total == 2 and (.response.devices | length) == 2 and
     ([.response.devices[].hwid] | sort) == ([$first,$second] | sort)' \
    "${output}" >/dev/null || fail "server-side HWID state does not contain the two expected devices"
}

set_user_hwid_limit() {
  local limit="$1" body="${STATE_DIR}/user-limit.json" response="${STATE_DIR}/user-limit-response.json"
  jq -n --argjson id "${USER_ID}" --argjson limit "${limit}" '{id:$id,hwidDeviceLimit:$limit}' >"${body}"
  chmod 0600 "${body}"
  api PATCH /api/users "${body}" "${response}"
  jq -e --argjson limit "${limit}" '.response.hwidDeviceLimit == $limit' "${response}" >/dev/null || \
    fail "Remnawave did not apply requested HWID limit"
}

delete_server_hwid() {
  local hwid="$1" body="${STATE_DIR}/delete-hwid.json" response="${STATE_DIR}/delete-hwid-response.json"
  jq -n --argjson userId "${USER_ID}" --arg hwid "${hwid}" '{userId:$userId,hwid:$hwid}' >"${body}"
  chmod 0600 "${body}"
  api POST /api/hwid/devices/delete "${body}" "${response}"
}

snapshot_committed_state() {
  local root="$1" destination="$2"
  install -d -m 0700 "${destination}"
  cp -- "$(subscriptions_path "${root}")" "${destination}/subscriptions.json"
  cp -- "$(profiles_path "${root}")" "${destination}/profiles.json"
  cp -- "$(client_id_path "${root}")" "${destination}/client-id"
  chmod 0600 "${destination}/"*
}

assert_committed_state_unchanged() {
  local root="$1" snapshot="$2"
  cmp -s "${snapshot}/subscriptions.json" "$(subscriptions_path "${root}")" || \
    fail "rejected refresh changed committed subscription state"
  cmp -s "${snapshot}/profiles.json" "$(profiles_path "${root}")" || \
    fail "rejected refresh changed committed profile state"
  cmp -s "${snapshot}/client-id" "$(client_id_path "${root}")" || \
    fail "rejected refresh rotated client identity"
}

install_candidate() {
  sudo -n apt install -y "${CANDIDATE_DEB}" >/dev/null
  PACKAGE_INSTALLED=1
  sudo -n systemctl daemon-reload
  sudo -n systemctl reset-failed podlazd.service >/dev/null 2>&1 || true
  sudo -n systemctl start podlazd.service
  assert_exact_podlaz_package_runtime_provenance "${CANDIDATE_DEB}" "${EXPECTED_COMMIT}"
}

cleanup_acceptance() {
  local code=$?
  trap - EXIT INT TERM
  set +e
  if [[ "${PACKAGE_INSTALLED}" == 1 ]]; then
    sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
    sudo -n apt purge -y podlaz >/dev/null 2>&1 || true
    sudo -n systemctl daemon-reload >/dev/null 2>&1 || true
    sudo -n systemctl reset-failed podlazd.service >/dev/null 2>&1 || true
  fi
  if remnawave_fixture_cleanup; then
    record_if_missing fixture.cleanup pass
  else
    record_if_missing fixture.cleanup fail
    mark_failure fixture remnawave.cleanup
    code=1
  fi
  if (( code != 0 )) && [[ "${FAILURE_CLASS}" == none ]]; then
    mark_failure diagnostic_unknown scenario
  fi
  finalize_report
  exit "${code}"
}
trap cleanup_acceptance EXIT INT TERM

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  install -d -m 0700 "${E2E_TMP_ROOT}" "${E2E_ARTIFACT_DIR}"
  : >"${REPORT}"
  chmod 0600 "${REPORT}"

  mark_failure fixture candidate.provenance
  validate_candidate "$1"
  CANDIDATE_SHA256="$(sha256sum "${CANDIDATE_DEB}" | awk '{print $1}')"
  printf 'candidate.commit=%s\n' "${EXPECTED_COMMIT,,}" >>"${REPORT}"
  printf 'candidate.package_sha256=%s\n' "${CANDIDATE_SHA256}" >>"${REPORT}"
  printf 'remnawave.panel_version=%s\n' "${REMNAWAVE_PANEL_VERSION}" >>"${REPORT}"
  printf 'remnawave.node_version=%s\n' "${REMNAWAVE_NODE_VERSION}" >>"${REPORT}"

  mark_failure fixture remnawave.bootstrap
  remnawave_fixture_start
  mask_value "${SUBSCRIPTION_URL}"

  mark_failure fixture candidate.install
  install_candidate

  mark_failure product subscription.import
  prepare_xdg "${FIRST_XDG}"
  expect_private_success remnawave-primary-import run_client "${FIRST_XDG}" import "${SUBSCRIPTION_URL}"
  assert_one_profile_and_subscription "${FIRST_XDG}"
  record_evidence remnawave.subscription_import pass

  local primary_client_id primary_subscription_id primary_client_id_after
  primary_client_id="$(read_client_id "${FIRST_XDG}")"
  mask_value "${primary_client_id}"
  assert_single_server_hwid "${primary_client_id}"
  record_evidence remnawave.hwid_registration pass
  primary_subscription_id="$(read_subscription_id "${FIRST_XDG}")"
  mask_value "${primary_subscription_id}"

  mark_failure product subscription.refresh
  expect_private_success remnawave-primary-refresh run_client "${FIRST_XDG}" subscription update "${primary_subscription_id}"
  primary_client_id_after="$(read_client_id "${FIRST_XDG}")"
  [[ "${primary_client_id_after}" == "${primary_client_id}" ]] || fail "primary refresh rotated client identity"
  assert_single_server_hwid "${primary_client_id}"
  record_evidence remnawave.subscription_refresh pass
  record_evidence remnawave.hwid_stable pass

  # Prepare a real committed secondary state without weakening the final policy:
  # temporarily admit one more device, import through Podlaz, then remove only
  # that secondary server-side device and restore hwidDeviceLimit=1.
  mark_failure remnawave hwid.policy_setup
  set_user_hwid_limit 2
  prepare_xdg "${SECOND_XDG}"
  expect_private_success remnawave-secondary-import run_client "${SECOND_XDG}" import "${SUBSCRIPTION_URL}"
  assert_one_profile_and_subscription "${SECOND_XDG}"

  local secondary_client_id secondary_subscription_id secondary_snapshot rejection_rc
  secondary_client_id="$(read_client_id "${SECOND_XDG}")"
  mask_value "${secondary_client_id}"
  [[ "${secondary_client_id}" != "${primary_client_id}" ]] || fail "fresh XDG identity reused primary client identity"
  assert_two_server_hwids "${primary_client_id}" "${secondary_client_id}"

  secondary_subscription_id="$(read_subscription_id "${SECOND_XDG}")"
  mask_value "${secondary_subscription_id}"
  secondary_snapshot="${E2E_TMP_ROOT}/secondary-committed-snapshot"
  snapshot_committed_state "${SECOND_XDG}" "${secondary_snapshot}"

  delete_server_hwid "${secondary_client_id}"
  set_user_hwid_limit 1
  assert_single_server_hwid "${primary_client_id}"

  set +e
  capture_private_command remnawave-secondary-rejected-refresh \
    run_client "${SECOND_XDG}" subscription update "${secondary_subscription_id}"
  rejection_rc=$?
  set -e
  mark_failure product hwid.rejection
  [[ "${rejection_rc}" == 1 ]] || fail "secondary refresh was not rejected with runtime failure"
  assert_committed_state_unchanged "${SECOND_XDG}" "${secondary_snapshot}"
  record_evidence remnawave.rejected_state_preserved pass
  assert_single_server_hwid "${primary_client_id}"
  record_evidence remnawave.hwid_device_limit pass

  expect_private_success remnawave-primary-refresh-after-rejection \
    run_client "${FIRST_XDG}" subscription update "${primary_subscription_id}"
  [[ "$(read_client_id "${FIRST_XDG}")" == "${primary_client_id}" ]] || \
    fail "primary identity rotated after secondary rejection"
  assert_single_server_hwid "${primary_client_id}"

  mark_failure none none
}

main "$@"
