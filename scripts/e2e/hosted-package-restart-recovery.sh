#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/profile_input.sh
source "${SCRIPT_DIR}/lib/profile_input.sh"
# shellcheck source=hosted-synthetic-tun.sh
source "${SCRIPT_DIR}/hosted-synthetic-tun.sh"

REPORT="${E2E_ARTIFACT_DIR}/hosted-package-restart-recovery.txt"
FOCUSED_ARTIFACT_DIR="${E2E_ARTIFACT_DIR}/focused"
MACHINE="podlaz-package-restart"
HOST_VETH="pzpkg0"
HOST_ENDPOINT_DEV="pzpkgsrv"
NFT_TABLE="pzpkg_hosted"
FOREIGN_NFT_TABLE="pzpkg_foreign"
NETWORK_CIDR="172.31.252.0/30"
HOST_CIDR="172.31.252.1/30"
GUEST_CIDR="172.31.252.2/30"
HOST_IP="172.31.252.1"
ENDPOINT_CIDR="172.31.251.1/32"
ENDPOINT_IP="172.31.251.1"
GUEST_ROOT="${E2E_TMP_ROOT}/system-guest"
PRIVATE_ROOT="${E2E_TMP_ROOT}/private"
XRAY_ROOT="${PRIVATE_ROOT}/guest-inputs"
GUEST_CANDIDATE="/opt/podlaz-candidate.deb"
GUEST_RUN_ROOT="/run/podlaz-hosted-package-restart"
GUEST_FOCUSED_PRIVATE="${GUEST_RUN_ROOT}/private"
GUEST_FOCUSED_PUBLIC="/opt/podlaz-hosted-package-restart-public"
GUEST_PROFILE_FILE="${GUEST_RUN_ROOT}/profile-uri"
GUEST_EGRESS_FILE="${GUEST_RUN_ROOT}/expected-egress"
GUEST_PREVIOUS="${GUEST_RUN_ROOT}/podlaz-v0.2.40.deb"
PACKAGE_RESTART_RECOVERY_RULE="/etc/polkit-1/rules.d/50-podlaz-hosted-package-restart.rules"
OUTER_PROFILE_FILE="${XRAY_ROOT}/profile-uri"
OUTER_EGRESS_FILE="${XRAY_ROOT}/expected-egress"
OUTER_PREVIOUS="${XRAY_ROOT}/podlaz-v0.2.40.deb"
PREVIOUS_SHA256="c9d8f76838292d39355506123e2f03ca1f0a96227fb2c22af8324ac6baf3b278"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-${GITHUB_SHA:-}}"
PUBLIC_IP_CHECK_URL="${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:-https://api.ipify.org}"
PREVIOUS_DEB=""
CANDIDATE_SHA256=""
PREVIOUS_ACTUAL_SHA256=""
TEARDOWN_RUNNING=false
SYSTEM_GUEST_ACTIVE=false
NSPAWN_PID=""
OUTER_DEFAULT_ROUTE=""
OUTER_RULES=""
OUTER_RESOLV_HASH=""
OUTER_IP_FORWARD=""
OUTER_EGRESS_IF=""
FAILURE_CLASS=none
FAILURE_STEP=none

EVIDENCE_KEYS=(
  guest.substrate
  source.release
  candidate.provenance
  source.vpn_traffic
  source.vpn_egress
  package.restart_converged
  post_convergence.traffic
  foreign.state
  terminal.cleanup
  recovery.idempotent
  outer.cleanup
  artifact.privacy
)

validate_previous() {
  local path="$1" version arch
  [[ -f "${path}" && ! -L "${path}" ]] || fail "v0.2.40 package must be a regular non-symlink file"
  [[ "$(dpkg-deb --field "${path}" Package)" == podlaz ]] || fail "v0.2.40 package is not podlaz"
  version="$(dpkg-deb --field "${path}" Version)"
  arch="$(dpkg-deb --field "${path}" Architecture)"
  [[ "${version%%-*}" == "0.2.40" ]] || fail "previous package must be exact v0.2.40"
  [[ "${arch}" == "$(dpkg --print-architecture)" ]] || fail "v0.2.40 package architecture does not match runner"
  PREVIOUS_ACTUAL_SHA256="$(sha256sum "${path}" | awk '{print $1}')"
  [[ "${PREVIOUS_ACTUAL_SHA256}" == "${PREVIOUS_SHA256}" ]] || fail "v0.2.40 package digest does not match pinned public release asset"
  PREVIOUS_DEB="$(readlink -f -- "${path}")"
}

