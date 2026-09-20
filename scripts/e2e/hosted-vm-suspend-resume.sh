#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/hosted_vm.sh
source "${SCRIPT_DIR}/lib/hosted_vm.sh"

REPORT="${E2E_ARTIFACT_DIR}/hosted-vm-suspend-resume.txt"
PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-vm-suspend-resume"
VM_ROOT="${PRIVATE_ROOT}/vm"
XRAY_ROOT="${PRIVATE_ROOT}/synthetic-xray"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-${GITHUB_SHA:-}}"
CANDIDATE_DEB=""
XRAY_PID=""
VM_ENDPOINT_IP="${HOSTED_VM_PROVIDER_HOST_IP}"
FAILURE_CLASS=none
FAILURE_STEP=none
FINALIZED=false

EVIDENCE_KEYS=(
  vm.acceleration
  vm.image_checksum
  vm.boot
  vm.suspend_wakeup_capability
  candidate.provenance
  fixture.synthetic_endpoint
  fixture.synthetic_endpoint_guest
  fixture.foreign_state
  tun.verified_active_before_suspend
  tun.exact_authority_before_suspend
  privacy.direct_uplink_blocked_before_suspend
  tun.traffic_before_suspend
  suspend.actual_guest_boundary
  suspend.same_boot
  privacy.direct_uplink_blocked_after_wakeup
  tun.verified_active_after_wakeup
  tun.same_network_session
  tun.exact_authority_after_wakeup
  fixture.foreign_state_after_wakeup
  tun.traffic_after_wakeup
  tun.clean_disconnect
  tun.exact_terminal_cleanup
  tun.recovery_clean
  guest.ordinary_connectivity_restored
  fixture.foreign_state_terminal
  artifact.privacy
)

evidence_recorded() {
  local key="$1"
  [[ -f "${REPORT}" ]] && grep -Eq "^${key}=" "${REPORT}"
}

record_evidence() {
  local key="$1" state="$2"
  [[ "${key}" =~ ^[a-z0-9_.-]+$ ]] || fail "invalid hosted VM suspend evidence key"
  case "${state}" in
    pass|fail|unavailable) ;;
    *) fail "invalid hosted VM suspend evidence state for ${key}" ;;
  esac
  ! evidence_recorded "${key}" || fail "duplicate hosted VM suspend evidence key: ${key}"
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
  if [[ "${HOSTED_VM_ACCEL}" == kvm ]]; then kvm_state=pass; else kvm_state=unavailable; fi
  {
    printf 'capability.kvm=%s\n' "${kvm_state}"
    printf 'failure.class=%s\n' "${FAILURE_CLASS}"
    printf 'failure.step=%s\n' "${FAILURE_STEP}"
  } >>"${REPORT}"
}

validate_report() {
  python3 - "${REPORT}" "${EVIDENCE_KEYS[@]}" <<'PY'
import re
import sys
from pathlib import Path

path = Path(sys.argv[1])
expected = sys.argv[2:]
if not path.is_file() or path.is_symlink():
    raise SystemExit("hosted VM suspend report is missing or invalid")
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
    raise SystemExit("hosted VM suspend report contains non-normalized data")
if set(values) != set(expected):
    raise SystemExit(f"evidence schema mismatch: expected={sorted(expected)} got={sorted(values)}")
if any(values[key] != "pass" for key in expected):
    raise SystemExit("required hosted VM suspend evidence is not successful")
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
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-vm-suspend-resume.txt' -print -quit)"
  [[ -z "${extra}" ]] || return 1
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || return 1
  ! grep -Eq 'vless://|vmess://|trojan://|ss://|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|10[.]0[.]2[.]100' "${REPORT}"
}

