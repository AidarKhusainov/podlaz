#!/usr/bin/env bash
set -Eeuo pipefail
umask 0077

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
SCRIPT="$ROOT/scripts/acceptance/release-laptop.sh"
# shellcheck source=/dev/null
source "$SCRIPT"

fail() { printf 'standalone_release_evidence: %s\n' "$*" >&2; exit 1; }
assert_eq() { [[ "$1" == "$2" ]] || fail "expected [$2], got [$1]"; }
assert_contains() { grep -Fq -- "$2" <<<"$1" || fail "expected [$1] to contain [$2]"; }

for fn in ra_artifact_dir_ensure ra_artifact_file_write ra_artifact_file_append ra_boot_id_normalize ra_failure_bundle_capture ra_verify_run_tree; do
  declare -F "$fn" >/dev/null || fail "missing release-evidence seam: $fn"
done

assert_eq "$(ra_boot_id_normalize 01234567-89AB-CDEF-0123-456789ABCDEF)" "0123456789abcdef0123456789abcdef"
assert_eq "$(ra_boot_id_normalize 0123456789abcdef0123456789abcdef)" "0123456789abcdef0123456789abcdef"
if ra_boot_id_normalize 'not-a-boot-id' >/dev/null 2>&1; then fail "invalid boot id was accepted"; fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
FAKE="$TMP/fake-bin"
mkdir -p "$FAKE"
export TEST_CAPTURE_DIR="$TMP/capture"
mkdir -p "$TEST_CAPTURE_DIR"
export TEST_MAIN_PID=$$

cat >"$FAKE/journalctl" <<'SH'
#!/usr/bin/env bash
printf '%q ' "$@" >>"$TEST_CAPTURE_DIR/journal-argv"
printf '\n' >>"$TEST_CAPTURE_DIR/journal-argv"
for a in "$@"; do [[ "$a" != --boot ]] || { echo 'invalid --boot usage' >&2; exit 64; }; done
[[ " $* " == *' _BOOT_ID=0123456789abcdef0123456789abcdef '* ]] || { echo 'missing exact _BOOT_ID' >&2; exit 65; }
[[ " $* " == *' --since 2026-09-03T08:00:00Z '* ]] || { echo 'missing scenario since' >&2; exit 66; }
[[ "${JOURNAL_FAIL:-0}" != 1 ]] || { echo 'journal command failed' >&2; exit 67; }
printf 'current-boot-only private-marker.example.invalid\n'
SH
cat >"$FAKE/systemctl" <<'SH'
#!/usr/bin/env bash
if [[ "$1" == show && " $* " == *' -p MainPID '* && " $* " == *' --value '* ]]; then printf '%s\n' "$TEST_MAIN_PID"; exit 0; fi
case "$1" in
  show) cat <<OUT
MainPID=$TEST_MAIN_PID
ActiveState=active
SubState=running
Result=success
ExecMainCode=1
ExecMainStatus=0
KillSignal=15
RestartKillSignal=15
KillMode=control-group
TimeoutStopUSec=90000000
FragmentPath=/usr/lib/systemd/system/podlazd.service
OUT
    ;;
  status) printf 'podlazd active\n' ;;
  *) exit 0 ;;
esac
SH
cat >"$FAKE/systemd" <<'SH'
#!/usr/bin/env bash
printf 'systemd 257 (257.5)\n'
SH
cat >"$FAKE/curl" <<'SH'
#!/usr/bin/env bash
printf '{"connection":"%s","transactions":[],"evidence_marker":"%s"}\n' "${TEST_STATUS_CONNECTION:-error (core exited)}" "${TEST_EVIDENCE_MARKER:-failure-a}"
SH
cat >"$FAKE/podlaz" <<'SH'
#!/usr/bin/env bash
printf '{"schema_version":"v1","status":"ok","evidence_marker":"%s"}\n' "${TEST_EVIDENCE_MARKER:-failure-a}"
SH
cat >"$FAKE/dpkg-query" <<'SH'
#!/usr/bin/env bash
printf 'install ok installed\t2.0\tamd64\n'
SH
for cmd in ip nft resolvectl; do
  cat >"$FAKE/$cmd" <<'SH'
#!/usr/bin/env bash
printf '{}\n'
SH
  chmod +x "$FAKE/$cmd"
