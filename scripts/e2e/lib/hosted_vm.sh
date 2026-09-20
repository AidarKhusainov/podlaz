#!/usr/bin/env bash

# Shared QEMU mechanics for hosted full-VM qualification. Scenario-specific
# lifecycle assertions and product cleanup remain in the caller.
# Callers must source lib/e2e.sh first and run with set -Eeuo pipefail.

HOSTED_VM_IMAGE_URL="https://cloud-images.ubuntu.com/releases/noble/release/ubuntu-24.04-server-cloudimg-amd64.img"
HOSTED_VM_SUMS_URL="https://cloud-images.ubuntu.com/releases/noble/release/SHA256SUMS"

HOSTED_VM_ROOT=""
HOSTED_VM_IMAGE=""
HOSTED_VM_OVERLAY=""
HOSTED_VM_SEED=""
HOSTED_VM_KEY=""
HOSTED_VM_PID=""
HOSTED_VM_SSH_PORT=""
HOSTED_VM_ACCEL=""
HOSTED_VM_QMP=""
HOSTED_VM_QGA=""
HOSTED_VM_GA_READY=false
HOSTED_VM_USER_NET_EXTRA=""

hosted_vm_init() {
  local root="$1"
  [[ -n "${root}" ]] || fail "hosted VM root is empty"
  HOSTED_VM_ROOT="${root}"
  HOSTED_VM_IMAGE="${HOSTED_VM_ROOT}/ubuntu.img"
  HOSTED_VM_OVERLAY="${HOSTED_VM_ROOT}/overlay.qcow2"
  HOSTED_VM_SEED="${HOSTED_VM_ROOT}/seed.img"
  HOSTED_VM_KEY="${HOSTED_VM_ROOT}/id_ed25519"
  local socket_tag
  socket_tag="$(printf '%s' "${HOSTED_VM_ROOT}" | sha256sum | awk '{print substr($1,1,16)}')"
  HOSTED_VM_QMP="/tmp/pzvm-${socket_tag}.qmp"
  HOSTED_VM_QGA="/tmp/pzvm-${socket_tag}.qga"
  HOSTED_VM_GA_READY=false
  rm -f -- "${HOSTED_VM_QMP}" "${HOSTED_VM_QGA}"
  install -d -m 0700 "${HOSTED_VM_ROOT}"
}

hosted_vm_probe_acceleration() {
  require_cmd cloud-localds curl qemu-img qemu-system-x86_64 scp sha256sum ssh ssh-keygen
  local pidfile="${HOSTED_VM_ROOT}/accel-probe.pid"

  HOSTED_VM_ACCEL=tcg
  if [[ -e /dev/kvm ]] && qemu-system-x86_64       -accel kvm -machine none -nodefaults -display none -monitor none -serial none       -S -daemonize -pidfile "${pidfile}" >/dev/null 2>&1; then
    HOSTED_VM_ACCEL=kvm
    kill "$(cat "${pidfile}")" >/dev/null 2>&1 || true
    rm -f -- "${pidfile}"
  fi

  if [[ "${HOSTED_VM_ACCEL}" == tcg ]]; then
    if ! qemu-system-x86_64         -accel tcg -machine none -nodefaults -display none -monitor none -serial none         -S -daemonize -pidfile "${pidfile}" >/dev/null 2>&1; then
      return 1
    fi
    kill "$(cat "${pidfile}")" >/dev/null 2>&1 || true
    rm -f -- "${pidfile}"
  fi
}

