#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
# shellcheck source=hosted-synthetic-tun.sh
source "${SCRIPT_DIR}/hosted-synthetic-tun.sh"

REPORT="${E2E_ARTIFACT_DIR}/hosted-maintained-package-upgrade.txt"
MACHINE="podlaz-maintained-upgrade"
HOST_VETH="pzmaint0"
HOST_ENDPOINT_DEV="pzmaintsrv"
NFT_TABLE="pzmaint_hosted"
FOREIGN_NFT_TABLE="pzmaint_foreign"
NETWORK_CIDR="172.31.250.0/30"
HOST_CIDR="172.31.250.1/30"
GUEST_CIDR="172.31.250.2/30"
HOST_IP="172.31.250.1"
ENDPOINT_CIDR="172.31.249.1/32"
ENDPOINT_IP="172.31.249.1"
GUEST_ROOT="${E2E_TMP_ROOT}/system-guest"
PRIVATE_ROOT="${E2E_TMP_ROOT}/private"
XRAY_ROOT="${PRIVATE_ROOT}/synthetic-xray"
GUEST_CANDIDATE="/opt/podlaz-candidate.deb"
GUEST_PREVIOUS="/run/podlaz-synthetic-xray/maintained-previous.deb"
OUTER_PREVIOUS="${XRAY_ROOT}/maintained-previous.deb"
GUEST_XDG="/home/e2e/.local/share/podlaz-maintained-upgrade"
TUN_RULE="/etc/polkit-1/rules.d/49-podlaz-maintained-upgrade.rules"
GUEST_PRIVATE="/tmp/podlaz-maintained-upgrade"
GUEST_MANIFEST="${GUEST_PRIVATE}/network-manifest.json"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-${GITHUB_SHA:-}}"
PREVIOUS_VERSION="${PODLAZ_E2E_PREVIOUS_VERSION:-}"
PREVIOUS_COMMIT="${PODLAZ_E2E_PREVIOUS_COMMIT:-}"
PREVIOUS_SHA256="${PODLAZ_E2E_PREVIOUS_SHA256:-}"
PREVIOUS_DEB=""
PREVIOUS_ACTUAL_SHA256=""
PRIVACY_WATCH_PID=""
PRIVACY_WATCH_STOP="${PRIVATE_ROOT}/privacy-watch.stop"
PRIVACY_WATCH_FAIL="${PRIVATE_ROOT}/privacy-watch.fail"
PRIVACY_WATCH_LOG="${PRIVATE_ROOT}/privacy-watch.log"

EVIDENCE_KEYS=(
  source.release
  source.provenance
  source.verified_active
  tun.system_dns
  tun.https_tls
  maintained.same_network_session
  maintained.privacy_continuous
  candidate.provenance
  maintained.post_upgrade_traffic
  maintained.foreign_state
  maintained.terminal_cleanup
  maintained.network_restored
  maintained.recovery_clean
  outer.cleanup
  artifact.privacy
)

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-maintained-package-upgrade.txt' -print -quit)"
  [[ -z "${extra}" ]] || return 1
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || return 1
  ! grep -Eq 'vless://|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|172[.]31[.](249|250)[.]' "${REPORT}"
}

validate_previous() {
  local path="$1" version arch
  [[ -n "${PREVIOUS_VERSION}" ]] || fail "maintained previous-release version is unavailable"
  [[ "${PREVIOUS_COMMIT}" =~ ^[0-9a-f]{40}$ ]] || fail "maintained previous-release commit is unavailable"
  [[ "${PREVIOUS_SHA256}" =~ ^[0-9a-f]{64}$ ]] || fail "maintained previous-release digest is unavailable"
  [[ -f "${path}" && ! -L "${path}" ]] || fail "maintained previous-release package must be a regular non-symlink file"
  [[ "$(dpkg-deb --field "${path}" Package)" == podlaz ]] || fail "maintained previous-release package is not podlaz"
  version="$(dpkg-deb --field "${path}" Version)"
  arch="$(dpkg-deb --field "${path}" Architecture)"
  [[ "${version%%-*}" == "${PREVIOUS_VERSION}" ]] || fail "maintained previous-release package version mismatch"
  [[ "${arch}" == "$(dpkg --print-architecture)" ]] || fail "maintained previous-release architecture does not match runner"
  PREVIOUS_ACTUAL_SHA256="$(sha256sum "${path}" | awk '{print $1}')"
  [[ "${PREVIOUS_ACTUAL_SHA256}" == "${PREVIOUS_SHA256}" ]] || fail "maintained previous-release digest mismatch"
  PREVIOUS_DEB="$(readlink -f -- "${path}")"
}

validate_candidate_for_upgrade() {
  local candidate_version previous_package_version
  validate_candidate "$1"
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-f]{40}$ ]] || fail "candidate commit identity is unavailable"
  candidate_version="$(dpkg-deb --field "${CANDIDATE_DEB}" Version)"
  previous_package_version="$(dpkg-deb --field "${PREVIOUS_DEB}" Version)"
  dpkg --compare-versions "${candidate_version}" gt "${previous_package_version}" || \
    fail "candidate Debian version must be newer than maintained previous release"
}

