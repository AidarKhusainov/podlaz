#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/hosted_vm.sh
source "${SCRIPT_DIR}/lib/hosted_vm.sh"

REPORT="${E2E_ARTIFACT_DIR}/hosted-vm-reboot-autostart-disabled.txt"
PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-vm-reboot-autostart-disabled"
VM_ROOT="${PRIVATE_ROOT}/vm"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-${GITHUB_SHA:-}}"
CANDIDATE_DEB=""
FAILURE_CLASS=none
FAILURE_STEP=none
FINALIZED=false

EVIDENCE_KEYS=(
  vm.acceleration
  vm.image_checksum
  vm.boot
  candidate.provenance
  autostart.disabled_before_reboot
  reboot.boot_id_changed
  candidate.provenance_after_reboot
  autostart.manifest_absent
  autostart.attempt_absent
  podlaz.clean_inactive
  podlaz.terminal_authority_clean
  guest.ordinary_connectivity
  artifact.privacy
)

evidence_recorded() {
  local key="$1"
  [[ -f "${REPORT}" ]] && grep -Eq "^${key}=" "${REPORT}"
}

record_evidence() {
  local key="$1" state="$2"
  [[ "${key}" =~ ^[a-z0-9_.-]+$ ]] || fail "invalid hosted VM evidence key"
  case "${state}" in
    pass|fail|unavailable) ;;
    *) fail "invalid hosted VM evidence state for ${key}" ;;
  esac
  ! evidence_recorded "${key}" || fail "duplicate hosted VM evidence key: ${key}"
  printf '%s=%s\n' "${key}" "${state}" >>"${REPORT}"
}

record_if_missing() {
  local key="$1" state="$2"
  evidence_recorded "${key}" || record_evidence "${key}" "${state}"
}

mark_failure() {
  local class="$1" step="$2"
  case "${class}" in
    product|fixture|infrastructure|capability|diagnostic_unknown) ;;
    *) class=infrastructure ;;
  esac
  FAILURE_CLASS="${class}"
  FAILURE_STEP="${step//[^A-Za-z0-9_.-]/_}"
}

finalize_report() {
  local key kvm_state
  [[ "${FINALIZED}" == false ]] || return 0
  FINALIZED=true
  for key in "${EVIDENCE_KEYS[@]}"; do
    record_if_missing "${key}" fail
  done
  printf 'capability.kvm=%s\n' "$([[ "${HOSTED_VM_ACCEL}" == kvm ]] && printf pass || printf unavailable)" >>"${REPORT}"
  printf 'failure.class=%s\n' "${FAILURE_CLASS}" >>"${REPORT}"
  printf 'failure.step=%s\n' "${FAILURE_STEP}" >>"${REPORT}"
}

validate_report() {
  python3 - "${REPORT}" "${EVIDENCE_KEYS[@]}" <<'PY'
import re
import sys
from pathlib import Path

path = Path(sys.argv[1])
expected = sys.argv[2:]
if not path.is_file() or path.is_symlink():
    raise SystemExit("hosted VM reboot report is missing or invalid")
values = {}
meta = {}
kvm = None
for line in path.read_text(encoding="utf-8").splitlines():
    match = re.fullmatch(r"([a-z0-9_.-]+)=(pass|fail|unavailable)", line)
    if match:
        key, value = match.groups()
        if key == "capability.kvm":
            if kvm is not None:
                raise SystemExit("duplicate KVM capability evidence")
            kvm = value
            continue
        if key in values:
            raise SystemExit(f"duplicate evidence key: {key}")
        values[key] = value
        continue
    match = re.fullmatch(r"failure\.(class|step)=([A-Za-z0-9_.-]+)", line)
    if match:
        key, value = match.groups()
        if key in meta:
            raise SystemExit(f"duplicate failure metadata: {key}")
        meta[key] = value
        continue
    raise SystemExit("hosted VM reboot report contains non-normalized data")
if set(values) != set(expected):
    raise SystemExit(f"evidence schema mismatch: expected={sorted(expected)} got={sorted(values)}")
if any(values[key] != "pass" for key in expected):
    raise SystemExit("required hosted VM reboot evidence is not successful")
if kvm not in {"pass", "unavailable"}:
    raise SystemExit("KVM capability was not reported")
if meta != {"class": "none", "step": "none"}:
    raise SystemExit(f"scenario failure metadata is not clean: {meta}")
PY
}

validate_candidate() {
  local path="$1"
  [[ -f "${path}" && ! -L "${path}" ]] || fail "candidate package must be a regular non-symlink file"
  [[ "$(dpkg-deb --field "${path}" Package)" == podlaz ]] || fail "candidate package is not podlaz"
  [[ "$(dpkg-deb --field "${path}" Architecture)" == amd64 ]] || fail "hosted VM candidate must be amd64"
  [[ -n "${EXPECTED_COMMIT}" ]] || fail "candidate commit provenance is required"
  CANDIDATE_DEB="$(readlink -f -- "${path}")"
}

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-vm-reboot-autostart-disabled.txt' -print -quit)"
  [[ -z "${extra}" ]] || return 1
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || return 1
  ! grep -Eq 'vless://|vmess://|trojan://|ss://|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}' "${REPORT}"
}

run_podlaz() {
  hosted_vm_ssh env     XDG_CONFIG_HOME=/home/e2e/.config     XDG_STATE_HOME=/home/e2e/.local/state     XDG_CACHE_HOME=/home/e2e/.cache     /usr/bin/podlaz "$@"
}

assert_autostart_disabled() {
  local output
  output="$(run_podlaz autostart status)"
  grep -Fx 'Autostart: Disabled' <<<"${output}" >/dev/null
}