hosted_vm_prepare_image() {
  local free_kb sums expected actual user_data
  free_kb="$(df -Pk "${HOSTED_VM_ROOT}" | awk 'NR == 2 {print $4}')"
  (( free_kb >= 3 * 1024 * 1024 )) || fail "hosted VM requires at least 3 GiB free"

  sums="${HOSTED_VM_ROOT}/SHA256SUMS"
  curl -fsSL "${HOSTED_VM_IMAGE_URL}" -o "${HOSTED_VM_IMAGE}"
  curl -fsSL "${HOSTED_VM_SUMS_URL}" -o "${sums}"
  expected="$(awk '$2 == "*ubuntu-24.04-server-cloudimg-amd64.img" || $2 == "ubuntu-24.04-server-cloudimg-amd64.img" {print $1; exit}' "${sums}")"
  actual="$(sha256sum "${HOSTED_VM_IMAGE}" | awk '{print $1}')"
  [[ -n "${expected}" && "${actual}" == "${expected}" ]] || fail "Ubuntu cloud image checksum mismatch"

  qemu-img create -q -f qcow2 -F qcow2 -b "${HOSTED_VM_IMAGE}" "${HOSTED_VM_OVERLAY}" 6G
  ssh-keygen -q -t ed25519 -N '' -f "${HOSTED_VM_KEY}"
  chmod 0600 "${HOSTED_VM_KEY}"

  user_data="${HOSTED_VM_ROOT}/user-data"
  cat >"${user_data}" <<EOF
#cloud-config
users:
  - name: e2e
    groups: [sudo]
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    ssh_authorized_keys:
      - $(cat "${HOSTED_VM_KEY}.pub")
ssh_pwauth: false
disable_root: true
EOF
  printf 'instance-id: podlaz-hosted-vm\nlocal-hostname: podlaz-hosted-vm\n' >"${HOSTED_VM_ROOT}/meta-data"
  cloud-localds "${HOSTED_VM_SEED}" "${user_data}" "${HOSTED_VM_ROOT}/meta-data"
}

hosted_vm_find_loopback_port() {
  python3 - <<'PY'
import socket
sock = socket.socket()
sock.bind(("127.0.0.1", 0))
print(sock.getsockname()[1])
sock.close()
PY
}

hosted_vm_start() {
  local cpu pidfile="${HOSTED_VM_ROOT}/qemu.pid"
  [[ "${HOSTED_VM_ACCEL}" == kvm || "${HOSTED_VM_ACCEL}" == tcg ]] || fail "hosted VM acceleration was not selected"
  if [[ "${HOSTED_VM_ACCEL}" == kvm ]]; then cpu=host; else cpu=max; fi
  HOSTED_VM_SSH_PORT="$(hosted_vm_find_loopback_port)"

  qemu-system-x86_64     -machine "q35,accel=${HOSTED_VM_ACCEL}"     -cpu "${cpu}"     -smp 2     -m 2048     -drive "if=virtio,file=${HOSTED_VM_OVERLAY},format=qcow2"     -drive "if=virtio,file=${HOSTED_VM_SEED},format=raw,readonly=on"     -nic "user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:${HOSTED_VM_SSH_PORT}-:22${HOSTED_VM_USER_NET_EXTRA}"     -qmp "unix:${HOSTED_VM_QMP},server=on,wait=off"     -chardev "socket,path=${HOSTED_VM_QGA},server=on,wait=off,id=qga0"     -device virtio-serial-pci     -device "virtserialport,chardev=qga0,name=org.qemu.guest_agent.0"     -display none     -monitor none     -serial "file:${HOSTED_VM_ROOT}/serial.log"     -daemonize     -pidfile "${pidfile}"
  HOSTED_VM_PID="$(cat "${pidfile}")"
}

hosted_vm_ssh() {
  ssh -i "${HOSTED_VM_KEY}" -p "${HOSTED_VM_SSH_PORT}"     -o BatchMode=yes -o ConnectTimeout=5     -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR     e2e@127.0.0.1 "$@"
}

hosted_vm_scp_to() {
  local source="$1" destination="$2"
  scp -q -i "${HOSTED_VM_KEY}" -P "${HOSTED_VM_SSH_PORT}"     -o BatchMode=yes -o ConnectTimeout=5     -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR     -- "${source}" "e2e@127.0.0.1:${destination}"
}

hosted_vm_wait_ssh() {
  local attempts="${1:-180}"
  for _ in $(seq 1 "${attempts}"); do
    if hosted_vm_ssh true >/dev/null 2>&1; then
      return 0
    fi
    [[ -n "${HOSTED_VM_PID}" ]] && kill -0 "${HOSTED_VM_PID}" >/dev/null 2>&1 || return 1
    sleep 2
  done
  return 1
}

hosted_vm_wait_cloud_init() {
  hosted_vm_ssh sudo cloud-init status --wait >/dev/null
}

