#!/usr/bin/env bash
set -Eeuo pipefail

OBSERVATION_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Reuse the installed-package guest, endpoint, authority and exact-cleanup owners.
PODLAZ_E2E_PROFILE_TRANSITIONS=true
# shellcheck source=hosted-synthetic-tun.sh
source "${OBSERVATION_SCRIPT_DIR}/hosted-synthetic-tun.sh"
# shellcheck source=lib/status_polling.sh
source "${OBSERVATION_SCRIPT_DIR}/lib/status_polling.sh"

OBS_ROOT=/run/podlaz/observation-fixture
OBS_FILE=/run/podlaz/diagnostics/connect-observation.jsonl
OBS_ENABLED="${PODLAZ_E2E_OBSERVATION_ENABLED:-true}"
OBS_RESULTS="${E2E_ARTIFACT_DIR}/connect-observation.jsonl"
# The focused driver uses the two-profile fixture but does not claim the broad
# inherited concurrency/restart suite. Its extra assertions have their own file.
OBS_BASE_KEYS=()
for key in "${EVIDENCE_KEYS[@]}"; do
  [[ "$key" == transition.* ]] || OBS_BASE_KEYS+=("$key")
done
EVIDENCE_KEYS=("${OBS_BASE_KEYS[@]}")

assert_public_artifact_privacy() {
  python3 - "${E2E_ARTIFACT_DIR}" <<'PRIVACY'
import json,re,sys
from pathlib import Path
root=Path(sys.argv[1])
scenarios={'none','b-first','b-second','b-timeout','restore-prequery','restore-query','rollback','reconciliation','cancelled-client'}
allowed={'hosted-synthetic-tun.txt','connect-observation.jsonl','connect-failure-summary.jsonl'}|{f'connect-observation-{s}.jsonl' for s in scenarios}
for p in root.iterdir():
    assert p.name in allowed and p.is_file() and not p.is_symlink(), 'unapproved public artifact'
    raw=p.read_text()
    assert not re.search(r'vless://|vmess://|172[.]31[.]|[0-9a-fA-F]{8}(?:-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}',raw), 'private fixture data'
    if p.suffix=='.jsonl':
        for line in raw.splitlines():
            row=json.loads(line)
            for v in row.values():
                if isinstance(v,str):
                    assert re.fullmatch(r'[A-Za-z0-9_-]{1,80}',v), 'unexpected diagnostic string'
PRIVACY
}

observation_failed_command() {
  # Keep only already structured daemon enums, never the raw journal or IDs.
  guest_exec journalctl -u podlazd.service -n 100 --no-pager -o cat | python3 -c '
import json,re,sys
allowed={"phase","rollback_status","classification","tun_primary_classification"}
for line in sys.stdin:
 if "connect request failed" not in line: continue
 row={key:value for key,value in re.findall(r"([a-z_]+)=([A-Za-z0-9_-]+)",line) if key in allowed}
 print(json.dumps(row,sort_keys=True))
' >>"${E2E_ARTIFACT_DIR}/connect-failure-summary.jsonl"
}

create_foreign_sentinel() {
  # Calibrate before the first TUN connection; the counter is observation only.
  guest_exec python3 /workspace/scripts/e2e/lib/connect_observation_egress.py /run/observation-calibration calibrate >>"${OBS_RESULTS}"
  ! guest_exec nft list table inet "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || return 1
  guest_exec nft add table inet "${FOREIGN_NFT_TABLE}"
}

observation_watch_start() {
  guest_exec rm -rf /run/observation-egress
  guest_exec python3 /workspace/scripts/e2e/lib/connect_observation_egress.py /run/observation-egress >"${PRIVATE_ROOT}/egress.json" 2>"${PRIVATE_ROOT}/egress.stderr" &
  OBS_WATCH_PID=$!
  wait_for_status_match "calibrated egress watch" 10 observation_watch_ready
}
observation_watch_ready() { guest_exec test -f /run/observation-egress/ready >/dev/null 2>&1; }
observation_watch_stop() {
  guest_exec touch /run/observation-egress/stop
  wait "${OBS_WATCH_PID}"
  cat "${PRIVATE_ROOT}/egress.json" >>"${OBS_RESULTS}"
}

observation_reset() {
  guest_exec /bin/bash -s -- "${OBS_ROOT}" "${OBS_ENABLED}" <<'GUEST'
set -euo pipefail
root="$1" enabled="$2"
rm -f "$root/fault.json" "$root/queries" "$root/failed-query" "$root/disable-core-ready" "$root/disable-core-continue" "$root/restore-ready" "$root/restore-continue"
chmod 0755 /usr/lib/podlaz/xray
install -d -m 0700 "$root" /etc/systemd/system/podlazd.service.d
install -d -m 0755 /opt/connect-observation-bin
install -m 0755 /workspace/scripts/e2e/lib/connect_observation_fault.py /opt/connect-observation-bin/resolvectl
ln -sf resolvectl /opt/connect-observation-bin/ip
until=0
if [[ "$enabled" == true ]]; then until="$(($(date +%s) + 880))"; fi
cat >/etc/systemd/system/podlazd.service.d/observation.conf <<UNIT
[Service]
Environment=PATH=/opt/connect-observation-bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
Environment=PODLAZ_CONNECT_OBSERVATION_UNTIL=$until
ExecStartPre=+/usr/bin/rm -f /run/podlaz/diagnostics/connect-observation.jsonl
UNIT
systemctl daemon-reload
systemctl restart podlazd.service
GUEST
  wait_guest_status verified-active 150
  assert_verified_active_authority
}