start_synthetic_endpoint() {
  local extract config uuid port
  extract="${XRAY_ROOT}/package"
  config="${XRAY_ROOT}/server.json"
  install -d -m 0700 "${XRAY_ROOT}" "${extract}"
  dpkg-deb -x "${CANDIDATE_DEB}" "${extract}"
  uuid="$("${extract}/usr/lib/podlaz/xray" uuid | tr -d '[:space:]')"
  [[ "${uuid}" =~ ^[0-9a-fA-F-]{36}$ ]] || return 1
  hosted_vm_provider_prepare_host
  port="$(python3 - "${VM_ENDPOINT_IP}" <<'PY'
import socket
import sys
sock = socket.socket()
sock.bind((sys.argv[1], 0))
print(sock.getsockname()[1])
sock.close()
PY
)"
  cat >"${config}" <<EOF
{
  "log": {"loglevel": "warning"},
  "inbounds": [{
    "listen": "${VM_ENDPOINT_IP}",
    "port": ${port},
    "protocol": "vless",
    "settings": {"clients": [{"id": "${uuid}"}], "decryption": "none"},
    "streamSettings": {"security": "none"}
  }],
  "outbounds": [{"protocol": "freedom", "settings": {}}]
}
EOF
  chmod 0600 "${config}"
  "${extract}/usr/lib/podlaz/xray" run -test -config "${config}" >"${XRAY_ROOT}/config-test.log" 2>&1
  "${extract}/usr/lib/podlaz/xray" run -config "${config}" >"${XRAY_ROOT}/server.log" 2>&1 &
  XRAY_PID=$!
  for _ in $(seq 1 100); do
    if ss -H -ltn | awk '{print $4}' | grep -Fx "${VM_ENDPOINT_IP}:${port}" >/dev/null; then
      break
    fi
    kill -0 "${XRAY_PID}" >/dev/null 2>&1 || return 1
    sleep 0.1
  done
  ss -H -ltn | awk '{print $4}' | grep -Fx "${VM_ENDPOINT_IP}:${port}" >/dev/null || return 1
  printf 'vless://%s@%s:%s?type=tcp&security=none&encryption=none#hosted-vm-suspend\n'     "${uuid}" "${VM_ENDPOINT_IP}" "${port}" >"${XRAY_ROOT}/client-uri"
  chmod 0600 "${XRAY_ROOT}/client-uri"
  :
}

stop_synthetic_endpoint() {
  if [[ -n "${XRAY_PID}" ]]; then
    kill "${XRAY_PID}" >/dev/null 2>&1 || true
    wait "${XRAY_PID}" >/dev/null 2>&1 || true
    XRAY_PID=""
  fi
}

install_guest_helpers() {
  hosted_vm_scp_to "${REPO_ROOT}/scripts/e2e/lib/daemon_status_semantics.py" /tmp/daemon_status_semantics.py
  hosted_vm_scp_to "${REPO_ROOT}/scripts/e2e/hosted_synthetic_active_authority.py" /tmp/active_authority.py
  hosted_vm_scp_to "${REPO_ROOT}/scripts/e2e/hosted_synthetic_network_authority.py" /tmp/network_authority.py
  hosted_vm_scp_to "${REPO_ROOT}/scripts/e2e/lib/recovery_json.sh" /tmp/recovery_json.sh
  hosted_vm_scp_to "${XRAY_ROOT}/client-uri" /tmp/client-uri
}

assert_synthetic_endpoint_guest_reachable() {
  hosted_vm_ga_bash_stdin <<'EOF'
set -Eeuo pipefail
python3 - <<'PY'
import socket
from urllib.parse import urlsplit

uri = open("/tmp/client-uri", encoding="utf-8").read().strip()
parsed = urlsplit(uri)
if not parsed.hostname or not parsed.port:
    raise SystemExit("synthetic endpoint URI has no host/port")
with socket.create_connection((parsed.hostname, parsed.port), timeout=5):
    pass
PY
EOF
}

diagnose_connect_failure() {
  local token
  token="$(hosted_vm_ga_bash_stdin <<'EOF' 2>/dev/null || true
set -Eeuo pipefail
if grep -Eqi 'authorization (denied|unavailable)' /tmp/podlaz-vm-private/connect.stderr; then
  printf 'authorization\n'
  exit 0
fi
curl --fail --silent --show-error --max-time 5 --unix-socket /run/podlaz/podlazd.sock \
  http://localhost/v1/status >/tmp/podlaz-vm-private/status.json 2>/dev/null || {
  printf 'status-unavailable\n'
  exit 0
}
status_token="$(python3 /tmp/daemon_status_semantics.py diagnose-active /tmp/podlaz-vm-private/status.json 2>/dev/null || printf 'status-unclassified')"
set +e
runuser -u e2e -- env XDG_CONFIG_HOME=/home/e2e/.config XDG_STATE_HOME=/home/e2e/.local/state XDG_CACHE_HOME=/home/e2e/.cache \
  /usr/bin/podlaz doctor --tun --json >/tmp/podlaz-vm-private/doctor.json 2>/tmp/podlaz-vm-private/doctor.stderr
doctor_rc=$?
set -e
doctor_token="$(python3 - "$doctor_rc" /tmp/podlaz-vm-private/doctor.json <<'PY'
import json
import re
import sys