hosted_vm_boot_id() {
  if [[ "${HOSTED_VM_GA_READY}" == true ]]; then
    hosted_vm_ga_exec /bin/cat /proc/sys/kernel/random/boot_id | tr -d '[:space:]'
  else
    hosted_vm_ssh cat /proc/sys/kernel/random/boot_id | tr -d '[:space:]'
  fi
}

hosted_vm_reboot() {
  local before after=""
  before="$(hosted_vm_boot_id)"
  [[ -n "${before}" ]] || return 1

  if [[ "${HOSTED_VM_GA_READY}" == true ]]; then
    hosted_vm_ga_async guest-shutdown '{"mode":"reboot"}' || return 1
  else
    hosted_vm_ssh sudo systemctl reboot >/dev/null 2>&1 || true
  fi

  HOSTED_VM_GA_READY=false
  for _ in $(seq 1 240); do
    if hosted_vm_ssh true >/dev/null 2>&1; then
      after="$(hosted_vm_ssh cat /proc/sys/kernel/random/boot_id 2>/dev/null | tr -d '[:space:]' || true)"
      if [[ -n "${after}" && "${after}" != "${before}" ]]; then
        break
      fi
    fi
    sleep 2
  done
  [[ -n "${after}" && "${after}" != "${before}" ]] || return 1
  if [[ -S "${HOSTED_VM_QGA}" ]]; then
    hosted_vm_wait_ga 120 || return 1
  fi
  printf '%s\t%s\n' "${before}" "${after}"
}

hosted_vm_install_candidate() {
  local candidate="$1"
  hosted_vm_scp_to "${candidate}" /tmp/podlaz-candidate.deb
  hosted_vm_ssh sudo env DEBIAN_FRONTEND=noninteractive apt-get update -qq
  hosted_vm_ssh sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq /tmp/podlaz-candidate.deb
  hosted_vm_ssh sudo systemctl daemon-reload
  hosted_vm_ssh sudo systemctl reset-failed podlazd.service >/dev/null 2>&1 || true
  hosted_vm_ssh sudo systemctl start podlazd.service
  hosted_vm_ssh sudo systemctl is-active --quiet podlazd.service
}

