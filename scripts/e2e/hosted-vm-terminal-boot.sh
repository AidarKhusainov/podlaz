#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
source "${SCRIPT_DIR}/lib/e2e.sh"
source "${SCRIPT_DIR}/lib/hosted_vm.sh"
source "${SCRIPT_DIR}/lib/hosted_vm_tun.sh"

REPORT="${E2E_ARTIFACT_DIR}/hosted-vm-terminal-boot.txt"
PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-vm-terminal-boot"
VM_ROOT="${PRIVATE_ROOT}/vm"
XRAY_ROOT="${PRIVATE_ROOT}/synthetic-xray"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-${GITHUB_SHA:-}}"
CANDIDATE_DEB=""
FAILURE_CLASS=none
FAILURE_STEP=none
FINALIZED=false

# Guest-private diagnostics must survive the real reboot boundary; shared VM TUN helpers keep them under /var/tmp.
EVIDENCE_KEYS=(
  vm.acceleration
  vm.image_checksum
  vm.initial_boot
  candidate.provenance
  candidate.provenance_after_reboot
  fixture.foreign_state
  terminal_profile.imported
  autostart.manifest_exact
  autostart.no_same_boot_attempt_before_reboot
  reboot.boot_id_changed
  terminal_attempt.connect_failed
  terminal_attempt.generation_exact
  terminal_state.clean_inactive
  terminal_cleanup.exact
  guest.ordinary_connectivity
  fixture.foreign_state_after_terminal
  same_boot_restart.new_process
  same_boot_restart.attempt_unchanged
  same_boot_restart.clean_inactive
  same_boot_restart.no_session
  same_boot_restart.no_retry
  tun.recovery_clean
  autostart.future_policy_disabled
  artifact.privacy
)

evidence_recorded(){ [[ -f "${REPORT}" ]] && grep -Eq "^$1=" "${REPORT}"; }
record_evidence(){ local k="$1" v="$2"; [[ "${k}" =~ ^[a-z0-9_.-]+$ ]] || fail "invalid terminal boot evidence key"; case "${v}" in pass|fail|unavailable);;*) fail "invalid terminal boot evidence state";;esac; ! evidence_recorded "${k}" || fail "duplicate terminal boot evidence"; printf '%s=%s\n' "${k}" "${v}" >>"${REPORT}"; }
record_if_missing(){ evidence_recorded "$1" || record_evidence "$1" "$2"; }
mark_failure(){ local c="$1" s="$2"; case "${c}" in product|fixture|infrastructure|capability|diagnostic_unknown);;*) c=infrastructure;;esac; FAILURE_CLASS="${c}"; FAILURE_STEP="${s//[^A-Za-z0-9_.-]/_}"; }
finalize_report(){ local k kv; [[ "${FINALIZED}" == false ]] || return 0; FINALIZED=true; for k in "${EVIDENCE_KEYS[@]}";do record_if_missing "${k}" fail;done; if [[ "${HOSTED_VM_ACCEL}" == kvm ]];then kv=pass;else kv=unavailable;fi; { printf 'capability.kvm=%s\n' "${kv}"; printf 'failure.class=%s\n' "${FAILURE_CLASS}"; printf 'failure.step=%s\n' "${FAILURE_STEP}"; } >>"${REPORT}"; }
validate_report(){ python3 - "${REPORT}" "${EVIDENCE_KEYS[@]}" <<'PY'
import re,sys
from pathlib import Path
p=Path(sys.argv[1]); exp=sys.argv[2:]; vals={}; meta={}; kvm=None
if not p.is_file() or p.is_symlink(): raise SystemExit("report missing")
for line in p.read_text().splitlines():
 m=re.fullmatch(r"([a-z0-9_.-]+)=(pass|fail|unavailable)",line)
 if m:
  k,v=m.groups()
  if k=="capability.kvm": kvm=v
  else:
   if k in vals: raise SystemExit("duplicate evidence")
   vals[k]=v
  continue
 m=re.fullmatch(r"failure\.(class|step)=([A-Za-z0-9_.-]+)",line)
 if m: meta[m.group(1)]=m.group(2); continue
 raise SystemExit("non-normalized report")
if set(vals)!=set(exp) or any(vals[k]!="pass" for k in exp): raise SystemExit("required evidence incomplete")
if kvm not in {"pass","unavailable"}: raise SystemExit("kvm missing")
if meta!={"class":"none","step":"none"}: raise SystemExit("failure metadata not clean")
PY
}

