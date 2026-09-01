#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
SCRIPT="$ROOT/scripts/acceptance/release-laptop.sh"

fail() {
  printf 'standalone_acceptance_design: %s\n' "$*" >&2
  exit 1
}

export RELEASE_ACCEPTANCE_TEST_MODE=1
# shellcheck source=/dev/null
source "$SCRIPT"

# --help is a normal success path and must print the usage block exactly once.
help_output="$(SUDO_USER="${USER:-tester}" bash "$SCRIPT" --help 2>&1)" || fail "--help returned non-zero"
[[ "$(grep -c '^Usage:$' <<<"$help_output")" == 1 ]] || fail "--help prints usage more than once"

# Resource summaries must be numeric, measured-window summaries rather than raw JSONL only.
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
cat >"$tmp/first.jsonl" <<'JSONL'
{"tag":"soak","elapsed":10,"identity":{"transaction_id":"tx-1","session_id":"session-1","config_ref":"/run/podlaz/generated/one.json","daemon":{"pid":100,"start_time_ticks":1000,"exe":"/usr/bin/podlazd","cgroup_path":"/system.slice/podlazd.service"},"xray":{"pid":101,"start_time_ticks":1001,"exe":"/usr/lib/podlaz/xray","cgroup_path":"/system.slice/podlazd.service"}},"daemon":{"rss_kb":100,"pss_kb":80,"threads":4,"fds":10,"cpu_ticks":100},"xray":{"rss_kb":200,"pss_kb":160,"threads":6,"fds":20,"cpu_ticks":200},"service":{"memory_current":1000,"memory_peak":5000,"pids_current":2,"cpu_usage_usec":10000}}
{"tag":"soak","elapsed":20,"identity":{"transaction_id":"tx-1","session_id":"session-1","config_ref":"/run/podlaz/generated/one.json","daemon":{"pid":100,"start_time_ticks":1000,"exe":"/usr/bin/podlazd","cgroup_path":"/system.slice/podlazd.service"},"xray":{"pid":101,"start_time_ticks":1001,"exe":"/usr/lib/podlaz/xray","cgroup_path":"/system.slice/podlazd.service"}},"daemon":{"rss_kb":120,"pss_kb":90,"threads":4,"fds":11,"cpu_ticks":130},"xray":{"rss_kb":220,"pss_kb":170,"threads":6,"fds":21,"cpu_ticks":250},"service":{"memory_current":1200,"memory_peak":5100,"pids_current":2,"cpu_usage_usec":12000}}
{"tag":"soak","elapsed":30,"identity":{"transaction_id":"tx-1","session_id":"session-1","config_ref":"/run/podlaz/generated/one.json","daemon":{"pid":100,"start_time_ticks":1000,"exe":"/usr/bin/podlazd","cgroup_path":"/system.slice/podlazd.service"},"xray":{"pid":101,"start_time_ticks":1001,"exe":"/usr/lib/podlaz/xray","cgroup_path":"/system.slice/podlazd.service"}},"daemon":{"rss_kb":140,"pss_kb":100,"threads":5,"fds":12,"cpu_ticks":160},"xray":{"rss_kb":240,"pss_kb":180,"threads":7,"fds":22,"cpu_ticks":300},"service":{"memory_current":1400,"memory_peak":5200,"pids_current":2,"cpu_usage_usec":15000}}
JSONL

first_summary="$(ra_resource_summary_file "$tmp/first.jsonl")" || fail "resource summary helper failed"
jq -e '
  .sample_count==3 and
  .identity.transaction_id=="tx-1" and
  .identity.session_id=="session-1" and
  .daemon.rss_kb.median==120 and .daemon.rss_kb.last==140 and .daemon.rss_kb.sampled_peak==140 and
  .xray.rss_kb.median==220 and .xray.fds.delta==2 and
  .service.memory_current.median==1200 and .service.memory_current.sampled_peak==1400 and
  .service.lifetime_memory_peak==5200 and
  .service.cpu_usage_usec.delta==5000
' <<<"$first_summary" >/dev/null || fail "resource summary values are incomplete/wrong"

