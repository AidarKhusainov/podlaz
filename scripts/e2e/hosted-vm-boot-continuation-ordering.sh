#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
source "${SCRIPT_DIR}/lib/e2e.sh"
source "${SCRIPT_DIR}/lib/hosted_vm.sh"
source "${SCRIPT_DIR}/lib/hosted_vm_tun.sh"

REPORT="${E2E_ARTIFACT_DIR}/hosted-vm-boot-continuation-ordering.txt"
PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-vm-boot-continuation-ordering"
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
  fixture.synthetic_endpoint_guest
  fixture.foreign_state_persistent
  explicit_session.verified_active
  explicit_session.exact_authority
  explicit_session.current_boot
  explicit_session.no_boot_attempt
  autostart.manifest_exact_while_active
  autostart.no_same_boot_attempt
  same_boot_restart.new_process
  same_boot_restart.session_unchanged
  same_boot_restart.no_boot_attempt
  same_boot_restart.exact_authority
  same_boot_restart.privacy_protected
  same_boot_restart.foreign_state
  reboot.boot_id_changed
  candidate.provenance_after_reboot
  boot_autostart.succeeded_once
  boot_autostart.generation_exact
  boot_autostart.new_session
  boot_autostart.current_boot_session
  boot_autostart.exact_authority
  boot_autostart.privacy_protected
  boot_autostart.traffic
  boot_autostart.foreign_state
  disconnect.clean_inactive
  disconnect.exact_terminal_cleanup
  tun.recovery_clean
  guest.ordinary_connectivity_restored
  autostart.future_policy_disabled
  fixture.foreign_state_terminal
  artifact.privacy
)

evidence_recorded(){ [[ -f "${REPORT}" ]] && grep -Eq "^$1=" "${REPORT}"; }
record_evidence(){
  local key="$1" state="$2"
  [[ "${key}" =~ ^[a-z0-9_.-]+$ ]] || fail "invalid boot continuation evidence key"
  case "${state}" in pass|fail|unavailable) ;; *) fail "invalid boot continuation evidence state" ;; esac
  ! evidence_recorded "${key}" || fail "duplicate boot continuation evidence key"
  printf '%s=%s\n' "${key}" "${state}" >>"${REPORT}"
}
record_if_missing(){ evidence_recorded "$1" || record_evidence "$1" "$2"; }
mark_failure(){
  local class="$1" step="$2"
  case "${class}" in product|fixture|infrastructure|capability|diagnostic_unknown) ;; *) class=infrastructure ;; esac
  FAILURE_CLASS="${class}"
  FAILURE_STEP="${step//[^A-Za-z0-9_.-]/_}"
}
finalize_report(){
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
validate_report(){
  python3 - "${REPORT}" "${EVIDENCE_KEYS[@]}" <<'PY'
import re
import sys
from pathlib import Path
path=Path(sys.argv[1]); expected=sys.argv[2:]
if not path.is_file() or path.is_symlink():
    raise SystemExit("boot continuation report missing")
values={}; meta={}; kvm=None
for line in path.read_text(encoding="utf-8").splitlines():
    match=re.fullmatch(r"([a-z0-9_.-]+)=(pass|fail|unavailable)",line)
    if match:
        key,value=match.groups()
        if key=="capability.kvm":
            if kvm is not None: raise SystemExit("duplicate kvm")
            kvm=value
        else:
            if key in values: raise SystemExit("duplicate evidence")
            values[key]=value
        continue
    match=re.fullmatch(r"failure\.(class|step)=([A-Za-z0-9_.-]+)",line)
    if match:
        key,value=match.groups()
        if key in meta: raise SystemExit("duplicate failure metadata")
        meta[key]=value
        continue
    raise SystemExit("non-normalized report")
if set(values)!=set(expected): raise SystemExit("evidence schema mismatch")
if any(values[key]!="pass" for key in expected): raise SystemExit("required evidence incomplete")
if kvm not in {"pass","unavailable"}: raise SystemExit("kvm capability missing")
if meta!={"class":"none","step":"none"}: raise SystemExit("failure metadata not clean")
PY
}

validate_candidate(){
  local path="$1"
  [[ -f "${path}" && ! -L "${path}" ]] || fail "candidate package invalid"
  [[ "$(dpkg-deb --field "${path}" Package)" == podlaz ]] || fail "candidate is not podlaz"
  [[ "$(dpkg-deb --field "${path}" Architecture)" == amd64 ]] || fail "candidate must be amd64"
  [[ -n "${EXPECTED_COMMIT}" ]] || fail "candidate commit missing"
  CANDIDATE_DEB="$(readlink -f -- "${path}")"
}

assert_public_artifact_privacy(){
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-vm-boot-continuation-ordering.txt' -print -quit)"
  [[ -z "${extra}" ]] || return 1
  ! grep -Eq 'vless://|vmess://|trojan://|ss://|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|10[.]0[.]2[.]100' "${REPORT}"
}

manifest_generation(){
  hosted_vm_ga_bash "jq -r '.generation' /var/lib/podlaz/boot-autostart-manifest.json" | tr -d '[:space:]'
}
attempt_sha(){
  hosted_vm_ga_bash 'sha256sum /run/podlaz/boot-autostart-attempt.json' | awk '{print $1}'
}
assert_attempt_absent(){ hosted_vm_ga_bash 'test ! -e /run/podlaz/boot-autostart-attempt.json'; }
assert_manifest_exact(){
  local boot="$1" profile="$2"
  hosted_vm_ga_bash "jq -e --arg boot ${boot@Q} --arg profile ${profile@Q} '.schema_version==\"podlaz.boot-autostart-manifest.v1\" and .configured_boot_id==\$boot and (.generation|test(\"^[0-9a-f]{32}$\")) and .configuration.mode==\"tun\" and .configuration.profile.id==\$profile' /var/lib/podlaz/boot-autostart-manifest.json >/dev/null"
}
assert_succeeded_attempt(){
  local boot="$1" generation="$2" profile="$3"
  hosted_vm_ga_bash "jq -e --arg boot ${boot@Q} --arg generation ${generation@Q} --arg profile ${profile@Q} '.schema_version==\"podlaz.boot-autostart-attempt.v1\" and .boot_id==\$boot and .manifest_generation==\$generation and .state==\"succeeded\" and .configuration.mode==\"tun\" and .configuration.profile.id==\$profile and ((.terminal_reason//\"\")==\"\")' /run/podlaz/boot-autostart-attempt.json >/dev/null"
}

boot_attempt_token(){
  hosted_vm_ga_bash_stdin <<'EOF' 2>/dev/null | tr '[:upper:]' '[:lower:]' | tr -cs 'a-z0-9_.-' '-' | sed 's/^-*//; s/-*$//' || true
set -Eeuo pipefail
if ! test -f /run/podlaz/boot-autostart-attempt.json; then
  printf 'absent\n'
  exit 0
fi
state="$(jq -r '.state // "unknown"' /run/podlaz/boot-autostart-attempt.json)"
reason="$(jq -r '.terminal_reason // "none"' /run/podlaz/boot-autostart-attempt.json)"
printf '%s-%s\n' "$state" "$reason"
EOF
}

wait_succeeded_attempt(){
  local boot="$1" generation="$2" profile="$3" token
  for _ in $(seq 1 360); do
    if assert_succeeded_attempt "${boot}" "${generation}" "${profile}" >/dev/null 2>&1; then
      return 0
    fi
    token="$(boot_attempt_token)"
    case "${token}" in
      terminal-*)
        mark_failure product "boot_autostart.attempt.${token}"
        return 1
        ;;
    esac
    sleep 1
  done
  mark_failure product "boot_autostart.attempt.$(boot_attempt_token)"
  return 1
}
assert_session_current_boot(){
  local boot="$1" expected_session="$2"
  hosted_vm_ga_bash "jq -e --arg boot ${boot@Q} --arg session ${expected_session@Q} '.schema_version==\"podlaz.network-session-state.v1\" and .owner==\"podlaz\" and .boot_id==\$boot and .session_id==\$session and .intent==\"resume\" and .protection.state==\"armed\"' /run/podlaz/network-session-continuation.json >/dev/null"
}

