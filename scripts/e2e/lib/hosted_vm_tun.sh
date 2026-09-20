#!/usr/bin/env bash

# Shared synthetic full-TUN fixture mechanics for hosted QEMU VM scenarios.
# Callers own lifecycle assertions, evidence schema, and failure classification.
# lib/e2e.sh and lib/hosted_vm.sh must be sourced first.

HOSTED_VM_TUN_ENDPOINT_IP="${HOSTED_VM_PROVIDER_ENDPOINT_IP}"
HOSTED_VM_TUN_XRAY_ROOT=""
HOSTED_VM_TUN_XRAY_PID=""
HOSTED_VM_TUN_CANDIDATE=""
HOSTED_VM_TUN_EXPECTED_COMMIT=""
HOSTED_VM_TUN_PRIVATE="/tmp/podlaz-hosted-vm-tun"
HOSTED_VM_TUN_XDG="/home/e2e/.local/share/podlaz-hosted-vm-tun"

hosted_vm_tun_init() {
  HOSTED_VM_TUN_CANDIDATE="$1"
  HOSTED_VM_TUN_EXPECTED_COMMIT="$2"
  HOSTED_VM_TUN_XRAY_ROOT="$3"
  [[ -f "${HOSTED_VM_TUN_CANDIDATE}" && ! -L "${HOSTED_VM_TUN_CANDIDATE}" ]] || fail "VM TUN candidate must be a regular file"
  [[ -n "${HOSTED_VM_TUN_EXPECTED_COMMIT}" ]] || fail "VM TUN candidate commit is empty"
  install -d -m 0700 "${HOSTED_VM_TUN_XRAY_ROOT}"
}

hosted_vm_tun_start_endpoint() {
  local extract config uuid port
  extract="${HOSTED_VM_TUN_XRAY_ROOT}/package"
  config="${HOSTED_VM_TUN_XRAY_ROOT}/server.json"
  install -d -m 0700 "${extract}"
  dpkg-deb -x "${HOSTED_VM_TUN_CANDIDATE}" "${extract}"
  uuid="$("${extract}/usr/lib/podlaz/xray" uuid | tr -d '[:space:]')"
  [[ "${uuid}" =~ ^[0-9a-fA-F-]{36}$ ]] || return 1
  hosted_vm_provider_prepare_host
  port="$(python3 - "${HOSTED_VM_TUN_ENDPOINT_IP}" <<'PY'
import socket
import sys
sock=socket.socket(); sock.bind((sys.argv[1],0)); print(sock.getsockname()[1]); sock.close()
PY
)"
  cat >"${config}" <<EOF
{
  "log": {"loglevel": "warning"},
  "inbounds": [{
    "listen": "${HOSTED_VM_TUN_ENDPOINT_IP}",
    "port": ${port},
    "protocol": "vless",
    "settings": {"clients": [{"id": "${uuid}"}], "decryption": "none"},
    "streamSettings": {"security": "none"}
  }],
  "outbounds": [{"protocol": "freedom", "settings": {}}]
}
EOF
  chmod 0600 "${config}"
  "${extract}/usr/lib/podlaz/xray" run -test -config "${config}" >"${HOSTED_VM_TUN_XRAY_ROOT}/config-test.log" 2>&1
  "${extract}/usr/lib/podlaz/xray" run -config "${config}" >"${HOSTED_VM_TUN_XRAY_ROOT}/server.log" 2>&1 &
  HOSTED_VM_TUN_XRAY_PID=$!
  for _ in $(seq 1 100); do
    ss -H -ltn | awk '{print $4}' | grep -Fx "${HOSTED_VM_TUN_ENDPOINT_IP}:${port}" >/dev/null && break
    kill -0 "${HOSTED_VM_TUN_XRAY_PID}" >/dev/null 2>&1 || return 1
    sleep 0.1
  done
  ss -H -ltn | awk '{print $4}' | grep -Fx "${HOSTED_VM_TUN_ENDPOINT_IP}:${port}" >/dev/null || return 1
  printf 'vless://%s@%s:%s?type=tcp&security=none&encryption=none#hosted-vm-tun\n'     "${uuid}" "${HOSTED_VM_TUN_ENDPOINT_IP}" "${port}" >"${HOSTED_VM_TUN_XRAY_ROOT}/client-uri"
  chmod 0600 "${HOSTED_VM_TUN_XRAY_ROOT}/client-uri"
  :
}

hosted_vm_tun_stop_endpoint() {
  if [[ -n "${HOSTED_VM_TUN_XRAY_PID}" ]]; then
    kill "${HOSTED_VM_TUN_XRAY_PID}" >/dev/null 2>&1 || true
    wait "${HOSTED_VM_TUN_XRAY_PID}" >/dev/null 2>&1 || true
    HOSTED_VM_TUN_XRAY_PID=""
  fi
}

