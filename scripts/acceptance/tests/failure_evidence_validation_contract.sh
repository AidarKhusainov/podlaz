#!/usr/bin/env bash
set -Eeuo pipefail
umask 0077

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
SCRIPT="$ROOT/scripts/acceptance/release-laptop.sh"
# shellcheck source=/dev/null
source "$SCRIPT"

fail() { printf 'failure_evidence_validation_contract: %s\n' "$*" >&2; exit 1; }
assert_eq() { [[ "$1" == "$2" ]] || fail "$3: got [$1], want [$2]"; }
latest_bundle() { find "$RA_PRIVATE_DIR/failures" -mindepth 1 -maxdepth 1 -type d -name '20*' -print | sort | tail -n1; }

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
RA_BOOT_ATTEMPT="$TMP/boot-autostart-attempt.json"
RA_TRANSACTIONS="$TMP/transactions"
RA_RESUME_DIAGNOSTIC="$TMP/network-session-resume.json"
mkdir -m 0700 "$RA_TRANSACTIONS"

ra_artifacts_init_new evidence-validation
cat >"$RA_CONTINUATION" <<'JSON'
{"schema_version":"podlaz.network-session-state.v1","owner":"podlaz"}
JSON
chmod 0600 "$RA_CONTINUATION"

write_checkpoint() {
  local scenario="$1" candidate_applied="${2:-false}"
  local mutations='{}'
  if [[ "$candidate_applied" == true ]]; then
    mutations='{"candidate_upgrade":{"state":"acquired","kind":"candidate_package","identity":{"applied":true}}}'
  fi
  cat >"$RA_CHECKPOINT" <<JSON
{
  "schema_version":"podlaz.release-acceptance-checkpoint.v5",
  "run_id":"evidence-validation",
  "run_started_at":"2026-09-14T08:00:00Z",
  "starting_boot_id":"01234567-89ab-cdef-0123-456789abcdef",
  "current_boot_id":"01234567-89ab-cdef-0123-456789abcdef",
  "phase":"fail-cleanup-failed",
  "current_scenario":"$scenario",
  "last_failure":{"boot_id":"01234567-89ab-cdef-0123-456789abcdef"},
  "candidate":{"package":"podlaz","version":"9.9.9","architecture":"amd64","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "mutations":$mutations,
  "scenarios":{
    "$scenario":{"name":"$scenario","state":"failed","started_at":"2026-09-14T08:01:00Z"}
  },
  "private":{"artifact_root":"$RA_ARTIFACT_DIR","service_active_before":true,"run_config":{"soak_minutes":60},"resource":{}}
}
JSON
  chmod 0600 "$RA_CHECKPOINT"
}

ra_status_json() { printf '%s' '{"connection":"inactive","transactions":[]}'; }
ra_product() {
  if [[ "${1:-}" == doctor && "${2:-}" == --tun && "${3:-}" == --json ]]; then
    RA_CAPTURE='{"schema_version":1,"status":"unhealthy","primary_classification":"network_apply_failure"}'
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

# Required boot-attempt evidence must be structurally valid, not merely a readable file.
write_checkpoint reboot_autostart_on false
printf '%s\n' '{not-json' >"$RA_BOOT_ATTEMPT"
chmod 0600 "$RA_BOOT_ATTEMPT"
ra_failure_bundle_capture invalid_required_boot_attempt 1 automatic-finalizer
INVALID_BOOT_BUNDLE="$(latest_bundle)"
assert_eq "$(jq -r '.components.boot_attempt.observation // ""' "$INVALID_BOOT_BUNDLE/metadata.json")" invalid 'invalid required boot attempt observation'
assert_eq "$(jq -r '.components.boot_attempt.applicability // ""' "$INVALID_BOOT_BUNDLE/metadata.json")" required 'invalid required boot attempt applicability'
assert_eq "$(jq -r '.capture_status' "$INVALID_BOOT_BUNDLE/metadata.json")" partial 'invalid required boot attempt completeness'

# Terminal attempts must preserve one of the daemon-owned typed terminal reasons.
cat >"$RA_BOOT_ATTEMPT" <<'JSON'
{
  "schema_version":"podlaz.boot-autostart-attempt.v1",
  "boot_id":"01234567-89ab-cdef-0123-456789abcdef",
  "manifest_generation":"0123456789abcdef0123456789abcdef",
  "state":"terminal",
  "terminal_reason":"not-a-real-reason",
  "configuration":{}
}
JSON
chmod 0600 "$RA_BOOT_ATTEMPT"
ra_failure_bundle_capture invalid_terminal_boot_attempt 1 automatic-finalizer
INVALID_TERMINAL_BUNDLE="$(latest_bundle)"
assert_eq "$(jq -r '.components.boot_attempt.observation // ""' "$INVALID_TERMINAL_BUNDLE/metadata.json")" invalid 'invalid terminal boot reason observation'
assert_eq "$(jq -r '.capture_status' "$INVALID_TERMINAL_BUNDLE/metadata.json")" partial 'invalid terminal boot reason completeness'

# A valid current-boot attempt satisfies the same required component.
cat >"$RA_BOOT_ATTEMPT" <<'JSON'
{
  "schema_version":"podlaz.boot-autostart-attempt.v1",
  "boot_id":"01234567-89ab-cdef-0123-456789abcdef",
  "manifest_generation":"0123456789abcdef0123456789abcdef",
  "state":"succeeded",
  "configuration":{}
}
JSON
chmod 0600 "$RA_BOOT_ATTEMPT"
ra_failure_bundle_capture valid_required_boot_attempt 1 automatic-finalizer
VALID_BOOT_BUNDLE="$(latest_bundle)"
assert_eq "$(jq -r '.components.boot_attempt.observation // .components.boot_attempt.status // ""' "$VALID_BOOT_BUNDLE/metadata.json")" captured 'valid required boot attempt observation'
assert_eq "$(jq -r '.capture_status' "$VALID_BOOT_BUNDLE/metadata.json")" complete 'valid required boot attempt completeness'

# Once candidate replacement has been durably recorded as applied inside the
# lower-release-upgrade scenario, replay evidence is required even if the file
# itself has disappeared before immutable failure capture.
rm -f "$RA_BOOT_ATTEMPT" "$RA_RESUME_DIAGNOSTIC"
write_checkpoint lower_release_upgrade true
ra_failure_bundle_capture required_replay_diagnostic_absent 1 automatic-finalizer
REPLAY_BUNDLE="$(latest_bundle)"
assert_eq "$(jq -r '.components.replay_diagnostic.observation // ""' "$REPLAY_BUNDLE/metadata.json")" verified_absent 'required replay diagnostic absence observation'
assert_eq "$(jq -r '.components.replay_diagnostic.applicability // ""' "$REPLAY_BUNDLE/metadata.json")" required 'required replay diagnostic applicability'
assert_eq "$(jq -r '.capture_status' "$REPLAY_BUNDLE/metadata.json")" partial 'required replay diagnostic absence completeness'

# The same absent diagnostic remains optional before candidate replacement is applied.
write_checkpoint lower_release_upgrade false
ra_failure_bundle_capture optional_pre_replay_diagnostic_absent 1 automatic-finalizer
PRE_REPLAY_BUNDLE="$(latest_bundle)"
assert_eq "$(jq -r '.components.replay_diagnostic.observation // ""' "$PRE_REPLAY_BUNDLE/metadata.json")" verified_absent 'pre-replay diagnostic absence observation'
assert_eq "$(jq -r '.components.replay_diagnostic.applicability // ""' "$PRE_REPLAY_BUNDLE/metadata.json")" optional 'pre-replay diagnostic applicability'
assert_eq "$(jq -r '.capture_status' "$PRE_REPLAY_BUNDLE/metadata.json")" complete 'pre-replay optional absence completeness'

printf 'failure_evidence_validation_contract: PASS\n'