stage_previous() {
  install -d -m 0700 "${XRAY_ROOT}"
  install -m 0600 "${PREVIOUS_DEB}" "${OUTER_PREVIOUS}"
}

install_previous_fixture() {
  guest_exec /bin/bash -lc "DEBIAN_FRONTEND=noninteractive apt-get install -y '${GUEST_PREVIOUS}' >/tmp/podlaz-maintained-previous-install.log 2>&1"
  guest_exec systemctl daemon-reload
  guest_exec systemctl start podlazd.service
  guest_exec systemctl is-active --quiet podlazd.service
  guest_exec /bin/bash -lc "cd /workspace && source scripts/e2e/lib/e2e.sh && source scripts/e2e/lib/package_provenance.sh && assert_exact_podlaz_package_runtime_provenance '${GUEST_PREVIOUS}' '${PREVIOUS_COMMIT}'"
}

prepare_guest_user_state() {
  guest_exec install -d -o e2e -g e2e -m 0700 \
    "${GUEST_XDG}" "${GUEST_XDG}/config" "${GUEST_XDG}/state" "${GUEST_XDG}/cache" "${GUEST_PRIVATE}"
  guest_exec /bin/bash -lc "uri=\$(cat /run/podlaz-synthetic-xray/profile-uri); runuser -u e2e -- env XDG_CONFIG_HOME='${GUEST_XDG}/config' XDG_STATE_HOME='${GUEST_XDG}/state' XDG_CACHE_HOME='${GUEST_XDG}/cache' /usr/bin/podlaz profile import \"\${uri}\" >'${GUEST_PRIVATE}/import.stdout' 2>'${GUEST_PRIVATE}/import.stderr'"
  guest_exec /bin/bash -lc "awk '/^Imported profile:/ {print \$3; exit}' '${GUEST_PRIVATE}/import.stdout' >'${GUEST_PRIVATE}/profile-id' && test -s '${GUEST_PRIVATE}/profile-id'"
  guest_exec /bin/bash -lc "id=\$(cat '${GUEST_PRIVATE}/profile-id'); runuser -u e2e -- env XDG_CONFIG_HOME='${GUEST_XDG}/config' XDG_STATE_HOME='${GUEST_XDG}/state' XDG_CACHE_HOME='${GUEST_XDG}/cache' /usr/bin/podlaz profile validate \"\${id}\" --mode tun >'${GUEST_PRIVATE}/validate.stdout' 2>'${GUEST_PRIVATE}/validate.stderr'"
}

connect_on_previous_release() {
  guest_exec /bin/bash -lc "id=\$(cat '${GUEST_PRIVATE}/profile-id'); runuser -u e2e -- env XDG_CONFIG_HOME='${GUEST_XDG}/config' XDG_STATE_HOME='${GUEST_XDG}/state' XDG_CACHE_HOME='${GUEST_XDG}/cache' /usr/bin/podlaz connect --mode tun \"\${id}\" >'${GUEST_PRIVATE}/connect.stdout' 2>'${GUEST_PRIVATE}/connect.stderr'"
}

capture_network_session_id() {
  guest_exec jq -er '.session_id | select(type == "string" and length > 0)' /run/podlaz/network-session-continuation.json | tr -d '[:space:]'
}

start_privacy_watch() {
  local table tun
  rm -f -- "${PRIVACY_WATCH_STOP}" "${PRIVACY_WATCH_FAIL}" "${PRIVACY_WATCH_LOG}"
  read -r table tun < <(
    guest_exec jq -er '[.protection.table, .protection.tun_interface] | @tsv' /run/podlaz/network-session-continuation.json
  )
  [[ -n "${table}" && -n "${tun}" ]] || return 1
  (
    while [[ ! -e "${PRIVACY_WATCH_STOP}" ]]; do
      if ! guest_exec /bin/bash -lc "nft -j list table inet '${table}' | jq -e --arg table '${table}' --arg tun '${tun}' '([.nftables[] | .rule? | select(.table == \$table and .comment == \"podlaz:privacy-envelope:block-direct\" and (.expr | tostring | contains(\"reject\")))] | length) == 1 and ([.nftables[] | .rule? | select(.table == \$table and .comment == \"podlaz:privacy-envelope:tun-egress\" and (.expr | tostring | contains(\$tun)))] | length) == 1' >/dev/null" >>"${PRIVACY_WATCH_LOG}" 2>&1; then
        : >"${PRIVACY_WATCH_FAIL}"
        return 1
      fi
      sleep 0.1
    done
  ) &
  PRIVACY_WATCH_PID=$!
}

stop_privacy_watch() {
  local watch_code=0
  [[ -n "${PRIVACY_WATCH_PID}" ]] || return 1
  : >"${PRIVACY_WATCH_STOP}"
  wait "${PRIVACY_WATCH_PID}" || watch_code=$?
  PRIVACY_WATCH_PID=""
  [[ "${watch_code}" == 0 && ! -e "${PRIVACY_WATCH_FAIL}" ]]
}