assert_attempt_absent() {
  hosted_vm_ssh sudo test ! -e /run/podlaz/boot-autostart-attempt.json
}

assert_manifest_absent() {
  hosted_vm_ssh sudo test ! -e /var/lib/podlaz/boot-autostart-manifest.json
}

assert_clean_inactive() {
  hosted_vm_ssh sudo python3 - <<'PY'
import json
import socket

request = b"GET /v1/status HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
sock.settimeout(5)
sock.connect("/run/podlaz/podlazd.sock")
sock.sendall(request)
data = b""
while True:
    chunk = sock.recv(65536)
    if not chunk:
        break
    data += chunk
sock.close()
head, body = data.split(b"\r\n\r\n", 1)
if b" 200 " not in head.splitlines()[0]:
    raise SystemExit("daemon status returned non-200")
status = json.loads(body)
if status.get("connection") != "inactive" or status.get("tun") != "disabled":
    raise SystemExit("daemon did not converge clean inactive")
PY
}

assert_terminal_authority_clean() {
  hosted_vm_ssh sudo bash -s <<'EOF'
set -Eeuo pipefail
test ! -e /run/podlaz/network-session-continuation.json
test ! -e /run/podlaz/boot-autostart-attempt.json
test ! -e /run/podlaz/generated/xray.json
if test -d /run/podlaz/transactions; then
  test -z "$(find /run/podlaz/transactions -mindepth 1 -maxdepth 1 -type f -print -quit)"
fi
! ip link show dev podlaz0 >/dev/null 2>&1
! nft list tables 2>/dev/null | grep -E 'table inet podlaz_pe_[0-9a-f]+' >/dev/null
EOF
}

assert_ordinary_connectivity() {
  hosted_vm_ssh timeout 20 getent ahostsv4 example.com >/dev/null
  hosted_vm_ssh timeout 30 curl -4 -fsS -o /dev/null https://example.com/
}

cleanup() {
  local saved=$? cleanup_failed=0
  trap - EXIT
  set +e
  hosted_vm_remove_polkit_rule || cleanup_failed=1
  hosted_vm_stop || cleanup_failed=1
  if (( cleanup_failed != 0 )) && (( saved == 0 )); then
    mark_failure infrastructure vm.cleanup
    saved=1
  fi
  finalize_report
  set -e
  exit "${saved}"
}

run_scenario() {
  local reboot_ids before_boot after_boot

  mark_failure capability vm.acceleration
  hosted_vm_probe_acceleration
  record_evidence vm.acceleration pass

  mark_failure infrastructure vm.image
  hosted_vm_prepare_image
  record_evidence vm.image_checksum pass

  mark_failure infrastructure vm.boot
  hosted_vm_start
  hosted_vm_wait_ssh
  hosted_vm_wait_cloud_init
  # Expansion is intentionally evaluated by the guest shell.
  # shellcheck disable=SC2016
  hosted_vm_ssh '. /etc/os-release; test "$ID" = ubuntu; test "$VERSION_ID" = 24.04'
  record_evidence vm.boot pass

  mark_failure product candidate.install
  hosted_vm_install_candidate "${CANDIDATE_DEB}"
  hosted_vm_assert_candidate_provenance "${CANDIDATE_DEB}" "${EXPECTED_COMMIT}"
  record_evidence candidate.provenance pass

  mark_failure fixture autostart.authorization
  hosted_vm_install_polkit_rule io.github.aidarkhusainov.podlaz.configure-autostart

  mark_failure product autostart.disable
  run_podlaz autostart disable >/dev/null
  assert_autostart_disabled
  assert_manifest_absent
  assert_attempt_absent
  record_evidence autostart.disabled_before_reboot pass

  mark_failure infrastructure vm.reboot
  reboot_ids="$(hosted_vm_reboot)"
  before_boot="${reboot_ids%%$'\t'*}"
  after_boot="${reboot_ids#*$'\t'}"
  [[ -n "${before_boot}" && -n "${after_boot}" && "${before_boot}" != "${after_boot}" ]]
  record_evidence reboot.boot_id_changed pass

  mark_failure product candidate.provenance_after_reboot
  hosted_vm_assert_candidate_provenance "${CANDIDATE_DEB}" "${EXPECTED_COMMIT}"
  record_evidence candidate.provenance_after_reboot pass

  mark_failure product autostart.disabled_after_reboot
  assert_autostart_disabled
  assert_manifest_absent
  record_evidence autostart.manifest_absent pass

  assert_attempt_absent
  record_evidence autostart.attempt_absent pass

  assert_clean_inactive
  record_evidence podlaz.clean_inactive pass

  mark_failure product terminal.cleanup
  assert_terminal_authority_clean
  record_evidence podlaz.terminal_authority_clean pass

  mark_failure infrastructure guest.connectivity
  assert_ordinary_connectivity
  record_evidence guest.ordinary_connectivity pass

  mark_failure diagnostic_unknown artifact.privacy
  assert_public_artifact_privacy
  record_evidence artifact.privacy pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
}

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  require_cmd awk bash curl dpkg-deb find grep install mktemp python3 readlink sha256sum
  validate_candidate "$1"
  install -d -m 0700 "${PRIVATE_ROOT}" "${E2E_ARTIFACT_DIR}"
  : >"${REPORT}"
  chmod 0600 "${REPORT}"
  hosted_vm_init "${VM_ROOT}"
  trap cleanup EXIT
  run_scenario
}

if [[ "${1:-}" == validate-report ]]; then
  validate_report
  exit 0
fi

main "$@"