observation_arm() {
  local mode="$1"
  # The restart's replay already completed. Keep that independently correlated
  # operation, then arm the next operation; validation selects the last one.
  # Guest positional parameters are expanded by guest bash.
  # shellcheck disable=SC2016
  guest_exec /bin/bash -c 'printf "{\"mode\":\"%s\"}\n" "$1" >"$2/fault.json"; rm -f "$2/queries"' _ "${mode}" "${OBS_ROOT}"
}

observation_source_identity() {
  # Exact current service process, sampled on both sides of file collection.
  guest_exec /bin/bash -s <<'IDENTITY'
pid="$(systemctl show -p MainPID --value podlazd.service)"
python3 - "$pid" <<'PYID'
from pathlib import Path
import sys
pid=int(sys.argv[1]); assert pid>0
stat=Path(f"/proc/{pid}/stat").read_text()
tick=stat[stat.rindex(') ')+2:].split()[19]
assert tick.isdigit() and int(tick)>0
print(f"{pid}-{tick}")
PYID
IDENTITY
}

observation_save() {
  local mode="$1" path="${PRIVATE_ROOT}/observation-${1}.jsonl" source_before source_after
  source_before="$(observation_source_identity)"
  guest_exec cat "${OBS_FILE}" >"${path}"
  source_after="$(observation_source_identity)"
  [[ "$source_before" == "$source_after" ]]
  python3 "${SCRIPT_DIR}/lib/connect_observation_verdict.py" "${path}" "${mode}" "$source_before" >>"${OBS_RESULTS}"
  install -m 0600 "${path}" "${E2E_ARTIFACT_DIR}/connect-observation-${mode}.jsonl"
}

observation_cancelled_client() {
  local selector="$1" relay="${GUEST_PRIVATE}/observation-relay"
  guest_exec install -d -m 0700 -o e2e -g e2e "${relay}"
  guest_exec runuser -u e2e -- python3 /workspace/scripts/e2e/lib/connect_observation_cancel.py "${relay}/podlazd.sock" "${relay}/cancel" >"${PRIVATE_ROOT}/cancel-relay.log" 2>&1 &
  TRANSITION_PROXY_PID=$!
  wait_for_status_match "observation relay socket" 10 observation_relay_ready
  run_guest_user env PODLAZ_RUNTIME_DIR="${relay}" /usr/bin/podlaz connect "${selector}" >"${PRIVATE_ROOT}/cancel-client.stdout" 2>"${PRIVATE_ROOT}/cancel-client.stderr" &
  local client_pid=$!
  wait_for_status_match "B Query" 20 observation_query_ready
  guest_exec install -o e2e -g e2e -m 0600 /dev/null "${relay}/cancel"
  local client_rc=0
  wait "${client_pid}" || client_rc=$?
  (( client_rc != 0 ))
  wait "${TRANSITION_PROXY_PID}"
  TRANSITION_PROXY_PID=""
  wait_for_status_match "detached A Query" 20 observation_restore_ready
  # At this point the CLI is already gone and A is still inside its own Query.
  # shellcheck disable=SC2016
  guest_exec /bin/bash -c 'test -f "$1/restore-ready" && touch "$1/restore-continue"' _ "${OBS_ROOT}"
}
observation_relay_ready() { guest_exec test -S "${GUEST_PRIVATE}/observation-relay/podlazd.sock" >/dev/null 2>&1; }
observation_query_ready() { guest_exec test -f "${OBS_ROOT}/failed-query" >/dev/null 2>&1; }
observation_restore_ready() { guest_exec test -f "${OBS_ROOT}/restore-ready" >/dev/null 2>&1; }