validate_candidate(){ local p="$1"; [[ -f "${p}" && ! -L "${p}" ]] || fail "candidate invalid"; [[ "$(dpkg-deb --field "${p}" Package)" == podlaz ]] || fail "wrong package"; [[ "$(dpkg-deb --field "${p}" Architecture)" == amd64 ]] || fail "wrong arch"; [[ -n "${EXPECTED_COMMIT}" ]] || fail "commit missing"; CANDIDATE_DEB="$(readlink -f -- "${p}")"; }
assert_public_artifact_privacy(){ local extra; extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-vm-terminal-boot.txt' -print -quit)"; [[ -z "${extra}" ]] || return 1; ! grep -Eq 'vless://|vpn[.]invalid|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}' "${REPORT}"; }

prepare_terminal_profile(){
  local script
  script="$(cat <<'EOF'
set -Eeuo pipefail
uri='vless://00000000-0000-4000-8000-000000000001@vpn.invalid:443?security=tls&type=tcp&sni=vpn.invalid#BootTerminalFailure'
runuser -u e2e -- env XDG_CONFIG_HOME="$xdg/config" XDG_STATE_HOME="$xdg/state" XDG_CACHE_HOME="$xdg/cache" \
 /usr/bin/podlaz profile import "$uri" >"$private/terminal-import.stdout" 2>"$private/terminal-import.stderr"
awk '/^Imported profile:/ {print $3; exit}' "$private/terminal-import.stdout" >"$private/terminal-profile-id"
test -s "$private/terminal-profile-id"
profile="$(cat "$private/terminal-profile-id")"
runuser -u e2e -- env XDG_CONFIG_HOME="$xdg/config" XDG_STATE_HOME="$xdg/state" XDG_CACHE_HOME="$xdg/cache" \
 /usr/bin/podlaz profile validate "$profile" --mode tun >"$private/terminal-validate.stdout" 2>"$private/terminal-validate.stderr"
EOF
)"
  hosted_vm_ga_bash "xdg=${HOSTED_VM_TUN_XDG@Q}; private=${HOSTED_VM_TUN_PRIVATE@Q}; ${script}"
}
terminal_profile_id(){ hosted_vm_ga_bash "cat ${HOSTED_VM_TUN_PRIVATE@Q}/terminal-profile-id" | tr -d '[:space:]'; }
manifest_generation(){ hosted_vm_ga_bash "jq -r '.generation' /var/lib/podlaz/boot-autostart-manifest.json" | tr -d '[:space:]'; }
attempt_sha(){ hosted_vm_ga_bash 'sha256sum /run/podlaz/boot-autostart-attempt.json' | awk '{print $1}'; }
assert_manifest_exact(){ local boot="$1" profile="$2"; hosted_vm_ga_bash "jq -e --arg boot ${boot@Q} --arg profile ${profile@Q} '.schema_version==\"podlaz.boot-autostart-manifest.v1\" and .configured_boot_id==\$boot and (.generation|test(\"^[0-9a-f]{32}$\")) and .configuration.mode==\"tun\" and .configuration.profile.id==\$profile' /var/lib/podlaz/boot-autostart-manifest.json >/dev/null"; }
assert_terminal_attempt(){ local boot="$1" gen="$2" profile="$3"; hosted_vm_ga_bash "jq -e --arg boot ${boot@Q} --arg gen ${gen@Q} --arg profile ${profile@Q} '.schema_version==\"podlaz.boot-autostart-attempt.v1\" and .boot_id==\$boot and .manifest_generation==\$gen and .state==\"terminal\" and .terminal_reason==\"connect_failed\" and .configuration.mode==\"tun\" and .configuration.profile.id==\$profile' /run/podlaz/boot-autostart-attempt.json >/dev/null"; }
assert_attempt_absent(){ hosted_vm_ga_bash 'test ! -e /run/podlaz/boot-autostart-attempt.json'; }

wait_terminal_attempt(){
  local boot="$1" gen="$2" profile="$3"
  for _ in $(seq 1 360); do
    if assert_terminal_attempt "${boot}" "${gen}" "${profile}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  mark_failure product "terminal.boot_attempt.$(terminal_failure_token)"
  return 1
}