hosted_vm_assert_candidate_provenance() {
  local candidate="$1" expected_commit="$2"
  local expected_version extract expected_cli expected_daemon expected_xray script
  [[ -n "${expected_commit}" ]] || fail "expected candidate commit is empty"

  expected_version="$(dpkg-deb --field "${candidate}" Version)"
  extract="$(mktemp -d "${HOSTED_VM_ROOT}/candidate.XXXXXX")"
  dpkg-deb -x "${candidate}" "${extract}"
  expected_cli="$(sha256sum "${extract}/usr/bin/podlaz" | awk '{print $1}')"
  expected_daemon="$(sha256sum "${extract}/usr/bin/podlazd" | awk '{print $1}')"
  expected_xray="$(sha256sum "${extract}/usr/lib/podlaz/xray" | awk '{print $1}')"
  rm -rf -- "${extract}"

  if [[ "${HOSTED_VM_GA_READY}" != true ]]; then
    hosted_vm_ssh bash -s -- "${expected_version}" "${expected_commit}" "${expected_cli}" "${expected_daemon}" "${expected_xray}" <<'EOF'
set -Eeuo pipefail
expected_version="$1"
expected_commit="$2"
expected_cli="$3"
expected_daemon="$4"
expected_xray="$5"
[[ "$(dpkg-query -W -f='${db:Status-Status}' podlaz)" == installed ]]
[[ "$(dpkg-query -W -f='${Version}' podlaz)" == "$expected_version" ]]
[[ "$(sha256sum /usr/bin/podlaz | awk '{print $1}')" == "$expected_cli" ]]
[[ "$(sha256sum /usr/bin/podlazd | awk '{print $1}')" == "$expected_daemon" ]]
[[ "$(sha256sum /usr/lib/podlaz/xray | awk '{print $1}')" == "$expected_xray" ]]
/usr/bin/podlaz version | grep -Fx "commit: $expected_commit" >/dev/null
systemctl is-active --quiet podlazd.service
pid="$(systemctl show -p MainPID --value podlazd.service)"
[[ "$pid" =~ ^[1-9][0-9]*$ ]]
[[ "$(sudo readlink -f "/proc/$pid/exe")" == /usr/bin/podlazd ]]
[[ "$(sudo sha256sum "/proc/$pid/exe" | awk '{print $1}')" == "$expected_daemon" ]]
[[ "$(sudo stat -Lc '%d:%i' "/proc/$pid/exe")" == "$(stat -Lc '%d:%i' /usr/bin/podlazd)" ]]
EOF
    return
  fi

  script="$(cat <<'EOF'
set -Eeuo pipefail
[[ "$(dpkg-query -W -f='${db:Status-Status}' podlaz)" == installed ]]
[[ "$(dpkg-query -W -f='${Version}' podlaz)" == "$expected_version" ]]
[[ "$(sha256sum /usr/bin/podlaz | awk '{print $1}')" == "$expected_cli" ]]
[[ "$(sha256sum /usr/bin/podlazd | awk '{print $1}')" == "$expected_daemon" ]]
[[ "$(sha256sum /usr/lib/podlaz/xray | awk '{print $1}')" == "$expected_xray" ]]
/usr/bin/podlaz version | grep -Fx "commit: $expected_commit" >/dev/null
systemctl is-active --quiet podlazd.service
pid="$(systemctl show -p MainPID --value podlazd.service)"
[[ "$pid" =~ ^[1-9][0-9]*$ ]]
[[ "$(readlink -f "/proc/$pid/exe")" == /usr/bin/podlazd ]]
[[ "$(sha256sum "/proc/$pid/exe" | awk '{print $1}')" == "$expected_daemon" ]]
[[ "$(stat -Lc '%d:%i' "/proc/$pid/exe")" == "$(stat -Lc '%d:%i' /usr/bin/podlazd)" ]]
EOF
)"
  hosted_vm_ga_bash "expected_version=${expected_version@Q}; expected_commit=${expected_commit@Q}; expected_cli=${expected_cli@Q}; expected_daemon=${expected_daemon@Q}; expected_xray=${expected_xray@Q}; ${script}"
}

hosted_vm_install_polkit_rule() {
  local action="$1" path="/tmp/podlaz-hosted-vm-polkit.rules"
  [[ "${action}" =~ ^io[.]github[.]aidarkhusainov[.]podlaz[.][a-z-]+$ ]] || fail "invalid hosted VM polkit action"
  cat >"${HOSTED_VM_ROOT}/polkit.rules" <<EOF
polkit.addRule(function(action, subject) {
    if (subject.user == "e2e" && action.id == "${action}") {
        return polkit.Result.YES;
    }
});
EOF
  hosted_vm_scp_to "${HOSTED_VM_ROOT}/polkit.rules" "${path}"
  hosted_vm_ssh sudo install -D -m 0644 "${path}" /etc/polkit-1/rules.d/49-podlaz-hosted-vm.rules
  hosted_vm_ssh sudo systemctl restart polkit.service
}

hosted_vm_remove_polkit_rule() {
  if [[ "${HOSTED_VM_GA_READY}" == true ]]; then
    hosted_vm_ga_exec /bin/rm -f /etc/polkit-1/rules.d/49-podlaz-hosted-vm.rules >/dev/null 2>&1 || true
  else
    hosted_vm_ssh sudo rm -f /etc/polkit-1/rules.d/49-podlaz-hosted-vm.rules >/dev/null 2>&1 || true
  fi
}