hosted_vm_tun_install_candidate() {
  hosted_vm_install_candidate "${HOSTED_VM_TUN_CANDIDATE}"
  hosted_vm_assert_candidate_provenance "${HOSTED_VM_TUN_CANDIDATE}" "${HOSTED_VM_TUN_EXPECTED_COMMIT}"
}

hosted_vm_tun_prepare_control() {
  hosted_vm_ssh sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl jq
  hosted_vm_prepare_guest_agent
  if [[ -n "${HOSTED_VM_PROVIDER_TAP}" ]]; then
    hosted_vm_provider_prepare_guest
  fi
}

hosted_vm_tun_assert_candidate_provenance() {
  hosted_vm_assert_candidate_provenance "${HOSTED_VM_TUN_CANDIDATE}" "${HOSTED_VM_TUN_EXPECTED_COMMIT}"
}

hosted_vm_tun_install_helpers() {
  local repo_root="$1"
  hosted_vm_scp_to "${repo_root}/scripts/e2e/lib/daemon_status_semantics.py" /tmp/daemon_status_semantics.py
  hosted_vm_scp_to "${repo_root}/scripts/e2e/hosted_synthetic_active_authority.py" /tmp/active_authority.py
  hosted_vm_scp_to "${repo_root}/scripts/e2e/hosted_synthetic_network_authority.py" /tmp/network_authority.py
  hosted_vm_scp_to "${repo_root}/scripts/e2e/lib/recovery_json.sh" /tmp/recovery_json.sh
  hosted_vm_scp_to "${HOSTED_VM_TUN_XRAY_ROOT}/client-uri" /tmp/client-uri
}

hosted_vm_tun_install_polkit() {
  local local_rule="$1"
  cat >"${local_rule}" <<'EOF'
polkit.addRule(function(action, subject) {
    if (subject.user == "e2e" &&
        (action.id == "io.github.aidarkhusainov.podlaz.connect-tun" ||
         action.id == "io.github.aidarkhusainov.podlaz.disconnect" ||
         action.id == "io.github.aidarkhusainov.podlaz.configure-autostart")) {
        return polkit.Result.YES;
    }
});
EOF
  hosted_vm_scp_to "${local_rule}" /tmp/podlaz-hosted-vm-polkit.rules
  hosted_vm_ssh sudo install -D -m 0644 /tmp/podlaz-hosted-vm-polkit.rules /etc/polkit-1/rules.d/49-podlaz-hosted-vm.rules
  hosted_vm_ssh sudo systemctl restart polkit.service
}

hosted_vm_tun_run_podlaz() {
  if [[ "${HOSTED_VM_GA_READY}" == true ]]; then
    hosted_vm_ga_exec /usr/sbin/runuser -u e2e -- env       "XDG_CONFIG_HOME=${HOSTED_VM_TUN_XDG}/config"       "XDG_STATE_HOME=${HOSTED_VM_TUN_XDG}/state"       "XDG_CACHE_HOME=${HOSTED_VM_TUN_XDG}/cache"       /usr/bin/podlaz "$@"
  else
    hosted_vm_ssh env       "XDG_CONFIG_HOME=${HOSTED_VM_TUN_XDG}/config"       "XDG_STATE_HOME=${HOSTED_VM_TUN_XDG}/state"       "XDG_CACHE_HOME=${HOSTED_VM_TUN_XDG}/cache"       /usr/bin/podlaz "$@"
  fi
}

hosted_vm_tun_prepare_profile() {
  local script
  script="$(cat <<'EOF'
set -Eeuo pipefail
install -d -o e2e -g e2e -m 0700 "$xdg" "$xdg/config" "$xdg/state" "$xdg/cache" "$private"
uri="$(cat /tmp/client-uri)"
runuser -u e2e -- env XDG_CONFIG_HOME="$xdg/config" XDG_STATE_HOME="$xdg/state" XDG_CACHE_HOME="$xdg/cache"   /usr/bin/podlaz profile import "$uri" >"$private/import.stdout" 2>"$private/import.stderr"
awk '/^Imported profile:/ {print $3; exit}' "$private/import.stdout" >"$private/profile-id"
test -s "$private/profile-id"
profile="$(cat "$private/profile-id")"
runuser -u e2e -- env XDG_CONFIG_HOME="$xdg/config" XDG_STATE_HOME="$xdg/state" XDG_CACHE_HOME="$xdg/cache"   /usr/bin/podlaz profile validate "$profile" --mode tun >"$private/validate.stdout" 2>"$private/validate.stderr"
EOF
)"
  hosted_vm_ga_bash "xdg=${HOSTED_VM_TUN_XDG@Q}; private=${HOSTED_VM_TUN_PRIVATE@Q}; ${script}"
}