terminal_failure_token(){
  hosted_vm_ga_bash_stdin <<'EOF' 2>/dev/null | tr '[:upper:]' '[:lower:]' | tr -cs 'a-z0-9_.-' '-' | sed 's/^-*//; s/-*$//' || true
set -Eeuo pipefail
attempt_state=absent
attempt_reason=none
if test -f /run/podlaz/boot-autostart-attempt.json; then
  attempt_state="$(jq -r '.state // "unknown"' /run/podlaz/boot-autostart-attempt.json)"
  attempt_reason="$(jq -r '.terminal_reason // "none"' /run/podlaz/boot-autostart-attempt.json)"
fi
service_state="$(systemctl is-active podlazd.service 2>/dev/null || true)"
service_result="$(systemctl show -p Result --value podlazd.service 2>/dev/null || true)"
socket_state=absent
test -S /run/podlaz/podlazd.sock && socket_state=present
status_connection=unavailable
status_reason=none
status_transport=unavailable
if curl --fail --silent --show-error --max-time 5 --unix-socket /run/podlaz/podlazd.sock \
    http://localhost/v1/status >/var/tmp/podlaz-hosted-vm-tun/terminal-diagnostic.json 2>/dev/null; then
  status_transport=ok
  status_connection="$(jq -r '.connection // "unknown"' /var/tmp/podlaz-hosted-vm-tun/terminal-diagnostic.json)"
  status_reason="$(jq -r '.terminal_reason // "none"' /var/tmp/podlaz-hosted-vm-tun/terminal-diagnostic.json)"
else
  status_transport="curl-$?"
fi
printf 'attempt-%s-%s.status-%s-%s.transport-%s.service-%s-%s.socket-%s\n' \
  "$attempt_state" "$attempt_reason" "$status_connection" "$status_reason" "$status_transport" \
  "${service_state:-unknown}" "${service_result:-unknown}" "$socket_state"
EOF
}

assert_terminal_inactive(){
  if hosted_vm_tun_wait_status clean-inactive 120; then
    return 0
  fi
  mark_failure product "terminal.clean_inactive.$(terminal_failure_token)"
  return 1
}
assert_terminal_cleanup(){
  hosted_vm_ga_bash_stdin <<'EOF'
set -Eeuo pipefail
test ! -e /run/podlaz/network-session-continuation.json
test ! -e /run/podlaz/generated/xray.json
if test -d /run/podlaz/transactions; then test -z "$(find /run/podlaz/transactions -mindepth 1 -maxdepth 1 -type f -print -quit)"; fi
! ip link show dev podlaz0 >/dev/null 2>&1
! nft list tables 2>/dev/null | grep -E 'table inet podlaz_pe_[0-9a-f]+' >/dev/null
EOF
}
assert_ordinary_connectivity(){ hosted_vm_ga_bash 'timeout 20 getent ahostsv4 example.com >/dev/null && timeout 30 curl -4 -fsS -o /dev/null https://example.com/'; }

cleanup(){ local saved=$? failed=0; trap - EXIT; set +e; hosted_vm_tun_remove_foreign_state || failed=1; hosted_vm_remove_polkit_rule || failed=1; hosted_vm_stop || failed=1; hosted_vm_tun_stop_endpoint || failed=1; if ((failed!=0 && saved==0));then mark_failure infrastructure fixture.cleanup; saved=1;fi; finalize_report; set -e; exit "${saved}"; }