validate_candidate_for_restart() {
  local version
  validate_candidate "$1"
  version="$(dpkg-deb --field "${CANDIDATE_DEB}" Version)"
  dpkg --compare-versions "${version}" gt 0.2.40 || fail "candidate Debian version must be newer than v0.2.40"
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-f]{40}$ ]] || fail "candidate commit identity is unavailable"
  CANDIDATE_SHA256="$(sha256sum "${CANDIDATE_DEB}" | awk '{print $1}')"
}

stage_private_inputs() {
  local profile
  profile="$(first_configured_profile_uri)" || fail "real-provider profile input is unavailable"
  [[ -n "${profile}" ]] || fail "real-provider profile input is empty"
  if [[ -n "${PODLAZ_E2E_EXPECTED_EGRESS_IP:-}" ]]; then
    python3 - "${PODLAZ_E2E_EXPECTED_EGRESS_IP}" <<'PY' || fail "configured expected egress is not one IPv4 address"
import ipaddress
import sys
try:
    value = ipaddress.ip_address(sys.argv[1])
except ValueError:
    raise SystemExit(1)
raise SystemExit(0 if value.version == 4 else 1)
PY
    mask_value "${PODLAZ_E2E_EXPECTED_EGRESS_IP}"
  fi
  [[ "${PUBLIC_IP_CHECK_URL}" == https://* ]] || fail "PODLAZ_E2E_PUBLIC_IP_CHECK_URL must use HTTPS"

  mask_value "${profile}"

  install -d -m 0700 "${XRAY_ROOT}"
  printf '%s\n' "${profile}" >"${OUTER_PROFILE_FILE}"
  if [[ -n "${PODLAZ_E2E_EXPECTED_EGRESS_IP:-}" ]]; then
    printf '%s\n' "${PODLAZ_E2E_EXPECTED_EGRESS_IP}" >"${OUTER_EGRESS_FILE}"
    chmod 0600 "${OUTER_EGRESS_FILE}"
  else
    rm -f -- "${OUTER_EGRESS_FILE}"
  fi
  install -m 0600 "${PREVIOUS_DEB}" "${OUTER_PREVIOUS}"
  chmod 0600 "${OUTER_PROFILE_FILE}" "${OUTER_PREVIOUS}"
}

prepare_guest_private_inputs() {
  guest_exec install -d -o e2e -g e2e -m 0700 "${GUEST_RUN_ROOT}" "${GUEST_FOCUSED_PRIVATE}" "${GUEST_FOCUSED_PUBLIC}" || \
    fail "could not create guest-private package restart directories"
  guest_exec runuser -u e2e -- env HOME=/home/e2e git config --global --add safe.directory /workspace || \
    fail "could not configure guest git safe.directory"
  guest_exec install -o e2e -g e2e -m 0600 /run/podlaz-synthetic-xray/profile-uri "${GUEST_PROFILE_FILE}" || \
    fail "could not materialize guest-private profile input"
  guest_exec install -o e2e -g e2e -m 0600 /run/podlaz-synthetic-xray/podlaz-v0.2.40.deb "${GUEST_PREVIOUS}" || \
    fail "could not materialize guest-readable v0.2.40 package"
  if [[ -f "${OUTER_EGRESS_FILE}" ]]; then
    guest_exec install -o e2e -g e2e -m 0600 /run/podlaz-synthetic-xray/expected-egress "${GUEST_EGRESS_FILE}" || \
      fail "could not materialize guest-private expected egress input"
  fi
}

install_package_restart_recovery_authorization() {
  local rule_tmp
  rule_tmp="$(mktemp "${PRIVATE_ROOT}/recovery-polkit.XXXXXX")"
  cat >"${rule_tmp}" <<'EOF_RULE'
polkit.addRule(function(action, subject) {
    if (subject.user == "e2e" &&
        action.id == "io.github.aidarkhusainov.podlaz.recover-execute") {
        return polkit.Result.YES;
    }
});
EOF_RULE
  sudo -n install -D -m 0644 "${rule_tmp}" "${GUEST_ROOT}${PACKAGE_RESTART_RECOVERY_RULE}"
  rm -f "${rule_tmp}"
  sleep 1
}

run_focused_acceptance() {
  if [[ -f "${OUTER_EGRESS_FILE}" ]]; then
    guest_exec runuser -u e2e -- env \
      E2E_TMP_ROOT="${GUEST_FOCUSED_PRIVATE}" \
      E2E_ARTIFACT_DIR="${GUEST_FOCUSED_PUBLIC}" \
      PODLAZ_E2E_PROFILE_URI_FILE="${GUEST_PROFILE_FILE}" \
      FOREIGN_NFT_TABLE="${FOREIGN_NFT_TABLE}" \
      PODLAZ_E2E_EXPECTED_EGRESS_IP_FILE="${GUEST_EGRESS_FILE}" \
      PODLAZ_E2E_REQUIRE_EGRESS_CHANGE=true \
      PODLAZ_E2E_PUBLIC_IP_CHECK_URL="${PUBLIC_IP_CHECK_URL}" \
      bash /workspace/scripts/e2e/tun-package-restart-recovery.sh "${GUEST_CANDIDATE}" "${GUEST_PREVIOUS}"
    return
  fi
  guest_exec runuser -u e2e -- env \
    E2E_TMP_ROOT="${GUEST_FOCUSED_PRIVATE}" \
    E2E_ARTIFACT_DIR="${GUEST_FOCUSED_PUBLIC}" \
    PODLAZ_E2E_PROFILE_URI_FILE="${GUEST_PROFILE_FILE}" \
    FOREIGN_NFT_TABLE="${FOREIGN_NFT_TABLE}" \
    PODLAZ_E2E_REQUIRE_EGRESS_CHANGE=true \
    PODLAZ_E2E_PUBLIC_IP_CHECK_URL="${PUBLIC_IP_CHECK_URL}" \
    bash /workspace/scripts/e2e/tun-package-restart-recovery.sh "${GUEST_CANDIDATE}" "${GUEST_PREVIOUS}"
}

collect_focused_artifacts() {
  local source="${GUEST_ROOT}${GUEST_FOCUSED_PUBLIC}"
  [[ -d "${source}" && ! -L "${source}" ]] || return 1
  rm -rf "${FOCUSED_ARTIFACT_DIR}"
  install -d -m 0700 "${FOCUSED_ARTIFACT_DIR}"
  sudo -n cp -a "${source}/." "${FOCUSED_ARTIFACT_DIR}/"
  sudo -n chown -R "$(id -u):$(id -g)" "${FOCUSED_ARTIFACT_DIR}"
  find "${FOCUSED_ARTIFACT_DIR}" -type f -exec chmod 0600 {} +
}

require_result_line() {
  local line="$1"
  grep -Fx -- "${line}" "${FOCUSED_ARTIFACT_DIR}/package-restart-result.txt" >/dev/null
}

validate_focused_evidence() {
  local result="${FOCUSED_ARTIFACT_DIR}/package-restart-result.txt" outcome candidate_hash previous_hash
  [[ -f "${result}" && ! -L "${result}" ]] || return 1

  require_result_line "traffic_v0.2.40-package-restart-vpn=passed" || return 1
  require_result_line "egress_v0.2.40-package-restart-vpn=passed" || return 1
  require_result_line "historical_package_restart_failure=missing nftables chains" || return 1
  require_result_line "intent=resume" || return 1
  require_result_line "traffic_package-restart-terminal-ordinary=passed" || return 1
  require_result_line "package_restart_second_recovery_clean=true" || return 1

  outcome="$(awk -F= '$1=="candidate_outcome" {print $2}' "${result}")"
  case "${outcome}" in
    resumed)
      require_result_line "traffic_package-restart-resumed-vpn=passed" || return 1
      require_result_line "egress_package-restart-resumed-vpn=passed" || return 1
      ;;
    terminal)
      require_result_line "typed_terminal_replay=true" || return 1
      ;;
    *)
      return 1
      ;;
  esac

  candidate_hash="$(tr -d '[:space:]' <"${FOCUSED_ARTIFACT_DIR}/candidate-package.sha256")"
  previous_hash="$(tr -d '[:space:]' <"${FOCUSED_ARTIFACT_DIR}/v0.2.40-package.sha256")"
  [[ "${candidate_hash}" == "${CANDIDATE_SHA256}" ]] || return 1
  [[ "${previous_hash}" == "${PREVIOUS_ACTUAL_SHA256}" ]] || return 1

  grep -Fx "source_commit=${EXPECTED_COMMIT}" "${FOCUSED_ARTIFACT_DIR}/package-provenance-candidate-package-restart.txt" >/dev/null || return 1
  grep -Fx "source_commit=ab71c876d558a7653d44d5b98c76f3899e569a90" "${FOCUSED_ARTIFACT_DIR}/package-provenance-v0.2.40-package-restart.txt" >/dev/null || return 1
}

