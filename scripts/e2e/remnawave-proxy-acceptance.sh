#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/package_provenance.sh
source "${SCRIPT_DIR}/lib/package_provenance.sh"
# shellcheck source=remnawave-fixture.sh
source "${SCRIPT_DIR}/remnawave-fixture.sh"
# shellcheck source=lib/remnawave_evidence.sh
source "${SCRIPT_DIR}/lib/remnawave_evidence.sh"

require_cmd docker jq sha256sum sudo systemctl

EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-${GITHUB_SHA:-}}"
CANDIDATE_DEB=""
CANDIDATE_SHA256=""
PACKAGE_INSTALLED=0
EVIDENCE_KEYS=(remnawave.proxy_data_plane remnawave.proxy_path_attribution remnawave.proxy_cleanup fixture.cleanup)
REPORT="${E2E_ARTIFACT_DIR}/remnawave-proxy.txt"

validate_candidate() {
  local path="$1" arch
  [[ -f "${path}" && ! -L "${path}" ]] || fail "candidate package must be a regular non-symlink file"
  [[ "$(dpkg-deb --field "${path}" Package)" == podlaz ]] || fail "candidate package is not podlaz"
  arch="$(dpkg-deb --field "${path}" Architecture)"
  [[ "${arch}" == "$(dpkg --print-architecture)" ]] || fail "candidate package architecture does not match runner"
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "candidate commit provenance is required"
  CANDIDATE_DEB="$(readlink -f -- "${path}")"
}

node_access_count() {
  local value
  value="$(docker compose -f "${COMPOSE_FILE}" exec -T remnanode sh -c \
    'if [ -f /tmp/podlaz-remnawave-access.log ]; then wc -l </tmp/podlaz-remnawave-access.log; else printf 0; fi' \
    2>/dev/null | tr -d '[:space:]')"
  [[ "${value}" =~ ^[0-9]+$ ]] || fail "could not read Remnawave Node access counter"
  printf '%s' "${value}"
}

get_profile_uri() {
  local response="${STATE_DIR}/connection-keys.json" uri
  api GET "/api/subscriptions/connection-keys/${USER_ID}" "" "${response}"
  uri="$(jq -er '.response.enabledKeys | select(length == 1) | .[0]' "${response}")"
  [[ "${uri}" == vless://* ]] || fail "Remnawave did not generate the expected VLESS connection key"
  printf '%s' "${uri}"
}

cleanup_proxy_acceptance() {
  local code=$? cleanup_code=0
  trap - EXIT INT TERM
  set +e
  if dpkg-query -W -f='${db:Status-Status}' podlaz 2>/dev/null | grep -Fx installed >/dev/null 2>&1; then
    sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
    sudo -n apt purge -y podlaz >/dev/null 2>&1 || true
    sudo -n systemctl daemon-reload >/dev/null 2>&1 || true
    sudo -n systemctl reset-failed podlazd.service >/dev/null 2>&1 || true
  fi
  cleanup_fixture 0 || cleanup_code=1
  if (( cleanup_code == 0 )); then
    remnawave_record fixture.cleanup pass
  else
    remnawave_record fixture.cleanup fail
    remnawave_mark_failure fixture fixture.cleanup
    code=1
  fi
  if [[ -n "${CANDIDATE_SHA256}" ]]; then
    remnawave_finalize_report "${REPORT}" "${EXPECTED_COMMIT}" "${CANDIDATE_SHA256}" "${PANEL_VERSION}" "${NODE_VERSION}" "${EVIDENCE_KEYS[@]}"
  fi
  exit "${code}"
}
trap cleanup_proxy_acceptance EXIT INT TERM

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  validate_candidate "$1"
  CANDIDATE_SHA256="$(sha256sum "${CANDIDATE_DEB}" | awk '{print $1}')"
  install -d -m 0700 "${E2E_TMP_ROOT}" "${E2E_ARTIFACT_DIR}"

  remnawave_mark_failure fixture remnawave.bootstrap
  remnawave_fixture_start

  local profile_uri before after delta proxy_tmp proxy_artifacts
  profile_uri="$(get_profile_uri)"
  mask_value "${profile_uri}"
  before="$(node_access_count)"

  remnawave_mark_failure diagnostic_unknown proxy.data_plane
  proxy_tmp="${E2E_TMP_ROOT}/proxy-client"
  proxy_artifacts="${proxy_tmp}/proxy-private-artifacts"
  local data_plane_code
  set +e
  PODLAZ_E2E_PROFILE_URI="${profile_uri}" \
  PODLAZ_E2E_PROFILE_URI_LIST="" \
  PODLAZ_E2E_EXPECTED_EGRESS_IP="" \
  PODLAZ_E2E_RELIABILITY_CYCLES="${PODLAZ_E2E_RELIABILITY_CYCLES:-3}" \
  PODLAZ_E2E_PACKAGE_PATH="${CANDIDATE_DEB}" \
  PODLAZ_E2E_KEEP_PACKAGE=true \
  E2E_TMP_ROOT="${proxy_tmp}" \
  E2E_ARTIFACT_DIR="${proxy_artifacts}" \
    bash "${SCRIPT_DIR}/data-plane.sh"
  data_plane_code=$?
  set -e
  if (( data_plane_code != 0 )); then
    remnawave_mark_private_command_failure "${proxy_tmp}" diagnostic_unknown proxy.data_plane
    return "${data_plane_code}"
  fi
  remnawave_record remnawave.proxy_data_plane pass
  remnawave_record remnawave.proxy_cleanup pass

  PACKAGE_INSTALLED=1
  assert_installed_package_version_matches_deb "${CANDIDATE_DEB}" podlaz
  assert_installed_podlaz_files_match_deb "${CANDIDATE_DEB}"
  assert_installed_podlaz_commit "${EXPECTED_COMMIT}"

  after="$(node_access_count)"
  delta=$((after - before))
  # One explicit phase plus every reliability cycle performs one SOCKS and one
  # HTTP external request. Count-only attribution avoids publishing Node logs.
  expected_min=$((2 * (1 + PODLAZ_E2E_RELIABILITY_CYCLES)))
  remnawave_mark_failure remnawave proxy.path_attribution
  (( delta >= expected_min )) || fail "Remnawave Node did not observe all proxy data-plane requests"
  remnawave_record remnawave.proxy_path_attribution pass
  REMNAWAVE_FAILURE_CLASS=none
  REMNAWAVE_FAILURE_STEP=none

}

main "$@"