rc = int(sys.argv[1])
path = sys.argv[2]
if rc not in (0, 3):
    print("doctor-unavailable")
    raise SystemExit(0)
try:
    with open(path, encoding="utf-8") as handle:
        report = json.load(handle)
except Exception:
    print("doctor-invalid")
    raise SystemExit(0)

def safe(value):
    value = str(value or "").strip().lower()
    return value if re.fullmatch(r"[a-z0-9_-]+", value) else ""

parts = ["doctor"]
primary = safe(report.get("primary_classification"))
phase = safe(report.get("failure_phase"))
if primary:
    parts.append(primary)
if phase:
    parts.append(phase)
for probe in report.get("probes") or []:
    if probe.get("status") != "fail":
        continue
    probe_id = safe(probe.get("id"))
    classification = safe(probe.get("classification"))
    failure_phase = safe(probe.get("failure_phase"))
    if probe_id:
        parts.append(probe_id)
    if classification:
        parts.append(classification)
    if failure_phase:
        parts.append(failure_phase)
    break
if len(parts) == 1:
    parts.append(safe(report.get("status")) or "no-failure-detail")
print(".".join(parts))
PY
)"
printf '%s.%s\n' "$status_token" "$doctor_token"
EOF
)"
  token="$(printf '%s' "${token}" | tr '[:upper:]' '[:lower:]' | tr -cs 'a-z0-9_.-' '-' | sed 's/^-*//; s/-*$//')"
  printf '%s\n' "${token:-unknown}"
}

install_tun_authorization() {
  cat >"${PRIVATE_ROOT}/polkit.rules" <<'EOF'
polkit.addRule(function(action, subject) {
    if (subject.user == "e2e" &&
        (action.id == "io.github.aidarkhusainov.podlaz.connect-tun" ||
         action.id == "io.github.aidarkhusainov.podlaz.disconnect")) {
        return polkit.Result.YES;
    }
});
EOF
  hosted_vm_scp_to "${PRIVATE_ROOT}/polkit.rules" /tmp/podlaz-hosted-vm-polkit.rules
  hosted_vm_ssh sudo install -D -m 0644 /tmp/podlaz-hosted-vm-polkit.rules /etc/polkit-1/rules.d/49-podlaz-hosted-vm.rules
  hosted_vm_ssh sudo systemctl restart polkit.service
}

run_podlaz() {
  if [[ "${HOSTED_VM_GA_READY}" == true ]]; then
    hosted_vm_ga_exec /usr/sbin/runuser -u e2e -- env \
      XDG_CONFIG_HOME=/home/e2e/.config \
      XDG_STATE_HOME=/home/e2e/.local/state \
      XDG_CACHE_HOME=/home/e2e/.cache \
      /usr/bin/podlaz "$@"
  else
    hosted_vm_ssh env \
      XDG_CONFIG_HOME=/home/e2e/.config \
      XDG_STATE_HOME=/home/e2e/.local/state \
      XDG_CACHE_HOME=/home/e2e/.cache \
      /usr/bin/podlaz "$@"
  fi
}

prepare_profile() {
  hosted_vm_ga_bash_stdin <<'EOF'
set -Eeuo pipefail
install -d -o e2e -g e2e -m 0700 /home/e2e/.config /home/e2e/.local/state /home/e2e/.cache /tmp/podlaz-vm-private
uri="$(cat /tmp/client-uri)"
runuser -u e2e -- env XDG_CONFIG_HOME=/home/e2e/.config XDG_STATE_HOME=/home/e2e/.local/state XDG_CACHE_HOME=/home/e2e/.cache \
  /usr/bin/podlaz profile import "$uri" >/tmp/podlaz-vm-private/import.stdout 2>/tmp/podlaz-vm-private/import.stderr
awk '/^Imported profile:/ {print $3; exit}' /tmp/podlaz-vm-private/import.stdout >/tmp/podlaz-vm-private/profile-id
test -s /tmp/podlaz-vm-private/profile-id
profile="$(cat /tmp/podlaz-vm-private/profile-id)"
runuser -u e2e -- env XDG_CONFIG_HOME=/home/e2e/.config XDG_STATE_HOME=/home/e2e/.local/state XDG_CACHE_HOME=/home/e2e/.cache \
  /usr/bin/podlaz profile validate "$profile" --mode tun >/tmp/podlaz-vm-private/validate.stdout 2>/tmp/podlaz-vm-private/validate.stderr
EOF
}