scan_public_artifacts() {
  local privacy_report="${E2E_ARTIFACT_DIR}/hosted-package-restart-redaction-scan.txt"
  local sources=("${OUTER_PROFILE_FILE}")
  [[ -f "${OUTER_PROFILE_FILE}" ]] || return 1
  if [[ -f "${OUTER_EGRESS_FILE}" ]]; then
    sources+=("${OUTER_EGRESS_FILE}")
  fi
  python3 "${SCRIPT_DIR}/lib/redaction_scan.py" file-contents \
    "${E2E_ARTIFACT_DIR}" "${privacy_report}" "${sources[@]}"
}

teardown_hosted_package_restart() {
  local saved=$? cleanup_failed=0
  [[ "${TEARDOWN_RUNNING}" == false ]] || return
  TEARDOWN_RUNNING=true
  trap - EXIT
  set +e

  if [[ -d "${GUEST_ROOT}${GUEST_FOCUSED_PUBLIC}" ]]; then
    collect_focused_artifacts || cleanup_failed=1
  fi
  stop_system_guest || cleanup_failed=1
  cleanup_outer_plumbing || cleanup_failed=1
  if [[ -n "${OUTER_DEFAULT_ROUTE}" ]]; then
    if assert_outer_baseline_restored; then
      record_if_missing outer.cleanup pass
    else
      cleanup_failed=1
      record_if_missing outer.cleanup fail
      mark_failure infrastructure outer.cleanup
    fi
  else
    record_if_missing outer.cleanup fail
    cleanup_failed=1
  fi

  if scan_public_artifacts; then
    record_if_missing artifact.privacy pass
  else
    cleanup_failed=1
    record_if_missing artifact.privacy fail
    mark_failure fixture artifact.privacy
  fi

  rm -f -- "${OUTER_PROFILE_FILE}" "${OUTER_EGRESS_FILE}" >/dev/null 2>&1 || true

  if (( saved != 0 )) && [[ "${FAILURE_CLASS}" == none ]]; then
    mark_failure diagnostic_unknown package.restart
  fi
  finalize_report
  validate_report || cleanup_failed=1
  set -e
  if (( saved == 0 && cleanup_failed != 0 )); then
    saved=1
  fi
  exit "${saved}"
}

