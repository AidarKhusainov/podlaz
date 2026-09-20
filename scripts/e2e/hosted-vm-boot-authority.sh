#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
source "${SCRIPT_DIR}/lib/e2e.sh"
source "${SCRIPT_DIR}/lib/hosted_vm.sh"
source "${SCRIPT_DIR}/lib/hosted_vm_tun.sh"

REPORT="${E2E_ARTIFACT_DIR}/hosted-vm-boot-authority.txt"
PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-vm-boot-authority"
VM_ROOT="${PRIVATE_ROOT}/vm"
XRAY_ROOT="${PRIVATE_ROOT}/synthetic-xray"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-${GITHUB_SHA:-}}"
CANDIDATE_DEB=""
FAILURE_CLASS=none
FAILURE_STEP=none
FINALIZED=false

EVIDENCE_KEYS=(
  vm.acceleration
  vm.image_checksum
  vm.initial_boot
  candidate.provenance
  fixture.synthetic_endpoint
  fixture.foreign_state
  autostart.manifest_exact
  autostart.no_same_boot_attempt_before_reboot
  reboot.boot_id_changed
  autostart.succeeded_once
  autostart.attempt_generation_exact
  tun.verified_active_after_boot
  tun.exact_authority_after_boot
  privacy.direct_uplink_blocked_after_boot
  tun.traffic_after_boot
  daemon_restart.new_process
  daemon_restart.attempt_unchanged
  daemon_restart.session_unchanged
  daemon_restart.exact_authority
  daemon_restart.privacy_protected
  explicit_disconnect.clean_inactive
  explicit_disconnect.attempt_unchanged
  explicit_disconnect.exact_terminal_cleanup
  same_boot_restart.clean_inactive
  same_boot_restart.attempt_unchanged
  same_boot_restart.no_session
  tun.recovery_clean
  guest.ordinary_connectivity_restored
  fixture.foreign_state_terminal
  autostart.future_policy_disabled
  artifact.privacy
)

evidence_recorded() { [[ -f "${REPORT}" ]] && grep -Eq "^$1=" "${REPORT}"; }
record_evidence() {
  local key="$1" state="$2"
  [[ "${key}" =~ ^[a-z0-9_.-]+$ ]] || fail "invalid boot authority evidence key"
  case "${state}" in pass|fail|unavailable) ;; *) fail "invalid boot authority evidence state" ;; esac
  ! evidence_recorded "${key}" || fail "duplicate boot authority evidence key"
  printf '%s=%s\n' "${key}" "${state}" >>"${REPORT}"
}
record_if_missing() { evidence_recorded "$1" || record_evidence "$1" "$2"; }
mark_failure() {
  local class="$1" step="$2"
  case "${class}" in product|fixture|infrastructure|capability|diagnostic_unknown) ;; *) class=infrastructure ;; esac
  FAILURE_CLASS="${class}"
  FAILURE_STEP="${step//[^A-Za-z0-9_.-]/_}"
}
finalize_report() {
  local key kvm
  [[ "${FINALIZED}" == false ]] || return 0
  FINALIZED=true
  for key in "${EVIDENCE_KEYS[@]}"; do record_if_missing "${key}" fail; done
  if [[ "${HOSTED_VM_ACCEL}" == kvm ]]; then kvm=pass; else kvm=unavailable; fi
  {
    printf 'capability.kvm=%s\n' "${kvm}"
    printf 'failure.class=%s\n' "${FAILURE_CLASS}"
    printf 'failure.step=%s\n' "${FAILURE_STEP}"
  } >>"${REPORT}"
}
validate_report() {
  python3 - "${REPORT}" "${EVIDENCE_KEYS[@]}" <<'PY'
import re, sys
from pathlib import Path
path=Path(sys.argv[1]); expected=sys.argv[2:]
if not path.is_file() or path.is_symlink(): raise SystemExit("report missing")
values={}; meta={}; kvm=None
for line in path.read_text().splitlines():
    m=re.fullmatch(r"([a-z0-9_.-]+)=(pass|fail|unavailable)",line)
    if m:
        key,value=m.groups()
        if key=="capability.kvm":
            if kvm is not None: raise SystemExit("duplicate kvm")
            kvm=value
        else:
            if key in values: raise SystemExit("duplicate evidence")
            values[key]=value
        continue
    m=re.fullmatch(r"failure\.(class|step)=([A-Za-z0-9_.-]+)",line)
    if m:
        key,value=m.groups()
        if key in meta: raise SystemExit("duplicate failure metadata")
        meta[key]=value
        continue
    raise SystemExit("non-normalized report")
if set(values)!=set(expected): raise SystemExit("evidence schema mismatch")
if any(values[k]!="pass" for k in expected): raise SystemExit("required evidence failed")
if kvm not in {"pass","unavailable"}: raise SystemExit("kvm capability missing")
if meta!={"class":"none","step":"none"}: raise SystemExit("failure metadata not clean")
PY
}

