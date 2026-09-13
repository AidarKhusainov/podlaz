#!/usr/bin/env bash
set -Eeuo pipefail
umask 0077

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
SCRIPT="$ROOT/scripts/acceptance/release-laptop.sh"
# shellcheck source=/dev/null
source "$SCRIPT"

failures=0
expect_eq() {
  local got="$1" want="$2" label="$3"
  if [[ "$got" != "$want" ]]; then
    printf 'standalone_failure_evidence_truth: %s: got [%s], want [%s]\n' "$label" "$got" "$want" >&2
    ((failures+=1))
  fi
}
expect_file() {
  local path="$1" label="$2"
  if [[ ! -f "$path" || -L "$path" ]]; then
    printf 'standalone_failure_evidence_truth: %s: missing regular file %s\n' "$label" "$path" >&2
    ((failures+=1))
  fi
}

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

export RELEASE_ACCEPTANCE_TEST_MODE=1
RA_USER=tester
RA_UID="$(id -u)"
RA_GID="$(id -g)"
RA_HOME="$TMP/home"
mkdir -m 0700 "$RA_HOME"
RA_STATE_DIR="$TMP/state"
mkdir -m 0700 "$RA_STATE_DIR"
RA_CHECKPOINT="$RA_STATE_DIR/current.json"
RA_LOCK_FILE="$RA_STATE_DIR/lock"
RA_ARTIFACT_DIR="$RA_HOME/artifacts"
RA_MODE=new
RA_SOAK_MINUTES=60
RA_SOCKET="$TMP/fake.sock"
RA_CONTINUATION="$TMP/network-session.json"
RA_BOOT_ATTEMPT="$TMP/missing-boot-attempt.json"
RA_TRANSACTIONS="$TMP/transactions"
RA_RESUME_DIAGNOSTIC="$TMP/network-session-resume.json"
mkdir -m 0700 "$RA_TRANSACTIONS"

ra_artifacts_init_new evidence-truth

cat >"$RA_CONTINUATION" <<'JSON'
{"schema_version":"podlaz.network-session-state.v1","owner":"podlaz"}
JSON
chmod 0600 "$RA_CONTINUATION"

cat >"$RA_RESUME_DIAGNOSTIC" <<'JSON'
{
  "schema_version":"podlaz.network-session-resume-diagnostic.v1",
  "owner":"podlaz",
  "boot_id":"01234567-89ab-cdef-0123-456789abcdef",
  "recovery_epoch":1,
  "resume_stage":"connect-replay",
  "last_resume_outcome":"failed",
  "tun_failure_phase":"network-apply",
  "rollback_status":"completed",
  "transaction_present":true,
  "replay_disposition":"incomplete",
  "private_test_marker":"private-replay-marker.example.invalid"
}
JSON
chmod 0600 "$RA_RESUME_DIAGNOSTIC"