cleanup(){
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

run_scenario(){
  local profile boot_before boot_after generation session_before session_restart session_after
  local daemon_before daemon_after reboot_ids attempt_after

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
  hosted_vm_wait_ssh 180
  hosted_vm_wait_cloud_init
  record_evidence vm.initial_boot pass

  mark_failure product candidate.install
  hosted_vm_tun_install_candidate

  mark_failure fixture guest.control
  hosted_vm_tun_prepare_control

  mark_failure product candidate.provenance
  hosted_vm_tun_assert_candidate_provenance
  record_evidence candidate.provenance pass

  mark_failure fixture guest.setup
  hosted_vm_tun_install_helpers "${REPO_ROOT}"
  hosted_vm_tun_install_polkit "${PRIVATE_ROOT}/polkit.rules"
  hosted_vm_tun_prepare_profile
  hosted_vm_tun_capture_direct_baseline
  mark_failure fixture synthetic.endpoint_guest
  hosted_vm_tun_assert_endpoint_reachable
  record_evidence fixture.synthetic_endpoint_guest pass
  hosted_vm_tun_create_foreign_state
  hosted_vm_tun_assert_foreign_state
  record_evidence fixture.foreign_state_persistent pass

  profile="$(hosted_vm_tun_profile_id)"
  boot_before="$(hosted_vm_boot_id)"

  mark_failure product explicit_session.connect
  hosted_vm_tun_connect
  record_evidence explicit_session.verified_active pass
  hosted_vm_tun_capture_exact_active_authority yes
  record_evidence explicit_session.exact_authority pass
  session_before="$(hosted_vm_tun_session_id)"
  [[ -n "${session_before}" ]]
  assert_session_current_boot "${boot_before}" "${session_before}"
  record_evidence explicit_session.current_boot pass
  assert_attempt_absent
  record_evidence explicit_session.no_boot_attempt pass

  mark_failure product autostart.enable_while_active
  hosted_vm_tun_run_podlaz autostart enable --mode tun "${profile}" >/dev/null
  assert_manifest_exact "${boot_before}" "${profile}"
  generation="$(manifest_generation)"
  [[ "${generation}" =~ ^[0-9a-f]{32}$ ]]
  record_evidence autostart.manifest_exact_while_active pass
  assert_attempt_absent
  record_evidence autostart.no_same_boot_attempt pass

  daemon_before="$(hosted_vm_ga_bash 'systemctl show -p MainPID --value podlazd.service' | tr -d '[:space:]')"
  mark_failure product continuation.same_boot_restart
  hosted_vm_ga_bash 'systemctl restart podlazd.service'
  hosted_vm_tun_wait_status verified-active 180
  daemon_after="$(hosted_vm_ga_bash 'systemctl show -p MainPID --value podlazd.service' | tr -d '[:space:]')"
  [[ -n "${daemon_after}" && "${daemon_after}" != "${daemon_before}" ]]
  record_evidence same_boot_restart.new_process pass
  session_restart="$(hosted_vm_tun_session_id)"
  [[ "${session_restart}" == "${session_before}" ]]
  assert_session_current_boot "${boot_before}" "${session_restart}"
  record_evidence same_boot_restart.session_unchanged pass
  assert_attempt_absent
  record_evidence same_boot_restart.no_boot_attempt pass
  hosted_vm_tun_capture_exact_active_authority no
  record_evidence same_boot_restart.exact_authority pass
  hosted_vm_tun_assert_direct_blocked
  record_evidence same_boot_restart.privacy_protected pass
  hosted_vm_tun_assert_foreign_state
  record_evidence same_boot_restart.foreign_state pass

  mark_failure infrastructure vm.real_reboot
  reboot_ids="$(hosted_vm_reboot)"
  boot_after="${reboot_ids#*$'\t'}"
  [[ -n "${boot_after}" && "${boot_after}" != "${boot_before}" ]]
  record_evidence reboot.boot_id_changed pass

  mark_failure product candidate.provenance_after_reboot
  hosted_vm_assert_candidate_provenance "${CANDIDATE_DEB}" "${EXPECTED_COMMIT}"
  record_evidence candidate.provenance_after_reboot pass

  mark_failure product boot_autostart.after_continuation_boundary
  wait_succeeded_attempt "${boot_after}" "${generation}" "${profile}"
  record_evidence boot_autostart.succeeded_once pass
  record_evidence boot_autostart.generation_exact pass
  hosted_vm_tun_wait_status verified-active 300
  attempt_after="$(attempt_sha)"
  [[ -n "${attempt_after}" ]]

  session_after="$(hosted_vm_tun_session_id)"
  [[ -n "${session_after}" && "${session_after}" != "${session_before}" ]]
  record_evidence boot_autostart.new_session pass
  assert_session_current_boot "${boot_after}" "${session_after}"
  record_evidence boot_autostart.current_boot_session pass

  # Replace the pre-reboot route/rule manifest with the exact authority of the
  # newly admitted current-boot transaction so terminal cleanup proves the
  # dynamic allocation that actually owns this boot.
  hosted_vm_tun_capture_exact_active_authority yes
  record_evidence boot_autostart.exact_authority pass
  hosted_vm_tun_assert_direct_blocked
  record_evidence boot_autostart.privacy_protected pass
  hosted_vm_tun_run_traffic
  record_evidence boot_autostart.traffic pass
  hosted_vm_tun_assert_foreign_state
  record_evidence boot_autostart.foreign_state pass

  mark_failure product disconnect
  hosted_vm_tun_disconnect
  record_evidence disconnect.clean_inactive pass
  hosted_vm_tun_assert_exact_terminal_cleanup
  record_evidence disconnect.exact_terminal_cleanup pass

  hosted_vm_tun_run_clean_recovery
  record_evidence tun.recovery_clean pass
  hosted_vm_tun_capture_direct_baseline
  record_evidence guest.ordinary_connectivity_restored pass

  mark_failure product autostart.disable_future
  hosted_vm_tun_run_podlaz autostart disable >/dev/null
  hosted_vm_ga_bash 'test ! -e /var/lib/podlaz/boot-autostart-manifest.json'
  [[ "$(attempt_sha)" == "${attempt_after}" ]]
  record_evidence autostart.future_policy_disabled pass

  hosted_vm_tun_assert_foreign_state
  record_evidence fixture.foreign_state_terminal pass

  mark_failure diagnostic_unknown artifact.privacy
  assert_public_artifact_privacy
  record_evidence artifact.privacy pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
}

main(){
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  require_cmd awk bash curl dpkg-deb find grep install jq mktemp python3 readlink seq sha256sum sleep ss
  validate_candidate "$1"
  install -d -m 0700 "${PRIVATE_ROOT}" "${E2E_ARTIFACT_DIR}" "${XRAY_ROOT}"
  : >"${REPORT}"
  chmod 0600 "${REPORT}"
  hosted_vm_init "${VM_ROOT}"
  trap cleanup EXIT
  run_scenario
}

if [[ "${1:-}" == validate-report ]]; then validate_report; exit 0; fi
main "$@"