run_scenario() {
  mark_failure infrastructure outer.baseline
  capture_outer_baseline

  mark_failure infrastructure guest.prepare
  prepare_system_guest
  start_system_guest
  record_evidence guest.substrate pass

  mark_failure fixture private-input.handoff
  prepare_guest_private_inputs
  mark_failure fixture tun.authorization
  install_tun_authorization
  install_package_restart_recovery_authorization
  mark_failure diagnostic_unknown package.restart
  run_focused_acceptance
  collect_focused_artifacts
  validate_focused_evidence || return 1

  record_evidence source.release pass
  record_evidence candidate.provenance pass
  record_evidence source.vpn_traffic pass
  record_evidence source.vpn_egress pass
  record_evidence package.restart_converged pass
  record_evidence post_convergence.traffic pass
  record_evidence foreign.state pass
  record_evidence terminal.cleanup pass
  record_evidence recovery.idempotent pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
}

main() {
  (($# == 2)) || fail "usage: $0 CANDIDATE.deb EXACT-V0.2.40.deb"
  require_cmd awk bash chmod cmp curl debootstrap dpkg dpkg-deb find git grep install ip iptables jq mktemp nft python3 readlink rm seq sha256sum sleep ss sudo systemd-nspawn systemd-run timeout
  validate_candidate_for_restart "$1"
  validate_previous "$2"

  install -d -m 0700 "${PRIVATE_ROOT}" "${E2E_ARTIFACT_DIR}" "${XRAY_ROOT}"
  : >"${REPORT}"
  chmod 0600 "${REPORT}"
  stage_private_inputs

  trap teardown_hosted_package_restart EXIT
  run_scenario
}

if [[ "${1:-}" == validate-report ]]; then
  validate_report
  exit 0
fi

main "$@"