capture_direct_probe_baseline() {
  hosted_vm_ga_bash_stdin <<'EOF'
set -Eeuo pipefail
uplink="$(ip -4 route show default | awk 'NR == 1 {for (i=1; i<=NF; i++) if ($i == "dev") {print $(i+1); exit}}')"
test -n "$uplink"
probe="$(getent ahostsv4 example.com | awk 'NR == 1 {print $1}')"
test -n "$probe"
timeout 20 curl -4 -fsS --interface "$uplink" --connect-timeout 5 --max-time 15 \
  --resolve "example.com:443:$probe" https://example.com/ >/dev/null
printf '%s\n' "$uplink" >/tmp/podlaz-vm-private/uplink
printf '%s\n' "$probe" >/tmp/podlaz-vm-private/probe-ip
EOF
}

create_foreign_state() {
  hosted_vm_ga_bash_stdin <<'EOF'
set -Eeuo pipefail
! nft list table inet pzvm_foreign >/dev/null 2>&1
! ip link show dev pzvmforeign0 >/dev/null 2>&1
nft add table inet pzvm_foreign
ip link add dev pzvmforeign0 type dummy
ip address add 192.0.2.1/32 dev pzvmforeign0
ip link set dev pzvmforeign0 up
EOF
}

assert_foreign_state() {
  hosted_vm_ga_bash_stdin <<'EOF'
set -Eeuo pipefail
nft list table inet pzvm_foreign >/dev/null
ip -4 address show dev pzvmforeign0 | grep -F '192.0.2.1/32' >/dev/null
EOF
}

remove_foreign_state() {
  if [[ "${HOSTED_VM_GA_READY}" == true ]]; then
    hosted_vm_ga_bash 'nft delete table inet pzvm_foreign >/dev/null 2>&1 || true; ip link del dev pzvmforeign0 >/dev/null 2>&1 || true'
  else
    hosted_vm_ssh sudo nft delete table inet pzvm_foreign >/dev/null 2>&1 || true
    hosted_vm_ssh sudo ip link del dev pzvmforeign0 >/dev/null 2>&1 || true
  fi
}

wait_status() {
  local target="$1" attempts="$2" command
  command="curl --fail --silent --show-error --max-time 5 --unix-socket /run/podlaz/podlazd.sock http://localhost/v1/status >/tmp/podlaz-vm-private/status.json 2>/dev/null && python3 /tmp/daemon_status_semantics.py '${target}' /tmp/podlaz-vm-private/status.json"
  for _ in $(seq 1 "${attempts}"); do
    if hosted_vm_ga_bash "${command}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

connect_tun() {
  local rc diagnosis
  set +e
  hosted_vm_ga_bash_stdin <<'EOF'
set -Eeuo pipefail
profile="$(cat /tmp/podlaz-vm-private/profile-id)"
runuser -u e2e -- env XDG_CONFIG_HOME=/home/e2e/.config XDG_STATE_HOME=/home/e2e/.local/state XDG_CACHE_HOME=/home/e2e/.cache \
  /usr/bin/podlaz connect --mode tun "$profile" >/tmp/podlaz-vm-private/connect.stdout 2>/tmp/podlaz-vm-private/connect.stderr
EOF
  rc=$?
  set -e
  if (( rc != 0 )); then
    diagnosis="$(diagnose_connect_failure)"
    if [[ "${diagnosis}" == authorization ]]; then
      mark_failure fixture tun.authorization
    else
      mark_failure product "tun.connect.request.${diagnosis}"
    fi
    return "${rc}"
  fi
  if ! wait_status verified-active 150; then
    diagnosis="$(diagnose_connect_failure)"
    mark_failure product "tun.connect.convergence.${diagnosis}"
    return 1
  fi
}

capture_exact_active_authority() {
  local snapshot="$1" script
  script="$(cat <<'EOF'
set -Eeuo pipefail
curl --fail --silent --show-error --max-time 5 --unix-socket /run/podlaz/podlazd.sock \
  http://localhost/v1/status >/tmp/podlaz-vm-private/status.json
resolvectl dns >/tmp/podlaz-vm-private/resolved-dns.txt
resolvectl domain >/tmp/podlaz-vm-private/resolved-domain.txt
resolvectl default-route >/tmp/podlaz-vm-private/resolved-default-route.txt
nft -j list ruleset >/tmp/podlaz-vm-private/nft-ruleset.json
python3 /tmp/active_authority.py \
  --status /tmp/podlaz-vm-private/status.json \
  --transactions /run/podlaz/transactions \
  --session /run/podlaz/network-session-continuation.json \
  --boot-id /proc/sys/kernel/random/boot_id \
  --runtime-config /run/podlaz/generated/xray.json \
  --resolved-dns /tmp/podlaz-vm-private/resolved-dns.txt \
  --resolved-domain /tmp/podlaz-vm-private/resolved-domain.txt \
  --resolved-default-route /tmp/podlaz-vm-private/resolved-default-route.txt \
  --nft-ruleset /tmp/podlaz-vm-private/nft-ruleset.json
if [[ "$snapshot" == yes ]]; then
  python3 /tmp/network_authority.py snapshot /run/podlaz/transactions /tmp/podlaz-vm-private/network-manifest.json
fi
python3 /tmp/network_authority.py verify-present /tmp/podlaz-vm-private/network-manifest.json
EOF
)"
  hosted_vm_ga_bash "snapshot=${snapshot@Q}; ${script}"
}

