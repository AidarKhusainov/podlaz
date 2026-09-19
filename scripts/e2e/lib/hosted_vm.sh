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

hosted_vm_init() {
  local root="$1"
  [[ -n "${root}" ]] || fail "hosted VM root is empty"
  HOSTED_VM_ROOT="${root}"
  HOSTED_VM_IMAGE="${HOSTED_VM_ROOT}/ubuntu.img"
  HOSTED_VM_OVERLAY="${HOSTED_VM_ROOT}/overlay.qcow2"
  HOSTED_VM_SEED="${HOSTED_VM_ROOT}/seed.img"
  HOSTED_VM_KEY="${HOSTED_VM_ROOT}/id_ed25519"
  HOSTED_VM_QMP="${HOSTED_VM_ROOT}/qmp.sock"
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

  qemu-system-x86_64     -machine "q35,accel=${HOSTED_VM_ACCEL}"     -cpu "${cpu}"     -smp 2     -m 2048     -drive "if=virtio,file=${HOSTED_VM_OVERLAY},format=qcow2"     -drive "if=virtio,file=${HOSTED_VM_SEED},format=raw,readonly=on"     -nic "user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:${HOSTED_VM_SSH_PORT}-:22"     -qmp "unix:${HOSTED_VM_QMP},server=on,wait=off"     -display none     -monitor none     -serial "file:${HOSTED_VM_ROOT}/serial.log"     -daemonize     -pidfile "${pidfile}"
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
  hosted_vm_ssh cat /proc/sys/kernel/random/boot_id | tr -d '[:space:]'
}

hosted_vm_reboot() {
  local before after disappeared=false
  before="$(hosted_vm_boot_id)"
  [[ -n "${before}" ]] || return 1

  hosted_vm_ssh sudo systemctl reboot >/dev/null 2>&1 || true
  for _ in $(seq 1 60); do
    if ! hosted_vm_ssh true >/dev/null 2>&1; then
      disappeared=true
      break
    fi
    sleep 1
  done
  [[ "${disappeared}" == true ]] || return 1
  hosted_vm_wait_ssh 240 || return 1
  after="$(hosted_vm_boot_id)"
  [[ -n "${after}" && "${after}" != "${before}" ]] || return 1
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
  local expected_version extract expected_cli expected_daemon expected_xray
  [[ -n "${expected_commit}" ]] || fail "expected candidate commit is empty"

  expected_version="$(dpkg-deb --field "${candidate}" Version)"
  extract="$(mktemp -d "${HOSTED_VM_ROOT}/candidate.XXXXXX")"
  dpkg-deb -x "${candidate}" "${extract}"
  expected_cli="$(sha256sum "${extract}/usr/bin/podlaz" | awk '{print $1}')"
  expected_daemon="$(sha256sum "${extract}/usr/bin/podlazd" | awk '{print $1}')"
  expected_xray="$(sha256sum "${extract}/usr/lib/podlaz/xray" | awk '{print $1}')"
  rm -rf -- "${extract}"

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
  hosted_vm_ssh sudo rm -f /etc/polkit-1/rules.d/49-podlaz-hosted-vm.rules >/dev/null 2>&1 || true
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
}