qualify_profile_transitions() {
  local first second first_id session table mode rc started elapsed
  first="$(guest_exec cat "${GUEST_PRIVATE}/profile-selector")"
  first_id="$(guest_exec jq -er '.profile_id' "${GUEST_PRIVATE}/status.json")"
  session="$(guest_exec jq -er '.session_id' /run/podlaz/network-session-continuation.json)"
  table="$(guest_exec jq -er '.protection.table' /run/podlaz/network-session-continuation.json)"
  guest_exec install -o e2e -g e2e -m 0600 /run/podlaz-synthetic-xray/second-client-uri "${GUEST_PRIVATE}/second-client-uri"
  run_guest_user /usr/bin/podlaz import "${GUEST_PRIVATE}/second-client-uri" >"${PRIVATE_ROOT}/observation-import.stdout" 2>"${PRIVATE_ROOT}/observation-import.stderr"
  second="$(sed -n 's/^Profile: //p' "${PRIVATE_ROOT}/observation-import.stdout" | head -n1)"
  [[ -n "${first}" && -n "${second}" ]]

  observation_reset
  observation_watch_start
  for selector in "${second}" "${first}" "${second}" "${first}" "${second}" "${first}"; do
    mark_failure diagnostic_unknown observation.successful-transition
    started="$(date +%s%N)"
    rc=0
    run_guest_user /usr/bin/podlaz connect "${selector}" >"${PRIVATE_ROOT}/observation-success.stdout" 2>"${PRIVATE_ROOT}/observation-success.stderr" || rc=$?
    if (( rc != 0 )); then observation_failed_command; return "${rc}"; fi
    elapsed="$((($(date +%s%N) - started) / 1000))"
    printf '{"scenario":"successful-transition","enabled":"%s","elapsed_us":%s}\n' "${OBS_ENABLED}" "${elapsed}" >>"${OBS_RESULTS}"
    # A successful Connect can return while health initialization is pending.
    # The next canonical replacement requires the same verified-active gate as
    # the inherited transition suite; waiting does not retry a Connect.
    mark_failure diagnostic_unknown observation.transition-verified-active
    if ! wait_guest_status verified-active 150; then observation_failed_command; return 1; fi
    assert_verified_active_authority
  done
  observation_watch_stop
  assert_transition_verified_active "${first_id}" "${session}" "${table}"
  run_active_traffic_checks
  if [[ "${OBS_ENABLED}" != true ]]; then
    observation_arm b-timeout
    observation_watch_start
    started="$(date +%s%N)"
    rc=0
    run_guest_user /usr/bin/podlaz connect "${second}" >"${PRIVATE_ROOT}/reference-timeout.stdout" 2>"${PRIVATE_ROOT}/reference-timeout.stderr" || rc=$?
    elapsed="$((($(date +%s%N) - started) / 1000))"
    (( rc != 0 ))
    observation_watch_stop
    printf '{"scenario":"b-timeout","enabled":"false","elapsed_us":%s}\n' "${elapsed}" >>"${OBS_RESULTS}"
    assert_transition_verified_active "${first_id}" "${session}" "${table}"
    guest_exec rm -f "${OBS_ROOT}/fault.json"
    guest_exec test ! -e "${OBS_FILE}"
    return
  fi
  observation_save none

  for mode in b-first b-second b-timeout restore-prequery restore-query rollback reconciliation cancelled-client; do
    mark_failure diagnostic_unknown "observation.${mode}"
    observation_reset
    observation_arm "${mode}"
    observation_watch_start
    if [[ "${mode}" == restore-prequery ]]; then
      # shellcheck disable=SC2016
      guest_exec /bin/bash -c 'for ((i=0;i<500;i++)); do if test -f "$1/disable-core-ready"; then chmod 0600 /usr/lib/podlaz/xray; touch "$1/disable-core-continue"; exit; fi; sleep 0.02; done; exit 1' _ "${OBS_ROOT}" >"${PRIVATE_ROOT}/prequery-controller.log" 2>&1 &
      OBS_CONTROLLER_PID=$!
    fi
    started="$(date +%s%N)"
    if [[ "${mode}" == cancelled-client ]]; then
      observation_cancelled_client "${second}"
    else
      rc=0
      run_guest_user /usr/bin/podlaz connect "${second}" >"${PRIVATE_ROOT}/observation-${mode}.stdout" 2>"${PRIVATE_ROOT}/observation-${mode}.stderr" || rc=$?
      (( rc != 0 ))
    fi
    elapsed="$((($(date +%s%N) - started) / 1000))"
    printf '{"scenario":"%s","enabled":"true","elapsed_us":%s}\n' "${mode}" "${elapsed}" >>"${OBS_RESULTS}"
    if [[ "${mode}" == restore-prequery ]]; then wait "${OBS_CONTROLLER_PID}"; fi
    observation_watch_stop
    if [[ "${mode}" == rollback || "${mode}" == reconciliation || "${mode}" == restore-* ]]; then
      run_guest_user /usr/bin/podlaz status >"${PRIVATE_ROOT}/fault-status.stdout" 2>"${PRIVATE_ROOT}/fault-status.stderr" || true
      if grep -Fxq 'Status: Connected' "${PRIVATE_ROOT}/fault-status.stdout"; then return 1; fi
    else
      assert_transition_verified_active "${first_id}" "${session}" "${table}"
    fi
    observation_save "${mode}"
    assert_foreign_sentinel
    guest_exec rm -f "${OBS_ROOT}/fault.json"
    guest_exec chmod 0755 /usr/lib/podlaz/xray
    if [[ "${mode}" == reconciliation ]]; then guest_exec nft delete chain inet "${table}" observation_foreign; fi
    # Guest-only recovery of a deliberately injected failure, after archiving
    # its terminal result. No retries or changed policy in the daemon.
    guest_exec systemctl restart podlazd.service
    wait_guest_status verified-active 150
  done
}

# This script is restricted to the isolated hosted infrastructure. Its sourced
# harness owns exact outer-plumbing baseline restoration and guest destruction.
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  [[ "${GITHUB_ACTIONS:-}" == true ]] || fail "connect observation guest requires an isolated GitHub runner"
  main "$@"
fi