install_candidate_once() {
  guest_exec /bin/bash -lc "DEBIAN_FRONTEND=noninteractive apt-get install -y '${GUEST_CANDIDATE}' >/tmp/podlaz-maintained-candidate-install.log 2>&1"
}

assert_package_replacement_observed() {
  local before="$1" after
  guest_exec systemctl is-active --quiet podlazd.service
  after="$(guest_exec systemctl show -p MainPID --value podlazd.service | tr -d '[:space:]')"
  [[ "${before}" =~ ^[1-9][0-9]*$ && "${after}" =~ ^[1-9][0-9]*$ && "${after}" != "${before}" ]]
  guest_exec /bin/bash -lc 'result="$(systemctl show -p Result --value podlazd.service)"; [[ "$result" != timeout && "$result" == success ]]'
}

assert_candidate_runtime_provenance() {
  assert_guest_package_provenance
  guest_exec /bin/bash -lc "cd /workspace && source scripts/e2e/lib/e2e.sh && source scripts/e2e/lib/package_provenance.sh && assert_exact_podlaz_package_runtime_provenance '${GUEST_CANDIDATE}' '${EXPECTED_COMMIT}'"
}

assert_dns_https() {
  guest_exec resolvectl flush-caches
  guest_exec timeout 20 getent ahostsv4 example.com >/dev/null
  guest_exec timeout 30 curl -4 -fsS -o /dev/null https://example.com/
}

run_scenario() {
  local session_before session_after previous_pid install_code=0 convergence_code=0 privacy_code=0

  mark_failure infrastructure outer.baseline
  capture_outer_baseline
  mark_failure infrastructure guest.prepare
  prepare_system_guest
  start_system_guest

  mark_failure fixture previous.release
  install_previous_fixture
  record_evidence source.release pass
  record_evidence source.provenance pass

  mark_failure fixture synthetic.endpoint
  start_synthetic_xray_endpoint
  install_tun_authorization
  prepare_guest_user_state
  create_foreign_sentinel
  capture_guest_network_baseline

  mark_failure product source.connect
  connect_on_previous_release
  wait_guest_status verified-active 120
  assert_verified_active_authority
  record_evidence source.verified_active pass
  run_active_traffic_checks
  session_before="$(capture_network_session_id)"
  previous_pid="$(guest_exec systemctl show -p MainPID --value podlazd.service | tr -d '[:space:]')"

  mark_failure product candidate.package_replacement
  start_privacy_watch
  set +e
  install_candidate_once
  install_code=$?
  if (( install_code == 0 )); then
    wait_guest_status verified-active 180
    convergence_code=$?
  fi
  set -e
  stop_privacy_watch || privacy_code=$?
  (( install_code == 0 )) || return "${install_code}"
  (( convergence_code == 0 )) || return "${convergence_code}"
  (( privacy_code == 0 )) || return "${privacy_code}"
  record_evidence maintained.privacy_continuous pass

  assert_package_replacement_observed "${previous_pid}"
  assert_candidate_runtime_provenance
  record_evidence candidate.provenance pass
  session_after="$(capture_network_session_id)"
  [[ "${session_before}" == "${session_after}" ]] || fail "maintained package upgrade changed current-boot Network Session identity"
  record_evidence maintained.same_network_session pass
  assert_verified_active_authority
  assert_dns_https
  record_evidence maintained.post_upgrade_traffic pass
  assert_foreign_sentinel
  record_evidence maintained.foreign_state pass

  mark_failure product candidate.terminal_convergence
  run_guest_user /usr/bin/podlaz disconnect >"${PRIVATE_ROOT}/disconnect.stdout" 2>"${PRIVATE_ROOT}/disconnect.stderr"
  wait_guest_status clean-inactive 120
  assert_terminal_authority_clean
  record_evidence maintained.terminal_cleanup pass
  assert_guest_network_baseline_restored
  assert_dns_https
  record_evidence maintained.network_restored pass
  run_clean_recovery
  record_evidence maintained.recovery_clean pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
}

main() {
  (($# == 2)) || fail "usage: $0 CANDIDATE.deb MAINTAINED-PREVIOUS.deb"
  require_cmd awk bash cmp curl debootstrap dpkg dpkg-deb find grep install ip iptables jq mktemp nft python3 readlink rm seq sha256sum sleep ss sudo systemd-nspawn systemd-run timeout
  install -d -m 0700 "${PRIVATE_ROOT}" "${E2E_ARTIFACT_DIR}" "${XRAY_ROOT}"
  : >"${REPORT}"
  chmod 0600 "${REPORT}"
  validate_previous "$2"
  validate_candidate_for_upgrade "$1"
  stage_previous
  trap teardown_all EXIT
  run_scenario
}

if [[ "${1:-}" == validate-report ]]; then
  validate_report
  exit 0
fi

main "$@"
