#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/package_provenance.sh
source "${SCRIPT_DIR}/lib/package_provenance.sh"
# shellcheck source=remnawave-fixture.sh
source "${SCRIPT_DIR}/remnawave-fixture.sh"

require_cmd docker jq sha256sum sudo systemctl

EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-${GITHUB_SHA:-}}"
CANDIDATE_DEB=""
CANDIDATE_SHA256=""
PACKAGE_INSTALLED=0
REPORT="${E2E_ARTIFACT_DIR}/remnawave-proxy.txt"
EVIDENCE_KEYS=(
  remnawave.proxy_data_plane
  remnawave.proxy_path_attribution
  remnawave.proxy_cleanup
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
  [[ "${key}" =~ ^[a-z0-9_.-]+$ ]] || fail "invalid Remnawave proxy evidence key"
  case "${value}" in pass|fail) ;; *) fail "invalid Remnawave proxy evidence value" ;; esac
  ! evidence_recorded "${key}" || fail "duplicate Remnawave proxy evidence key: ${key}"
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
  local code=$?
  trap - EXIT INT TERM
  set +e
  if dpkg-query -W -f='${db:Status-Status}' podlaz 2>/dev/null | grep -Fx installed >/dev/null 2>&1; then
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
trap cleanup_proxy_acceptance EXIT INT TERM

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  install -d -m 0700 "${E2E_TMP_ROOT}" "${E2E_ARTIFACT_DIR}"
  : >"${REPORT}"
  chmod 0600 "${REPORT}"

  mark_failure fixture candidate.provenance
  validate_candidate "$1"
  CANDIDATE_SHA256="$(sha256sum "${CANDIDATE_DEB}" | awk '{print $1}')"
  {
    printf 'candidate.commit=%s\n' "${EXPECTED_COMMIT,,}"
    printf 'candidate.package_sha256=%s\n' "${CANDIDATE_SHA256}"
    printf 'remnawave.panel_version=%s\n' "${REMNAWAVE_PANEL_VERSION}"
    printf 'remnawave.node_version=%s\n' "${REMNAWAVE_NODE_VERSION}"
  } >>"${REPORT}"

  mark_failure fixture remnawave.bootstrap
  remnawave_fixture_start

  local profile_uri before after delta expected_min proxy_tmp proxy_artifacts
  mark_failure remnawave provider.material
  profile_uri="$(get_profile_uri)"
  mask_value "${profile_uri}"
  before="$(node_access_count)"

  proxy_tmp="${E2E_TMP_ROOT}/proxy-client"
  proxy_artifacts="${proxy_tmp}/proxy-private-artifacts"
  mark_failure diagnostic_unknown proxy.data_plane
  PODLAZ_E2E_PROFILE_URI="${profile_uri}" \
  PODLAZ_E2E_PROFILE_URI_LIST="" \
  PODLAZ_E2E_EXPECTED_EGRESS_IP="" \
  PODLAZ_E2E_RELIABILITY_CYCLES="${PODLAZ_E2E_RELIABILITY_CYCLES:-3}" \
  PODLAZ_E2E_PACKAGE_PATH="${CANDIDATE_DEB}" \
  PODLAZ_E2E_KEEP_PACKAGE=true \
  E2E_TMP_ROOT="${proxy_tmp}" \
  E2E_ARTIFACT_DIR="${proxy_artifacts}" \
    bash "${SCRIPT_DIR}/data-plane.sh"
  record_evidence remnawave.proxy_data_plane pass
  record_evidence remnawave.proxy_cleanup pass

  PACKAGE_INSTALLED=1
  mark_failure fixture candidate.provenance
  assert_installed_package_version_matches_deb "${CANDIDATE_DEB}" podlaz
  assert_installed_podlaz_files_match_deb "${CANDIDATE_DEB}"
  assert_installed_podlaz_commit "${EXPECTED_COMMIT}"

  mark_failure remnawave proxy.path_attribution
  after="$(node_access_count)"
  delta=$((after - before))
  # One explicit phase plus every reliability cycle performs one SOCKS and one
  # HTTP external request. Count-only attribution avoids publishing Node logs.
  expected_min=$((2 * (1 + PODLAZ_E2E_RELIABILITY_CYCLES)))
  (( delta >= expected_min )) || fail "Remnawave Node did not observe all proxy data-plane requests"
  record_evidence remnawave.proxy_path_attribution pass

  mark_failure none none
}

main "$@"