assert_direct_uplink_blocked() {
  if hosted_vm_ga_bash_stdin <<'EOF'
set -Eeuo pipefail
uplink="$(cat /tmp/podlaz-vm-private/uplink)"
probe="$(cat /tmp/podlaz-vm-private/probe-ip)"
timeout 7 curl -4 -fsSk --interface "$uplink" --connect-timeout 3 --max-time 5 \
  --resolve "example.com:443:$probe" https://example.com/ >/dev/null 2>&1
EOF
  then
    return 1
  fi
  return 0
}

run_tun_traffic() {
  hosted_vm_ga_bash 'timeout 20 getent ahostsv4 example.com >/dev/null && timeout 30 curl -4 -fsS -o /dev/null https://example.com/'
}

session_id() {
  hosted_vm_ga_bash "jq -r '.session_id' /run/podlaz/network-session-continuation.json" | tr -d '[:space:]'
}

assert_armed_current_boot_session() {
  local boot
  boot="$(hosted_vm_boot_id)"
  hosted_vm_ga_bash "jq -e --arg boot ${boot@Q} '.schema_version==\"podlaz.network-session-state.v1\" and .owner==\"podlaz\" and .boot_id==\$boot and .intent==\"resume\" and .protection.state==\"armed\"' /run/podlaz/network-session-continuation.json >/dev/null"
}

disconnect_tun() {
  run_podlaz disconnect >/dev/null
  wait_status clean-inactive 100
}

assert_exact_terminal_cleanup() {
  hosted_vm_ga_bash_stdin <<'EOF'
set -Eeuo pipefail
python3 /tmp/network_authority.py verify-absent /tmp/podlaz-vm-private/network-manifest.json
test ! -e /run/podlaz/network-session-continuation.json
test ! -e /run/podlaz/generated/xray.json
if test -d /run/podlaz/transactions; then
  test -z "$(find /run/podlaz/transactions -mindepth 1 -maxdepth 1 -type f -print -quit)"
fi
! ip link show dev podlaz0 >/dev/null 2>&1
! nft list tables 2>/dev/null | grep -E 'table inet podlaz_pe_[0-9a-f]+' >/dev/null
EOF
}

run_clean_recovery() {
  hosted_vm_ga_bash_stdin <<'EOF'
set -Eeuo pipefail
runuser -u e2e -- env XDG_CONFIG_HOME=/home/e2e/.config XDG_STATE_HOME=/home/e2e/.local/state XDG_CACHE_HOME=/home/e2e/.cache \
  /usr/bin/podlaz recover --json >/tmp/podlaz-vm-private/recover.json 2>/tmp/podlaz-vm-private/recover.stderr
source /tmp/recovery_json.sh
assert_clean_recovery_json_file /tmp/podlaz-vm-private/recover.json
EOF
}

cleanup() {
  local saved=$? cleanup_failed=0
  trap - EXIT
  set +e
  remove_foreign_state || cleanup_failed=1
  hosted_vm_remove_polkit_rule || cleanup_failed=1
  hosted_vm_stop || cleanup_failed=1
  stop_synthetic_endpoint || cleanup_failed=1
  if (( cleanup_failed != 0 )) && (( saved == 0 )); then
    mark_failure infrastructure fixture.cleanup
    saved=1
  fi
  finalize_report
  set -e
  exit "${saved}"
}