hosted_vm_tun_profile_id() {
  hosted_vm_ga_bash "cat ${HOSTED_VM_TUN_PRIVATE@Q}/profile-id" | tr -d '[:space:]'
}

hosted_vm_tun_assert_endpoint_reachable() {
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

hosted_vm_tun_capture_direct_baseline() {
  local script
  script="$(cat <<'EOF'
set -Eeuo pipefail
uplink="$(ip -4 route show default | awk 'NR == 1 {for (i=1; i<=NF; i++) if ($i == "dev") {print $(i+1); exit}}')"
test -n "$uplink"
probe="$(getent ahostsv4 example.com | awk 'NR == 1 {print $1}')"
test -n "$probe"
timeout 20 curl -4 -fsS --interface "$uplink" --connect-timeout 5 --max-time 15   --resolve "example.com:443:$probe" https://example.com/ >/dev/null
printf '%s\n' "$uplink" >"$private/uplink"
printf '%s\n' "$probe" >"$private/probe-ip"
EOF
)"
  hosted_vm_ga_bash "private=${HOSTED_VM_TUN_PRIVATE@Q}; ${script}"
}

hosted_vm_tun_assert_direct_blocked() {
  local script
  script="$(cat <<'EOF'
set -Eeuo pipefail
uplink="$(cat "$private/uplink")"
probe="$(cat "$private/probe-ip")"
timeout 7 curl -4 -fsSk --interface "$uplink" --connect-timeout 3 --max-time 5   --resolve "example.com:443:$probe" https://example.com/ >/dev/null 2>&1
EOF
)"
  if hosted_vm_ga_bash "private=${HOSTED_VM_TUN_PRIVATE@Q}; ${script}"; then
    return 1
  fi
  return 0
}

hosted_vm_tun_create_foreign_state() {
  hosted_vm_ga_bash_stdin <<'EOF'
set -Eeuo pipefail
cat >/usr/local/sbin/podlaz-vm-foreign-fixture <<'SCRIPT'
#!/bin/sh
set -eu
nft list table inet pzvm_foreign >/dev/null 2>&1 || nft add table inet pzvm_foreign
ip link show dev pzvmforeign0 >/dev/null 2>&1 || ip link add dev pzvmforeign0 type dummy
ip -4 address show dev pzvmforeign0 | grep -F '192.0.2.1/32' >/dev/null 2>&1 || ip address add 192.0.2.1/32 dev pzvmforeign0
ip link set dev pzvmforeign0 up
SCRIPT
chmod 0755 /usr/local/sbin/podlaz-vm-foreign-fixture
cat >/etc/systemd/system/podlaz-vm-foreign.service <<'UNIT'
[Unit]
Description=Hosted VM foreign network fixture
After=network-pre.target
Before=network.target

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/podlaz-vm-foreign-fixture
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
systemctl enable --now podlaz-vm-foreign.service
EOF
}

hosted_vm_tun_assert_foreign_state() {
  hosted_vm_ga_bash 'systemctl is-active --quiet podlaz-vm-foreign.service && nft list table inet pzvm_foreign >/dev/null && ip -4 address show dev pzvmforeign0 | grep -F "192.0.2.1/32" >/dev/null'
}

hosted_vm_tun_remove_foreign_state() {
  if [[ "${HOSTED_VM_GA_READY}" == true ]]; then
    hosted_vm_ga_bash_stdin <<'EOF' >/dev/null 2>&1 || true
set +e
systemctl disable --now podlaz-vm-foreign.service
rm -f /etc/systemd/system/podlaz-vm-foreign.service /usr/local/sbin/podlaz-vm-foreign-fixture
systemctl daemon-reload
nft delete table inet pzvm_foreign
ip link del dev pzvmforeign0
exit 0
EOF
  else
    hosted_vm_ssh sudo systemctl disable --now podlaz-vm-foreign.service >/dev/null 2>&1 || true
    hosted_vm_ssh sudo rm -f /etc/systemd/system/podlaz-vm-foreign.service /usr/local/sbin/podlaz-vm-foreign-fixture >/dev/null 2>&1 || true
    hosted_vm_ssh sudo nft delete table inet pzvm_foreign >/dev/null 2>&1 || true
    hosted_vm_ssh sudo ip link del dev pzvmforeign0 >/dev/null 2>&1 || true
  fi
}

hosted_vm_tun_wait_status() {
  local target="$1" attempts="$2" command
  command="curl --fail --silent --show-error --max-time 5 --abstract-unix-socket podlazd http://localhost/v1/status >${HOSTED_VM_TUN_PRIVATE}/status.json 2>/dev/null && python3 /tmp/daemon_status_semantics.py '${target}' ${HOSTED_VM_TUN_PRIVATE}/status.json"
  for _ in $(seq 1 "${attempts}"); do
    if hosted_vm_ga_bash "${command}" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  return 1
}