done
chmod +x "$FAKE/journalctl" "$FAKE/systemctl" "$FAKE/systemd" "$FAKE/curl" "$FAKE/podlaz" "$FAKE/dpkg-query"
export PATH="$FAKE:$PATH"

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
RA_CONTINUATION="$TMP/missing-continuation.json"
RA_BOOT_ATTEMPT="$TMP/missing-boot-attempt.json"
RA_TRANSACTIONS="$TMP/missing-transactions"
RA_SOCKET="$TMP/fake.sock"
RA_SOAK_MINUTES=60
ra_artifacts_init_new run-a
cat >"$RA_CHECKPOINT" <<JSON
{
  "schema_version":"podlaz.release-acceptance-checkpoint.v5",
  "run_id":"run-a",
  "run_started_at":"2026-09-03T07:00:00Z",
  "starting_boot_id":"01234567-89ab-cdef-0123-456789abcdef",
  "current_boot_id":"01234567-89ab-cdef-0123-456789abcdef",
  "phase":"running-pre-reboot",
  "current_scenario":"scenario-a",
  "last_failure":{"boot_id":"01234567-89ab-cdef-0123-456789abcdef"},
  "candidate":{"path":"/tmp/candidate.deb","package":"podlaz","version":"2.0","architecture":"amd64","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "mutations":{},
  "scenarios":{"scenario-a":{"state":"running","started_at":"2026-09-03T08:00:00Z"}},
  "private":{"artifact_root":"$RA_ARTIFACT_DIR","service_active_before":true,"run_config":{"soak_minutes":60},"resource":{}}
}
JSON
export RELEASE_ACCEPTANCE_TEST_DPKG_LOG="$TMP/dpkg.log"
cat >"$RELEASE_ACCEPTANCE_TEST_DPKG_LOG" <<'LOG'
2026-09-03 07:59:59 status installed podlaz:amd64 1.9
2026-09-03 08:00:01 upgrade podlaz:amd64 1.9 2.0
LOG

ra_failure_bundle_capture failure_a 9 || fail "first bundle capture failed"
failures="$RA_PRIVATE_DIR/failures"
A="$(find "$failures" -mindepth 1 -maxdepth 1 -type d -name '20*' -print | sort | head -n1)"
[[ -n "$A" ]] || fail "first immutable bundle missing"
A_ID="$(basename "$A")"
assert_eq "$(jq -r '.attempt_id' "$A/metadata.json")" "$A_ID"
assert_eq "$(jq -r '.root_failure_id' "$A/metadata.json")" "$A_ID"
assert_eq "$(jq -r '.previous_bundle_id' "$A/metadata.json")" "null"
assert_eq "$(jq -r '.components.journal.status' "$A/metadata.json")" captured
assert_eq "$(jq -r '.components.systemd_unit_properties.status' "$A/metadata.json")" captured
assert_eq "$(jq -r '.components.systemd_version.status' "$A/metadata.json")" captured
assert_eq "$(jq -r '.components.package_state.status' "$A/metadata.json")" captured
assert_eq "$(jq -r '.components.package_identity.status' "$A/metadata.json")" captured
assert_eq "$(jq -r '.components.dpkg_log.status' "$A/metadata.json")" captured
assert_eq "$(jq -r '.components.network_session.status' "$A/metadata.json")" unavailable
assert_eq "$(jq -r '.capture_status' "$A/metadata.json")" partial
assert_contains "$(cat "$A/journal.txt")" current-boot-only
if grep -Fq previous-boot "$A/journal.txt"; then fail "previous boot leaked into exact-boot journal"; fi
assert_contains "$(cat "$TEST_CAPTURE_DIR/journal-argv")" _BOOT_ID=0123456789abcdef0123456789abcdef
if grep -Eq '(^|[[:space:]])--boot([[:space:]]|$)' "$TEST_CAPTURE_DIR/journal-argv"; then fail "raw --boot was used"; fi
assert_contains "$(cat "$TEST_CAPTURE_DIR/journal-argv")" '--since 2026-09-03T08:00:00Z'
assert_contains "$(cat "$A/dpkg-log.txt")" '2026-09-03 08:00:01'
if grep -Fq '07:59:59' "$A/dpkg-log.txt"; then fail "dpkg evidence crossed run/scenario boundary"; fi

while IFS= read -r -d '' p; do
  if [[ -d "$p" ]]; then assert_eq "$(stat -Lc '%u:%g:%a' "$p")" "$RA_UID:$RA_GID:700"; else assert_eq "$(stat -Lc '%u:%g:%a' "$p")" "$RA_UID:$RA_GID:600"; fi
done < <(find "$RA_ARTIFACT_DIR/run-a" -mindepth 0 -print0)
ra_verify_run_tree || fail "run-tree verifier rejected secure evidence tree"

fingerprint() {
  local root="$1"
  find "$root" -mindepth 0 -print0 | sort -z | while IFS= read -r -d '' p; do
    if [[ -f "$p" && ! -L "$p" ]]; then printf '%s\t%s\t%s\n' "${p#$root/}" "$(stat -Lc '%u:%g:%a:%s' "$p")" "$(sha256sum "$p" | awk '{print $1}')"; else printf '%s\t%s\n' "${p#$root/}" "$(stat -Lc '%u:%g:%a' "$p")"; fi
  done
}
A_BEFORE="$(fingerprint "$A")"
TREE_BEFORE="$(fingerprint "$RA_ARTIFACT_DIR/run-a")"
ra_verify_run_tree || fail "run tree verify failed"
TREE_AFTER="$(fingerprint "$RA_ARTIFACT_DIR/run-a")"
assert_eq "$TREE_AFTER" "$TREE_BEFORE"

# The real clean abort captures recovered state as sibling B, leaves A byte-for-byte
# unchanged, finalizes the public ABORTED_CLEAN report and removes the active checkpoint.
ra_safe_cleanup() { return 0; }
export TEST_STATUS_CONNECTION=inactive
export TEST_EVIDENCE_MARKER=clean-b
jq '.phase="failed-cleanable"|.current_scenario=""' "$RA_CHECKPOINT" >"$RA_CHECKPOINT.next"
mv "$RA_CHECKPOINT.next" "$RA_CHECKPOINT"
out="$(ra_run_abort)" || fail "clean abort failed"
assert_contains "$out" ABORTED_CLEAN
[[ ! -e "$RA_CHECKPOINT" ]] || fail "clean abort retained checkpoint"
A_AFTER="$(fingerprint "$A")"
assert_eq "$A_AFTER" "$A_BEFORE"
B="$(find "$failures" -mindepth 1 -maxdepth 1 -type d -name '20*' -print | sort | tail -n1)"
[[ "$B" != "$A" ]] || fail "later abort overwrote first bundle"
assert_eq "$(jq -r '.previous_bundle_id' "$B/metadata.json")" "$A_ID"
assert_eq "$(jq -r '.root_failure_id' "$B/metadata.json")" "$A_ID"
assert_eq "$(jq -r '.invocation_mode' "$B/metadata.json")" abort
assert_contains "$(cat "$A/status.txt")" failure-a
assert_contains "$(cat "$B/status.txt")" clean-b
assert_eq "$(jq -r '.qualification' "$RA_PUBLIC_DIR/report.json")" ABORTED_CLEAN
if grep -R -E -q 'failure-a|clean-b|private-marker[.]example[.]invalid' "$RA_PUBLIC_DIR"; then fail "private evidence leaked into public output"; fi

# A command failure is a valid partial sibling bundle state, never "captured".
RA_ARTIFACT_DIR="$RA_HOME/journal-failure-artifacts"
ra_artifacts_init_new journal-failure-run
RA_CHECKPOINT="$RA_STATE_DIR/current.json"
cat >"$RA_CHECKPOINT" <<JSON
{"schema_version":"podlaz.release-acceptance-checkpoint.v5","run_id":"journal-failure-run","run_started_at":"2026-09-03T07:00:00Z","starting_boot_id":"01234567-89ab-cdef-0123-456789abcdef","current_boot_id":"01234567-89ab-cdef-0123-456789abcdef","phase":"running-pre-reboot","current_scenario":"scenario-a","last_failure":{"boot_id":"01234567-89ab-cdef-0123-456789abcdef"},"candidate":{"package":"podlaz","version":"2.0","architecture":"amd64","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"mutations":{},"scenarios":{"scenario-a":{"state":"running","started_at":"2026-09-03T08:00:00Z"}},"private":{"artifact_root":"$RA_ARTIFACT_DIR","service_active_before":true,"run_config":{"soak_minutes":60},"resource":{}}}
JSON
JOURNAL_FAIL=1; export JOURNAL_FAIL
ra_failure_bundle_capture failure_c 11 || fail "partial command-failure bundle was not finalized"
C="$(find "$RA_PRIVATE_DIR/failures" -mindepth 1 -maxdepth 1 -type d -name '20*' -print | sort | tail -n1)"
assert_eq "$(jq -r '.components.journal.status' "$C/metadata.json")" command_failed
assert_eq "$(jq -r '.capture_status' "$C/metadata.json")" partial
assert_contains "$(cat "$C/journal.txt")" 'rc=67'
if [[ "$(jq -r '.components.journal.status' "$C/metadata.json")" == captured ]]; then fail "failed journal masqueraded as captured"; fi
unset JOURNAL_FAIL

# Symlink and foreign/mode drift are rejected without repair or traversal.
RA_ARTIFACT_DIR="$RA_HOME/symlink-artifacts"
ra_artifacts_init_new run-symlink
mkdir "$TMP/escape"
ln -s "$TMP/escape" "$RA_PRIVATE_DIR/failures"
if ra_failure_bundle_capture symlink_attack 1 >/dev/null 2>&1; then fail "symlink failure tree was accepted"; fi
[[ -z "$(find "$TMP/escape" -mindepth 1 -print -quit)" ]] || fail "capture traversed symlink"
rm "$RA_PRIVATE_DIR/failures"
ra_artifact_dir_ensure "$RA_PRIVATE_DIR/failures"
chmod 0755 "$RA_PRIVATE_DIR/failures"
if ra_failure_bundle_capture foreign_mode 1 >/dev/null 2>&1; then fail "foreign/mode-drift failure tree was repaired"; fi
assert_eq "$(stat -Lc '%a' "$RA_PRIVATE_DIR/failures")" 755
chmod 0700 "$RA_PRIVATE_DIR/failures"
mkdir -m 0700 "$RA_PRIVATE_DIR/failures/not-an-attempt"
if ra_failure_bundle_capture foreign_sibling 1 >/dev/null 2>&1; then fail "unknown user-owned failure sibling was accepted"; fi
[[ -d "$RA_PRIVATE_DIR/failures/not-an-attempt" ]] || fail "foreign sibling was silently repaired/removed"

# A user-tree file whose owner does not match the recorded user is rejected read-only.
actual_uid="$(id -u)"; actual_gid="$(id -g)"; RA_UID=$((actual_uid+1)); RA_GID=$((actual_gid+1))
if ra_verify_run_tree >/dev/null 2>&1; then fail "foreign/root-style ownership mismatch was accepted"; fi
RA_UID="$actual_uid"; RA_GID="$actual_gid"

# Lifecycle outcomes use the same real bundle boundary, then cleanly retire the checkpoint.
prepare_lifecycle() {
  local run="$1"
  RA_ARTIFACT_DIR="$RA_HOME/$run-artifacts"
  ra_artifacts_init_new "$run"
  RA_CHECKPOINT="$RA_STATE_DIR/current.json"
  cat >"$RA_CHECKPOINT" <<JSON
{"schema_version":"podlaz.release-acceptance-checkpoint.v5","run_id":"$run","run_started_at":"2026-09-03T07:00:00Z","starting_boot_id":"01234567-89ab-cdef-0123-456789abcdef","current_boot_id":"01234567-89ab-cdef-0123-456789abcdef","phase":"running-pre-reboot","current_scenario":"scenario-a","last_failure":{"boot_id":"01234567-89ab-cdef-0123-456789abcdef"},"candidate":{"package":"podlaz","version":"2.0","architecture":"amd64","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"mutations":{},"scenarios":{"scenario-a":{"state":"running","started_at":"2026-09-03T08:00:00Z"}},"private":{"artifact_root":"$RA_ARTIFACT_DIR","service_active_before":true,"run_config":{"soak_minutes":60},"resource":{}}}
JSON
}
ra_safe_cleanup() { return 0; }

prepare_lifecycle failed-run
set +e
out="$(ra_failure_finalize explicit_failure 19)"
rc=$?
set -e
((rc != 0)) || fail "failure finalizer unexpectedly succeeded"
assert_contains "$out" FAILED_CLEAN
[[ ! -e "$RA_CHECKPOINT" ]] || fail "FAILED_CLEAN retained checkpoint"
assert_eq "$(jq -r '.qualification' "$RA_PUBLIC_DIR/report.json")" FAILED_CLEAN

prepare_lifecycle restart-run
ra_retire_existing_run RESTARTED_CLEAN || fail "clean restart retirement failed"
[[ ! -e "$RA_CHECKPOINT" ]] || fail "RESTARTED_CLEAN retained checkpoint"
assert_eq "$(jq -r '.qualification' "$RA_PUBLIC_DIR/report.json")" RESTARTED_CLEAN

if grep -R -Fq 'private-marker.example.invalid' "$RA_PUBLIC_DIR"; then fail "private evidence leaked into public output"; fi

printf 'standalone_release_evidence: PASS\n'