cat >"$RA_CHECKPOINT" <<JSON
{
  "schema_version":"podlaz.release-acceptance-checkpoint.v5",
  "run_id":"evidence-truth",
  "run_started_at":"2026-09-13T06:00:00Z",
  "starting_boot_id":"01234567-89ab-cdef-0123-456789abcdef",
  "current_boot_id":"01234567-89ab-cdef-0123-456789abcdef",
  "phase":"fail-cleanup-failed",
  "current_scenario":"lower_release_upgrade",
  "last_failure":{"boot_id":"01234567-89ab-cdef-0123-456789abcdef"},
  "candidate":{"package":"podlaz","version":"9.9.9","architecture":"amd64","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "mutations":{},
  "scenarios":{
    "lower_release_upgrade":{"name":"lower_release_upgrade","state":"failed","started_at":"2026-09-13T06:01:00Z"},
    "never_admitted":{"name":"never_admitted"},
    "user_skipped":{"name":"user_skipped","outcome":"SKIP_USER_REQUEST"}
  },
  "private":{"artifact_root":"$RA_ARTIFACT_DIR","service_active_before":true,"run_config":{"soak_minutes":60},"resource":{}}
}
JSON
chmod 0600 "$RA_CHECKPOINT"

# Keep this contract focused on failure-evidence semantics rather than host command availability.
ra_status_json() {
  printf '%s' '{"connection":"inactive","transactions":[]}'
}
ra_product() {
  if [[ "${1:-}" == doctor && "${2:-}" == --tun && "${3:-}" == --json ]]; then
    RA_CAPTURE='{"schema_version":"v1","status":"unhealthy","primary_classification":"network_apply_failure","historical":true,"rollback_status":"completed"}'
    RA_CAPTURE_RC=3
    return 3
  fi
  RA_CAPTURE='{}'
  RA_CAPTURE_RC=0
  return 0
}
ra_failure_capture_command() {
  local file="$1"
  shift
  { printf 'rc=0\n'; printf '{}\n'; } | ra_artifact_file_write "$file" || return 1
  printf captured
}
ra_failure_dpkg_slice() {
  local _source="$1" _since="$2" target="$3"
  printf 'example package event\n' | ra_artifact_file_write "$target" || return 1
  printf captured
}
ra_failure_process_identity() {
  printf '%s' '{"pid":42,"start_time_ticks":"100","exe":"/usr/bin/podlazd","cgroup_path":"/system.slice/podlazd.service"}'
}

ra_failure_bundle_capture product_failure 1 automatic-finalizer
BUNDLE="$(find "$RA_PRIVATE_DIR/failures" -mindepth 1 -maxdepth 1 -type d -name '20*' -print | sort | tail -n1)"
[[ -n "$BUNDLE" ]] || { printf 'standalone_failure_evidence_truth: failure bundle missing\n' >&2; exit 1; }

expect_eq "$(jq -r '.components.doctor.status // .components.doctor.observation // ""' "$BUNDLE/metadata.json")" captured 'semantic doctor rc=3 is captured evidence'
expect_eq "$(jq -r '.components.boot_attempt.observation // ""' "$BUNDLE/metadata.json")" verified_absent 'missing boot attempt is verified absent'
expect_eq "$(jq -r '.components.boot_attempt.applicability // ""' "$BUNDLE/metadata.json")" not_applicable 'boot attempt is not applicable to lower-release upgrade failure'
expect_eq "$(jq -r '.components.replay_diagnostic.status // .components.replay_diagnostic.observation // ""' "$BUNDLE/metadata.json")" captured 'replay diagnostic captured privately'
expect_eq "$(jq -r '.capture_status' "$BUNDLE/metadata.json")" complete 'valid semantic/absence evidence keeps bundle complete'
expect_file "$BUNDLE/network-session-resume.json" 'private replay diagnostic copy'
if [[ -f "$BUNDLE/network-session-resume.json" ]]; then
  expect_eq "$(jq -r '.private_test_marker' "$BUNDLE/network-session-resume.json")" private-replay-marker.example.invalid 'private replay marker preserved in private evidence'
fi

ra_report_write FAIL_CLEANUP_FAILED
expect_eq "$(jq -r '.scenarios.lower_release_upgrade.outcome' "$RA_PUBLIC_DIR/report.json")" FAIL 'failed controller state renders FAIL'
expect_eq "$(jq -r '.scenarios.never_admitted.outcome' "$RA_PUBLIC_DIR/report.json")" NOT_EXERCISED 'never-admitted scenario stays NOT_EXERCISED'
expect_eq "$(jq -r '.scenarios.user_skipped.outcome' "$RA_PUBLIC_DIR/report.json")" SKIP_USER_REQUEST 'typed user skip preserved'
expect_eq "$(jq -r '.qualification' "$RA_PUBLIC_DIR/report.json")" FAIL_CLEANUP_FAILED 'cleanup result remains independent top-level qualification'
if grep -R -Fq 'private-replay-marker.example.invalid' "$RA_PUBLIC_DIR"; then
  printf 'standalone_failure_evidence_truth: private replay diagnostic leaked into public artifacts\n' >&2
  ((failures+=1))
fi

if ((failures != 0)); then
  printf 'standalone_failure_evidence_truth: FAIL (%d assertions)\n' "$failures" >&2
  exit 1
fi
printf 'standalone_failure_evidence_truth: PASS\n'