hosted_vm_tun_connect() {
  local script
  script="$(cat <<'EOF'
set -Eeuo pipefail
profile="$(cat "$private/profile-id")"
runuser -u e2e -- env XDG_CONFIG_HOME="$xdg/config" XDG_STATE_HOME="$xdg/state" XDG_CACHE_HOME="$xdg/cache"   /usr/bin/podlaz connect --mode tun "$profile" >"$private/connect.stdout" 2>"$private/connect.stderr"
EOF
)"
  hosted_vm_ga_bash "xdg=${HOSTED_VM_TUN_XDG@Q}; private=${HOSTED_VM_TUN_PRIVATE@Q}; ${script}"
  hosted_vm_tun_wait_status verified-active 150
}

hosted_vm_tun_capture_exact_active_authority() {
  local snapshot="$1" script
  script="$(cat <<'EOF'
set -Eeuo pipefail
curl --fail --silent --show-error --max-time 5 --abstract-unix-socket podlazd http://localhost/v1/status >"$private/status.json"
resolvectl dns >"$private/resolved-dns.txt"
resolvectl domain >"$private/resolved-domain.txt"
resolvectl default-route >"$private/resolved-default-route.txt"
nft -j list ruleset >"$private/nft-ruleset.json"
python3 /tmp/active_authority.py   --status "$private/status.json" --transactions /run/podlaz/transactions   --session /run/podlaz/network-session-continuation.json --boot-id /proc/sys/kernel/random/boot_id   --runtime-config /run/podlaz/generated/xray.json --resolved-dns "$private/resolved-dns.txt"   --resolved-domain "$private/resolved-domain.txt" --resolved-default-route "$private/resolved-default-route.txt"   --nft-ruleset "$private/nft-ruleset.json"
if [[ "$snapshot" == yes ]]; then
  python3 /tmp/network_authority.py snapshot /run/podlaz/transactions "$private/network-manifest.json"
fi
python3 /tmp/network_authority.py verify-present "$private/network-manifest.json"
EOF
)"
  hosted_vm_ga_bash "private=${HOSTED_VM_TUN_PRIVATE@Q}; snapshot=${snapshot@Q}; ${script}"
}

hosted_vm_tun_session_id() {
  hosted_vm_ga_bash "jq -r '.session_id' /run/podlaz/network-session-continuation.json" | tr -d '[:space:]'
}

hosted_vm_tun_assert_armed_current_boot_session() {
  local boot
  boot="$(hosted_vm_boot_id)"
  hosted_vm_ga_bash "jq -e --arg boot ${boot@Q} '.schema_version==\"podlaz.network-session-state.v1\" and .owner==\"podlaz\" and .boot_id==\$boot and .intent==\"resume\" and .protection.state==\"armed\"' /run/podlaz/network-session-continuation.json >/dev/null"
}

hosted_vm_tun_run_traffic() {
  hosted_vm_ga_bash 'timeout 20 getent ahostsv4 example.com >/dev/null && timeout 30 curl -4 -fsS -o /dev/null https://example.com/'
}

hosted_vm_tun_disconnect() {
  hosted_vm_tun_run_podlaz disconnect >/dev/null
  hosted_vm_tun_wait_status clean-inactive 100
}

hosted_vm_tun_assert_exact_terminal_cleanup() {
  local script
  script="$(cat <<'EOF'
set -Eeuo pipefail
python3 /tmp/network_authority.py verify-absent "$private/network-manifest.json"
test ! -e /run/podlaz/network-session-continuation.json
test ! -e /run/podlaz/generated/xray.json
if test -d /run/podlaz/transactions; then
  test -z "$(find /run/podlaz/transactions -mindepth 1 -maxdepth 1 -type f -print -quit)"
fi
! ip link show dev podlaz0 >/dev/null 2>&1
! nft list tables 2>/dev/null | grep -E 'table inet podlaz_pe_[0-9a-f]+' >/dev/null
EOF
)"
  hosted_vm_ga_bash "private=${HOSTED_VM_TUN_PRIVATE@Q}; ${script}"
}

hosted_vm_tun_run_clean_recovery() {
  local script
  script="$(cat <<'EOF'
set -Eeuo pipefail
runuser -u e2e -- env XDG_CONFIG_HOME="$xdg/config" XDG_STATE_HOME="$xdg/state" XDG_CACHE_HOME="$xdg/cache"   /usr/bin/podlaz recover --json >"$private/recover.json" 2>"$private/recover.stderr"
source /tmp/recovery_json.sh
assert_clean_recovery_json_file "$private/recover.json"
EOF
)"
  hosted_vm_ga_bash "xdg=${HOSTED_VM_TUN_XDG@Q}; private=${HOSTED_VM_TUN_PRIVATE@Q}; ${script}"
}