hosted_vm_ga_exec() {
  local path="$1"
  shift
  python3 - "${HOSTED_VM_QGA}" "${path}" "$@" <<'PY'
import base64
import json
import os
import secrets
import socket
import sys
import time

socket_path = sys.argv[1]
path = sys.argv[2]
args = sys.argv[3:]

sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
sock.settimeout(10)
sock.connect(socket_path)
stream = sock.makefile("rwb", buffering=0)

def send(payload, *, delimited=False):
    raw = json.dumps(payload, separators=(",", ":")).encode("utf-8") + b"\n"
    if delimited:
        raw = b"\xff" + raw
    stream.write(raw)

def receive():
    while True:
        line = stream.readline()
        if not line:
            raise RuntimeError("QGA connection closed")
        line = line.lstrip(b"\xff")
        if not line.strip():
            continue
        try:
            message = json.loads(line)
        except json.JSONDecodeError:
            continue
        if "return" in message or "error" in message:
            return message

sync_id = secrets.randbits(63)
send({"execute": "guest-sync-delimited", "arguments": {"id": sync_id}}, delimited=True)
while True:
    synced = receive()
    if synced.get("return") == sync_id:
        break

send({
    "execute": "guest-exec",
    "arguments": {
        "path": path,
        "arg": args,
        "capture-output": True,
    },
})
started = receive()
if "error" in started:
    print(started["error"].get("desc", "guest-exec failed"), file=sys.stderr)
    raise SystemExit(125)
pid = (started.get("return") or {}).get("pid")
if not isinstance(pid, int) or pid <= 0:
    raise SystemExit("QGA guest-exec returned invalid pid")

deadline = time.monotonic() + 180
while True:
    if time.monotonic() >= deadline:
        raise SystemExit("QGA guest-exec timed out")
    send({"execute": "guest-exec-status", "arguments": {"pid": pid}})
    status_message = receive()
    if "error" in status_message:
        print(status_message["error"].get("desc", "guest-exec-status failed"), file=sys.stderr)
        raise SystemExit(125)
    status = status_message.get("return") or {}
    if not status.get("exited"):
        time.sleep(0.1)
        continue
    if status.get("out-truncated") or status.get("err-truncated"):
        raise SystemExit("QGA guest-exec output was truncated")
    stdout = base64.b64decode(status.get("out-data") or "")
    stderr = base64.b64decode(status.get("err-data") or "")
    sys.stdout.buffer.write(stdout)
    sys.stderr.buffer.write(stderr)
    if "signal" in status:
        raise SystemExit(125)
    code = status.get("exitcode")
    raise SystemExit(code if isinstance(code, int) else 125)
PY
}

hosted_vm_ga_bash() {
  local script="$1"
  hosted_vm_ga_exec /bin/bash -lc "${script}"
}

hosted_vm_ga_bash_stdin() {
  local script
  script="$(cat)"
  hosted_vm_ga_bash "${script}"
}

hosted_vm_ga_ping() {
  hosted_vm_ga_exec /bin/true >/dev/null 2>&1
}

hosted_vm_wait_ga() {
  local attempts="${1:-120}"
  for _ in $(seq 1 "${attempts}"); do
    if hosted_vm_ga_ping; then
      HOSTED_VM_GA_READY=true
      return 0
    fi
    [[ -n "${HOSTED_VM_PID}" ]] && kill -0 "${HOSTED_VM_PID}" >/dev/null 2>&1 || return 1
    sleep 1
  done
  return 1
}

hosted_vm_prepare_guest_agent() {
  hosted_vm_ssh sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq qemu-guest-agent
  hosted_vm_ssh sudo systemctl enable qemu-guest-agent.service >/dev/null 2>&1 || true
  hosted_vm_ssh sudo systemctl restart qemu-guest-agent.service
  hosted_vm_wait_ga 120
}

hosted_vm_control_bash() {
  local script="$1"
  if [[ "${HOSTED_VM_GA_READY}" == true ]]; then
    hosted_vm_ga_bash "${script}"
  else
    hosted_vm_ssh bash -lc "${script}"
  fi
}

hosted_vm_ga_async() {
  local command="$1" arguments_json="${2:-{}}"
  python3 - "${HOSTED_VM_QGA}" "${command}" "${arguments_json}" <<'PY'
import json
import secrets
import socket
import sys

socket_path, command, arguments_raw = sys.argv[1:4]
arguments = json.loads(arguments_raw)
sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
sock.settimeout(10)
sock.connect(socket_path)
stream = sock.makefile("rwb", buffering=0)

def send(payload, delimited=False):
    raw = json.dumps(payload, separators=(",", ":")).encode() + b"\n"
    if delimited:
        raw = b"\xff" + raw
    stream.write(raw)

def receive():
    while True:
        line = stream.readline()
        if not line:
            raise SystemExit("QGA connection closed")
        line = line.lstrip(b"\xff")
        if not line.strip():
            continue
        try:
            message = json.loads(line)
        except json.JSONDecodeError:
            continue
        if "return" in message or "error" in message:
            return message

sync_id = secrets.randbits(63)
send({"execute": "guest-sync-delimited", "arguments": {"id": sync_id}}, True)
while True:
    message = receive()
    if message.get("return") == sync_id:
        break
send({"execute": command, "arguments": arguments})
PY
}