run_scenario(){
  local boot_before boot_after profile gen reboot_ids before_hash after_hash pid_before pid_after
  mark_failure capability vm.acceleration; hosted_vm_probe_acceleration; record_evidence vm.acceleration pass
  hosted_vm_tun_init "${CANDIDATE_DEB}" "${EXPECTED_COMMIT}" "${XRAY_ROOT}"
  mark_failure infrastructure vm.image; hosted_vm_prepare_image; record_evidence vm.image_checksum pass
  mark_failure infrastructure vm.initial_boot; hosted_vm_start; hosted_vm_wait_ssh 180; hosted_vm_wait_cloud_init; record_evidence vm.initial_boot pass
  mark_failure product candidate.install
  hosted_vm_tun_install_candidate
  mark_failure fixture guest.control
  hosted_vm_tun_prepare_control
  mark_failure product candidate.provenance
  hosted_vm_tun_assert_candidate_provenance
  record_evidence candidate.provenance pass
  mark_failure fixture guest.setup
  hosted_vm_scp_to "${REPO_ROOT}/scripts/e2e/lib/daemon_status_semantics.py" /var/tmp/podlaz-hosted-vm-daemon-status-semantics.py
  hosted_vm_scp_to "${REPO_ROOT}/scripts/e2e/lib/recovery_json.sh" /var/tmp/podlaz-hosted-vm-recovery-json.sh
  hosted_vm_tun_install_polkit "${PRIVATE_ROOT}/polkit.rules"
  hosted_vm_ga_bash "install -d -o e2e -g e2e -m 0700 ${HOSTED_VM_TUN_XDG@Q} ${HOSTED_VM_TUN_XDG@Q}/config ${HOSTED_VM_TUN_XDG@Q}/state ${HOSTED_VM_TUN_XDG@Q}/cache ${HOSTED_VM_TUN_PRIVATE@Q}"
  hosted_vm_tun_capture_direct_baseline
  hosted_vm_tun_create_foreign_state
  hosted_vm_tun_assert_foreign_state
  record_evidence fixture.foreign_state pass
  mark_failure product terminal_profile.import
  prepare_terminal_profile
  record_evidence terminal_profile.imported pass
  profile="$(terminal_profile_id)"; boot_before="$(hosted_vm_boot_id)"
  mark_failure product autostart.enable_terminal; hosted_vm_tun_run_podlaz autostart enable --mode tun "${profile}" >/dev/null
  assert_manifest_exact "${boot_before}" "${profile}"; gen="$(manifest_generation)"; record_evidence autostart.manifest_exact pass
  assert_attempt_absent; record_evidence autostart.no_same_boot_attempt_before_reboot pass
  mark_failure infrastructure vm.reboot; reboot_ids="$(hosted_vm_reboot)"; boot_after="${reboot_ids#*$'\t'}"; [[ -n "${boot_after}" && "${boot_after}" != "${boot_before}" ]]; record_evidence reboot.boot_id_changed pass

  mark_failure product candidate.provenance_after_reboot; hosted_vm_assert_candidate_provenance "${CANDIDATE_DEB}" "${EXPECTED_COMMIT}"; record_evidence candidate.provenance_after_reboot pass
  mark_failure product terminal.boot_attempt
  wait_terminal_attempt "${boot_after}" "${gen}" "${profile}"
  record_evidence terminal_attempt.connect_failed pass
  record_evidence terminal_attempt.generation_exact pass
  assert_terminal_inactive
  record_evidence terminal_state.clean_inactive pass
  assert_terminal_cleanup; record_evidence terminal_cleanup.exact pass
  assert_ordinary_connectivity; record_evidence guest.ordinary_connectivity pass
  hosted_vm_tun_assert_foreign_state; record_evidence fixture.foreign_state_after_terminal pass

  before_hash="$(attempt_sha)"; pid_before="$(hosted_vm_ga_bash 'systemctl show -p MainPID --value podlazd.service' | tr -d '[:space:]')"
  mark_failure product terminal.same_boot_restart; hosted_vm_ga_bash 'systemctl restart podlazd.service'; assert_terminal_inactive
  pid_after="$(hosted_vm_ga_bash 'systemctl show -p MainPID --value podlazd.service' | tr -d '[:space:]')"; [[ -n "${pid_after}" && "${pid_after}" != "${pid_before}" ]]; record_evidence same_boot_restart.new_process pass
  after_hash="$(attempt_sha)"; [[ "${after_hash}" == "${before_hash}" ]]; assert_terminal_attempt "${boot_after}" "${gen}" "${profile}"; record_evidence same_boot_restart.attempt_unchanged pass; record_evidence same_boot_restart.clean_inactive pass
  hosted_vm_ga_bash 'test ! -e /run/podlaz/network-session-continuation.json'; record_evidence same_boot_restart.no_session pass
  assert_terminal_cleanup; hosted_vm_tun_assert_foreign_state; record_evidence same_boot_restart.no_retry pass

  hosted_vm_tun_run_clean_recovery; record_evidence tun.recovery_clean pass
  mark_failure product autostart.disable_future; hosted_vm_tun_run_podlaz autostart disable >/dev/null; hosted_vm_ga_bash 'test ! -e /var/lib/podlaz/boot-autostart-manifest.json'; [[ "$(attempt_sha)" == "${before_hash}" ]]; record_evidence autostart.future_policy_disabled pass
  mark_failure diagnostic_unknown artifact.privacy; assert_public_artifact_privacy; record_evidence artifact.privacy pass
  FAILURE_CLASS=none; FAILURE_STEP=none
}
main(){ (($#==1)) || fail "usage: $0 CANDIDATE.deb"; require_cmd awk bash curl dpkg-deb find grep install jq mktemp python3 readlink sed seq sha256sum sleep ss tr; validate_candidate "$1"; install -d -m 0700 "${PRIVATE_ROOT}" "${E2E_ARTIFACT_DIR}" "${XRAY_ROOT}"; : >"${REPORT}"; chmod 0600 "${REPORT}"; hosted_vm_init "${VM_ROOT}"; trap cleanup EXIT; run_scenario; }
if [[ "${1:-}" == validate-report ]];then validate_report;exit 0;fi
main "$@"