run_scenario() {
  local boot_before boot_after session_before session_after

  mark_failure capability vm.acceleration
  hosted_vm_probe_acceleration
  record_evidence vm.acceleration pass

  mark_failure fixture synthetic.endpoint
  start_synthetic_endpoint
  record_evidence fixture.synthetic_endpoint pass

  mark_failure infrastructure vm.image
  hosted_vm_prepare_image
  record_evidence vm.image_checksum pass

  mark_failure infrastructure vm.boot
  hosted_vm_start
  hosted_vm_wait_ssh 180
  hosted_vm_wait_cloud_init
  # Expansion is intentionally evaluated by the guest shell.
  # shellcheck disable=SC2016
  hosted_vm_ssh '. /etc/os-release; test "$ID" = ubuntu; test "$VERSION_ID" = 24.04'
  record_evidence vm.boot pass

  mark_failure capability vm.suspend
  hosted_vm_require_suspend_wakeup
  record_evidence vm.suspend_wakeup_capability pass

  mark_failure product candidate.install
  hosted_vm_install_candidate "${CANDIDATE_DEB}"
  hosted_vm_assert_candidate_provenance "${CANDIDATE_DEB}" "${EXPECTED_COMMIT}"

  mark_failure fixture guest.control
  hosted_vm_ssh sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl jq
  hosted_vm_prepare_guest_agent
  hosted_vm_provider_prepare_guest

  mark_failure product candidate.provenance
  hosted_vm_assert_candidate_provenance "${CANDIDATE_DEB}" "${EXPECTED_COMMIT}"
  record_evidence candidate.provenance pass

  mark_failure fixture guest.helpers
  install_guest_helpers
  install_tun_authorization
  prepare_profile
  capture_direct_probe_baseline

  mark_failure fixture synthetic.endpoint_guest
  assert_synthetic_endpoint_guest_reachable
  record_evidence fixture.synthetic_endpoint_guest pass

  create_foreign_state
  assert_foreign_state
  record_evidence fixture.foreign_state pass

  mark_failure product tun.connect
  connect_tun
  record_evidence tun.verified_active_before_suspend pass

  mark_failure product tun.authority_before_suspend
  capture_exact_active_authority yes
  record_evidence tun.exact_authority_before_suspend pass

  mark_failure product privacy.before_suspend
  assert_direct_uplink_blocked
  record_evidence privacy.direct_uplink_blocked_before_suspend pass

  run_tun_traffic
  record_evidence tun.traffic_before_suspend pass

  boot_before="$(hosted_vm_boot_id)"
  session_before="$(session_id)"
  [[ -n "${session_before}" ]]

  mark_failure infrastructure vm.suspend_runtime
  hosted_vm_suspend_guest
  record_evidence suspend.actual_guest_boundary pass

  boot_after="$(hosted_vm_boot_id)"
  [[ "${boot_after}" == "${boot_before}" ]]
  record_evidence suspend.same_boot pass

  mark_failure product privacy.after_wakeup
  assert_armed_current_boot_session
  assert_direct_uplink_blocked
  record_evidence privacy.direct_uplink_blocked_after_wakeup pass

  mark_failure product tun.convergence_after_wakeup
  wait_status verified-active 150
  record_evidence tun.verified_active_after_wakeup pass

  session_after="$(session_id)"
  [[ "${session_after}" == "${session_before}" ]]
  record_evidence tun.same_network_session pass

  capture_exact_active_authority no
  record_evidence tun.exact_authority_after_wakeup pass

  assert_foreign_state
  record_evidence fixture.foreign_state_after_wakeup pass

  run_tun_traffic
  record_evidence tun.traffic_after_wakeup pass

  mark_failure product tun.disconnect
  disconnect_tun
  record_evidence tun.clean_disconnect pass

  mark_failure product tun.terminal_cleanup
  assert_exact_terminal_cleanup
  record_evidence tun.exact_terminal_cleanup pass

  run_clean_recovery
  record_evidence tun.recovery_clean pass

  assert_foreign_state
  record_evidence fixture.foreign_state_terminal pass

  mark_failure infrastructure guest.connectivity
  capture_direct_probe_baseline
  record_evidence guest.ordinary_connectivity_restored pass

  mark_failure diagnostic_unknown artifact.privacy
  assert_public_artifact_privacy
  record_evidence artifact.privacy pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
}

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  require_cmd awk bash curl dpkg-deb find grep install jq mktemp python3 readlink sed seq sha256sum sleep ss tr
  validate_candidate "$1"
  install -d -m 0700 "${PRIVATE_ROOT}" "${E2E_ARTIFACT_DIR}" "${XRAY_ROOT}"
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