validate_candidate() {
  local path="$1"
  [[ -f "${path}" && ! -L "${path}" ]] || fail "candidate package invalid"
  [[ "$(dpkg-deb --field "${path}" Package)" == podlaz ]] || fail "candidate is not podlaz"
  [[ "$(dpkg-deb --field "${path}" Architecture)" == amd64 ]] || fail "candidate must be amd64"
  [[ -n "${EXPECTED_COMMIT}" ]] || fail "candidate commit missing"
  CANDIDATE_DEB="$(readlink -f -- "${path}")"
}

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-vm-boot-authority.txt' -print -quit)"
  [[ -z "${extra}" ]] || return 1
  ! grep -Eq 'vless://|vmess://|trojan://|ss://|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|10[.]0[.]2[.]100' "${REPORT}"
}

attempt_sha() {
  hosted_vm_ssh sudo sha256sum /run/podlaz/boot-autostart-attempt.json | awk '{print $1}'
}
manifest_generation() {
  hosted_vm_ssh sudo jq -r '.generation' /var/lib/podlaz/boot-autostart-manifest.json | tr -d '[:space:]'
}
assert_manifest_exact() {
  local boot="$1" profile="$2"
  hosted_vm_ssh sudo jq -e --arg boot "${boot}" --arg profile "${profile}"     '.schema_version=="podlaz.boot-autostart-manifest.v1" and .configured_boot_id==$boot and
     (.generation|test("^[0-9a-f]{32}$")) and .configuration.mode=="tun" and .configuration.profile.id==$profile'     /var/lib/podlaz/boot-autostart-manifest.json >/dev/null
}
assert_attempt_exact() {
  local boot="$1" generation="$2" profile="$3" state="$4"
  hosted_vm_ssh sudo jq -e --arg boot "${boot}" --arg generation "${generation}" --arg profile "${profile}" --arg state "${state}"     '.schema_version=="podlaz.boot-autostart-attempt.v1" and .boot_id==$boot and
     .manifest_generation==$generation and .state==$state and .configuration.mode=="tun" and
     .configuration.profile.id==$profile and ((.terminal_reason//"")=="")'     /run/podlaz/boot-autostart-attempt.json >/dev/null
}
assert_attempt_absent() { hosted_vm_ssh sudo test ! -e /run/podlaz/boot-autostart-attempt.json; }
assert_session_absent() { hosted_vm_ssh sudo test ! -e /run/podlaz/network-session-continuation.json; }

cleanup() {
  local saved=$? failed=0
  trap - EXIT
  set +e
  hosted_vm_tun_remove_foreign_state || failed=1
  hosted_vm_remove_polkit_rule || failed=1
  hosted_vm_stop || failed=1
  hosted_vm_tun_stop_endpoint || failed=1
  if (( failed != 0 && saved == 0 )); then mark_failure infrastructure fixture.cleanup; saved=1; fi
  finalize_report
  set -e
  exit "${saved}"
}

run_scenario() {
  local profile boot_before boot_after generation attempt_before session_before
  local daemon_before daemon_after attempt_after session_after attempt_disconnected

  mark_failure capability vm.acceleration
  hosted_vm_probe_acceleration
  record_evidence vm.acceleration pass

  hosted_vm_tun_init "${CANDIDATE_DEB}" "${EXPECTED_COMMIT}" "${XRAY_ROOT}"
  mark_failure fixture synthetic.endpoint
  hosted_vm_tun_start_endpoint
  record_evidence fixture.synthetic_endpoint pass

  mark_failure infrastructure vm.image
  hosted_vm_prepare_image
  record_evidence vm.image_checksum pass

  mark_failure infrastructure vm.initial_boot
  hosted_vm_start
  hosted_vm_wait_ssh
  hosted_vm_wait_cloud_init
  record_evidence vm.initial_boot pass

  mark_failure product candidate.install
  hosted_vm_tun_install_candidate_and_tools
  record_evidence candidate.provenance pass

  mark_failure fixture guest.setup
  hosted_vm_tun_install_helpers "${REPO_ROOT}"
  hosted_vm_tun_install_polkit "${PRIVATE_ROOT}/polkit.rules"
  hosted_vm_tun_prepare_profile
  hosted_vm_tun_capture_direct_baseline
  hosted_vm_tun_create_foreign_state
  hosted_vm_tun_assert_foreign_state
  record_evidence fixture.foreign_state pass

  profile="$(hosted_vm_tun_profile_id)"
  boot_before="$(hosted_vm_boot_id)"
  [[ -n "${profile}" && -n "${boot_before}" ]]

  mark_failure product autostart.enable
  hosted_vm_tun_run_podlaz autostart enable --mode tun "${profile}" >/dev/null
  assert_manifest_exact "${boot_before}" "${profile}"
  generation="$(manifest_generation)"
  [[ "${generation}" =~ ^[0-9a-f]{32}$ ]]
  record_evidence autostart.manifest_exact pass

  assert_attempt_absent
  hosted_vm_tun_wait_status clean-inactive 60
  record_evidence autostart.no_same_boot_attempt_before_reboot pass

  mark_failure infrastructure vm.reboot
  local reboot_ids
  reboot_ids="$(hosted_vm_reboot)"
  boot_after="${reboot_ids#*$'\t'}"
  [[ -n "${boot_after}" && "${boot_after}" != "${boot_before}" ]]
  record_evidence reboot.boot_id_changed pass

  mark_failure product autostart.boot_connect
  hosted_vm_tun_wait_status verified-active 180
  record_evidence tun.verified_active_after_boot pass

  assert_attempt_exact "${boot_after}" "${generation}" "${profile}" succeeded
  record_evidence autostart.succeeded_once pass
  record_evidence autostart.attempt_generation_exact pass

  hosted_vm_tun_capture_exact_active_authority yes
  record_evidence tun.exact_authority_after_boot pass
  hosted_vm_tun_assert_direct_blocked
  record_evidence privacy.direct_uplink_blocked_after_boot pass
  hosted_vm_tun_run_traffic
  record_evidence tun.traffic_after_boot pass

  attempt_before="$(attempt_sha)"
  session_before="$(hosted_vm_tun_session_id)"
  daemon_before="$(hosted_vm_ssh sudo systemctl show -p MainPID --value podlazd.service | tr -d '[:space:]')"

  mark_failure product daemon.same_boot_restart
  hosted_vm_ssh sudo systemctl restart podlazd.service
  hosted_vm_tun_wait_status verified-active 180
  daemon_after="$(hosted_vm_ssh sudo systemctl show -p MainPID --value podlazd.service | tr -d '[:space:]')"
  [[ -n "${daemon_after}" && "${daemon_after}" != "${daemon_before}" ]]
  record_evidence daemon_restart.new_process pass

  attempt_after="$(attempt_sha)"
  [[ "${attempt_after}" == "${attempt_before}" ]]
  assert_attempt_exact "${boot_after}" "${generation}" "${profile}" succeeded
  record_evidence daemon_restart.attempt_unchanged pass

  session_after="$(hosted_vm_tun_session_id)"
  [[ "${session_after}" == "${session_before}" ]]
  record_evidence daemon_restart.session_unchanged pass

  hosted_vm_tun_capture_exact_active_authority no
  record_evidence daemon_restart.exact_authority pass
  hosted_vm_tun_assert_direct_blocked
  record_evidence daemon_restart.privacy_protected pass
  hosted_vm_tun_assert_foreign_state

  mark_failure product explicit_disconnect
  hosted_vm_tun_disconnect
  record_evidence explicit_disconnect.clean_inactive pass
  attempt_disconnected="$(attempt_sha)"
  [[ "${attempt_disconnected}" == "${attempt_before}" ]]
  assert_attempt_exact "${boot_after}" "${generation}" "${profile}" succeeded
  record_evidence explicit_disconnect.attempt_unchanged pass

  hosted_vm_tun_assert_exact_terminal_cleanup
  record_evidence explicit_disconnect.exact_terminal_cleanup pass

  mark_failure product same_boot_restart_after_disconnect
  hosted_vm_ssh sudo systemctl restart podlazd.service
  hosted_vm_tun_wait_status clean-inactive 100
  record_evidence same_boot_restart.clean_inactive pass
  [[ "$(attempt_sha)" == "${attempt_before}" ]]
  assert_attempt_exact "${boot_after}" "${generation}" "${profile}" succeeded
  record_evidence same_boot_restart.attempt_unchanged pass
  assert_session_absent
  record_evidence same_boot_restart.no_session pass

  hosted_vm_tun_run_clean_recovery
  record_evidence tun.recovery_clean pass
  hosted_vm_tun_capture_direct_baseline
  record_evidence guest.ordinary_connectivity_restored pass
  hosted_vm_tun_assert_foreign_state
  record_evidence fixture.foreign_state_terminal pass

  mark_failure product autostart.disable_future
  hosted_vm_tun_run_podlaz autostart disable >/dev/null
  hosted_vm_ssh sudo test ! -e /var/lib/podlaz/boot-autostart-manifest.json
  [[ "$(attempt_sha)" == "${attempt_before}" ]]
  record_evidence autostart.future_policy_disabled pass

  mark_failure diagnostic_unknown artifact.privacy
  assert_public_artifact_privacy
  record_evidence artifact.privacy pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
}

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  require_cmd awk bash curl dpkg-deb find grep install jq mktemp python3 readlink seq sha256sum sleep ss
  validate_candidate "$1"
  install -d -m 0700 "${PRIVATE_ROOT}" "${E2E_ARTIFACT_DIR}" "${XRAY_ROOT}"
  : >"${REPORT}"; chmod 0600 "${REPORT}"
  hosted_vm_init "${VM_ROOT}"
  trap cleanup EXIT
  run_scenario
}
if [[ "${1:-}" == validate-report ]]; then validate_report; exit 0; fi
main "$@"