cat >"$tmp/second.jsonl" <<'JSONL'
{"tag":"second_session","elapsed":40,"identity":{"transaction_id":"tx-2","session_id":"session-2","config_ref":"/run/podlaz/generated/two.json","daemon":{"pid":100,"start_time_ticks":1000,"exe":"/usr/bin/podlazd","cgroup_path":"/system.slice/podlazd.service"},"xray":{"pid":102,"start_time_ticks":2001,"exe":"/usr/lib/podlaz/xray","cgroup_path":"/system.slice/podlazd.service"}},"daemon":{"rss_kb":130,"pss_kb":95,"threads":4,"fds":11,"cpu_ticks":170},"xray":{"rss_kb":230,"pss_kb":175,"threads":6,"fds":21,"cpu_ticks":310},"service":{"memory_current":1300,"memory_peak":5300,"pids_current":2,"cpu_usage_usec":16000}}
{"tag":"second_session","elapsed":50,"identity":{"transaction_id":"tx-2","session_id":"session-2","config_ref":"/run/podlaz/generated/two.json","daemon":{"pid":100,"start_time_ticks":1000,"exe":"/usr/bin/podlazd","cgroup_path":"/system.slice/podlazd.service"},"xray":{"pid":102,"start_time_ticks":2001,"exe":"/usr/lib/podlaz/xray","cgroup_path":"/system.slice/podlazd.service"}},"daemon":{"rss_kb":135,"pss_kb":98,"threads":4,"fds":11,"cpu_ticks":180},"xray":{"rss_kb":235,"pss_kb":178,"threads":6,"fds":21,"cpu_ticks":320},"service":{"memory_current":1350,"memory_peak":5350,"pids_current":2,"cpu_usage_usec":17000}}
JSONL
second_summary="$(ra_resource_summary_file "$tmp/second.jsonl")" || fail "second resource summary helper failed"
comparison="$(ra_resource_compare_sessions "$first_summary" "$second_summary")" || fail "resource session comparison failed"
jq -e '.compared==true and .first_session_id=="session-1" and .second_session_id=="session-2" and .same_session==false and (.deltas|type)=="object" and .structural_nonaccumulation==true' <<<"$comparison" >/dev/null || fail "resource comparison is not real/structural"

# Wi-Fi evidence reports whether address/gateway identity actually changed.
before='{"connection":"wifi","device":"wlan0","addresses":["192.0.2.10/24"],"gateway":"192.0.2.1"}'
after_same='{"connection":"wifi","device":"wlan0","addresses":["192.0.2.10/24"],"gateway":"192.0.2.1"}'
after_changed='{"connection":"wifi","device":"wlan0","addresses":["192.0.2.20/24"],"gateway":"192.0.2.1"}'
[[ "$(ra_dhcp_identity_changed "$before" "$after_same")" == false ]] || fail "same DHCP identity reported changed"
[[ "$(ra_dhcp_identity_changed "$before" "$after_changed")" == true ]] || fail "changed DHCP identity was not detected"

# Failure classification must preserve host-capability/state and internal semantics.
[[ "$(ra_failure_class host_capability_suspend)" == HOST_CAPABILITY ]] || fail "HOST_CAPABILITY classification missing"
[[ "$(ra_failure_class host_state_wifi_unavailable)" == HOST_STATE ]] || fail "HOST_STATE classification missing"
[[ "$(ra_failure_class status_contract_incompatible)" == INTERNAL ]] || fail "INTERNAL classification missing"

# The implementation must persist rollback authority before SIGKILL and scope journals to the recorded boot.
grep -q 'rolling_back_authority' "$SCRIPT" || fail "rollback authority evidence is not persisted"
grep -q -- '--boot' "$SCRIPT" || fail "failure journal is not boot-scoped"

# Meaningful suspend is 60-120 seconds and reports actual observed interval.
grep -Eq 'rtcwake[^\n]*-s[[:space:]]+(60|[6-9][0-9]|1[01][0-9]|120)' "$SCRIPT" || fail "suspend duration is not meaningful"
grep -q 'observed_suspend_seconds' "$SCRIPT" || fail "suspend interval evidence missing"

# Final restoration must include service state and run-tree ownership/mode verification.
grep -q 'service_active_before' "$SCRIPT" || fail "original service state is not captured"
grep -q 'run_tree' "$SCRIPT" || fail "run-tree restoration invariant missing"

printf 'standalone_acceptance_design: PASS\n'