hosted_vm_qmp() {
  local command="$1"
  python3 - "${HOSTED_VM_QMP}" "${command}" <<'PY'
import json
import socket
import sys

path, command = sys.argv[1], sys.argv[2]
sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
sock.settimeout(10)
sock.connect(path)
stream = sock.makefile("rwb", buffering=0)

def recv_response():
    while True:
        line = stream.readline()
        if not line:
            raise SystemExit("QMP connection closed")
        message = json.loads(line)
        if "return" in message or "error" in message:
            return message

greeting = json.loads(stream.readline())
if "QMP" not in greeting:
    raise SystemExit("QMP greeting missing")
stream.write(json.dumps({"execute": "qmp_capabilities"}).encode() + b"\r\n")
cap = recv_response()
if "error" in cap:
    raise SystemExit("QMP capabilities negotiation failed")
stream.write(json.dumps({"execute": command}).encode() + b"\r\n")
response = recv_response()
print(json.dumps(response, separators=(",", ":"), sort_keys=True))
if "error" in response:
    raise SystemExit(1)
PY
}

hosted_vm_wait_qmp_status() {
  local want="$1" attempts="${2:-120}" response
  for _ in $(seq 1 "${attempts}"); do
    if response="$(hosted_vm_qmp query-status 2>/dev/null)" &&
        python3 - "${want}" "${response}" <<'PY'
import json, sys
want, raw = sys.argv[1], sys.argv[2]
payload = json.loads(raw)
raise SystemExit(0 if (payload.get("return") or {}).get("status") == want else 1)
PY
    then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

hosted_vm_require_suspend_wakeup() {
  local response
  response="$(hosted_vm_qmp query-current-machine)"
  python3 - "${response}" <<'PY'
import json, sys
payload = json.loads(sys.argv[1])
value = (payload.get("return") or {}).get("wakeup-suspend-support")
raise SystemExit(0 if value is True else 1)
PY
}

hosted_vm_suspend_guest() {
  local boot_before pid_before
  [[ "${HOSTED_VM_GA_READY}" == true ]] || return 1
  boot_before="$(hosted_vm_boot_id)"
  pid_before="$(hosted_vm_ga_bash 'systemctl show -p MainPID --value podlazd.service' | tr -d '[:space:]')"
  [[ -n "${boot_before}" && "${pid_before}" =~ ^[1-9][0-9]*$ ]] || return 1

  hosted_vm_ga_async guest-suspend-ram '{}' || return 1
  hosted_vm_wait_qmp_status suspended 120 || return 1

  hosted_vm_qmp system_wakeup >/dev/null || return 1
  hosted_vm_wait_qmp_status running 120 || return 1
  hosted_vm_wait_ga 180 || return 1

  [[ "$(hosted_vm_boot_id)" == "${boot_before}" ]] || return 1
  [[ "$(hosted_vm_ga_bash 'systemctl show -p MainPID --value podlazd.service' | tr -d '[:space:]')" == "${pid_before}" ]] || return 1
}

hosted_vm_stop() {
  if [[ -n "${HOSTED_VM_PID}" ]]; then
    kill "${HOSTED_VM_PID}" >/dev/null 2>&1 || true
    for _ in $(seq 1 50); do
      kill -0 "${HOSTED_VM_PID}" >/dev/null 2>&1 || break
      sleep 0.1
    done
    kill -9 "${HOSTED_VM_PID}" >/dev/null 2>&1 || true
    wait "${HOSTED_VM_PID}" >/dev/null 2>&1 || true
    HOSTED_VM_PID=""
  fi
  rm -f -- "${HOSTED_VM_QMP}" "${HOSTED_VM_QGA}"
  HOSTED_VM_GA_READY=false
}
