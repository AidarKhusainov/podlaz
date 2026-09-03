#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
SCRIPT="$ROOT/scripts/acceptance/release-laptop.sh"

fail() { printf 'standalone_legacy_checkpoint_reconcile: %s\n' "$*" >&2; exit 1; }

export RELEASE_ACCEPTANCE_TEST_MODE=1
export SUDO_USER=tester
# shellcheck source=/dev/null
source "$SCRIPT"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

candidate_path="$TMP/candidate.deb"
previous_path="$TMP/previous.deb"
printf 'documentation-only candidate package fixture\n' >"$candidate_path"
printf 'documentation-only previous package fixture\n' >"$previous_path"
candidate_sha="$(sha256sum "$candidate_path" | awk '{print $1}')"
previous_sha="$(sha256sum "$previous_path" | awk '{print $1}')"
fixture_arch="$(dpkg --print-architecture)"

candidate_identity="$(jq -cn --arg path "$candidate_path" --arg arch "$fixture_arch" --arg sha "$candidate_sha" '{path:$path,package:"podlaz",version:"2.0.0",architecture:$arch,sha256:$sha}')"
previous_identity="$(jq -cn --arg path "$previous_path" --arg arch "$fixture_arch" --arg sha "$previous_sha" '{path:$path,package:"podlaz",version:"1.0.0",architecture:$arch,sha256:$sha}')"

fixture_installed_version="2.0.0"
install_count=0
inactive_ok=1
ordinary_ok=1
fresh_started=0

ra_pkg_inspect() {
  local path="$1" version sha
  case "$path" in
    "$candidate_path") version=2.0.0; sha="$(sha256sum "$path" | awk '{print $1}')" ;;
    "$previous_path") version=1.0.0; sha="$(sha256sum "$path" | awk '{print $1}')" ;;
    *) return 2 ;;
  esac
  jq -cn --arg path "$path" --arg version "$version" --arg arch "$fixture_arch" --arg sha "$sha" '{path:$path,package:"podlaz",version:$version,architecture:$arch,sha256:$sha,device:1,inode:1}'
}
ra_legacy_installed_package_identity() {
  [[ -n "$fixture_installed_version" ]] || return 1
  jq -cn --arg version "$fixture_installed_version" --arg arch "$fixture_arch" '{package:"podlaz",version:$version,architecture:$arch}'
}
ra_pkg_install_exact() {
  local identity="$1"
  ((install_count+=1))
  fixture_installed_version="$(jq -r '.version' <<<"$identity")"
}
ra_verify_inactive_boundary() { ((inactive_ok==1)); }
ra_verify_ordinary_network() { ((ordinary_ok==1)); }

reset_case() {
  local name="$1"
  export RELEASE_ACCEPTANCE_STATE_DIR="$TMP/$name/state"
  export RELEASE_ACCEPTANCE_TEST_HOME="$TMP/$name/home"
  export RELEASE_ACCEPTANCE_USER_STATE_HOME="$TMP/$name/home/.local/state"
  mkdir -p "$RELEASE_ACCEPTANCE_STATE_DIR" "$RELEASE_ACCEPTANCE_TEST_HOME"
  chmod 0700 "$RELEASE_ACCEPTANCE_STATE_DIR"
  RA_USER=tester
  RA_UID="$(id -u)"
  RA_GID="$(id -g)"
  RA_HOME="$RELEASE_ACCEPTANCE_TEST_HOME"
  RA_ARTIFACT_DIR=""
  ra_init_paths
  RA_CONTINUATION="$TMP/$name/network-session-continuation.json"
  RA_TRANSACTIONS="$TMP/$name/transactions"
  RA_BOOT_ATTEMPT="$TMP/$name/boot-autostart-attempt.json"
  RA_ROLLBACK_HOOK_DIR="$TMP/$name/rollback-hook"
  RA_ROLLBACK_OVERRIDE="$TMP/$name/rollback.conf"
  RA_TERMINAL_HOOK_DIR="$TMP/$name/terminal-hook"
  RA_TERMINAL_OVERRIDE="$TMP/$name/terminal.conf"
  unset RELEASE_ACCEPTANCE_TEST_CHECKPOINT_OWNER
  fixture_installed_version=2.0.0
  install_count=0
  inactive_ok=1
  ordinary_ok=1
  fresh_started=0
}

write_legacy() {
  local schema="$1" state="$2" candidate="${3:-$candidate_identity}" previous="${4:-$previous_identity}"
  jq -n --arg schema "$schema" --arg state "$state" --argjson candidate "$candidate" --argjson previous "$previous" '
    {schema_version:$schema,run_id:"legacy-documentation-fixture",phase:"failed-cleanable",candidate:$candidate,mutations:{package_setup:{state:$state,kind:"previous_package",identity:{previous:$previous,candidate:$candidate}}},scenarios:{},private:{}}' >"$RA_CHECKPOINT"
  chmod 0600 "$RA_CHECKPOINT"
}

assert_retired_with_archive() {
  [[ ! -e "$RA_CHECKPOINT" && ! -L "$RA_CHECKPOINT" ]] || fail "legacy checkpoint was not retired"
  local archive_dir="$RA_STATE_DIR/legacy-checkpoint-archive"
  [[ -d "$archive_dir" && ! -L "$archive_dir" ]] || fail "legacy checkpoint archive directory missing"
  local count
  count="$(find "$archive_dir" -maxdepth 1 -type f -name '*.json' | wc -l)"
  [[ "$count" == 1 ]] || fail "expected one archived legacy checkpoint, got $count"
}

assert_no_mutation() {
  local before="$1"
  [[ -f "$RA_CHECKPOINT" && ! -L "$RA_CHECKPOINT" ]] || fail "ambiguous checkpoint was mutated/removed"
  [[ "$(sha256sum "$RA_CHECKPOINT" | awk '{print $1}')" == "$before" ]] || fail "ambiguous checkpoint content changed"
  [[ "$install_count" == 0 ]] || fail "ambiguous checkpoint caused package mutation"
  [[ ! -e "$RA_STATE_DIR/legacy-checkpoint-archive" ]] || fail "ambiguous checkpoint was archived before proof completed"
}

# 1. v3/v4 releasing package-only boundary with exact candidate already installed retires safely.
for schema in podlaz.release-acceptance-checkpoint.v3 podlaz.release-acceptance-checkpoint.v4; do
  reset_case "releasing-${schema##*.}"
  write_legacy "$schema" releasing
  ra_run_abort >/dev/null || fail "$schema releasing boundary did not retire"
  [[ "$install_count" == 0 ]] || fail "$schema reinstalled an already-installed candidate"
  assert_retired_with_archive
done

# 2. v3 package setup still on the exact previous package restores the exact candidate once, then retires.
reset_case previous-installed
fixture_installed_version=1.0.0
write_legacy podlaz.release-acceptance-checkpoint.v3 acquired
ra_run_abort >/dev/null || fail "v3 previous-package boundary did not reconcile"
[[ "$install_count" == 1 && "$fixture_installed_version" == 2.0.0 ]] || fail "candidate was not restored exactly once"
assert_retired_with_archive

# 3. Candidate/previous artifact identity mismatch fails closed without any mutation.
reset_case identity-mismatch
bad_candidate="$(jq '.sha256="ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"' <<<"$candidate_identity")"
write_legacy podlaz.release-acceptance-checkpoint.v3 releasing "$bad_candidate" "$previous_identity"
before="$(sha256sum "$RA_CHECKPOINT" | awk '{print $1}')"
if ra_run_abort >/dev/null 2>&1; then fail "candidate identity mismatch was accepted"; fi
assert_no_mutation "$before"

reset_case previous-identity-mismatch
fixture_installed_version=1.0.0
bad_previous="$(jq '.sha256="eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"' <<<"$previous_identity")"
write_legacy podlaz.release-acceptance-checkpoint.v3 acquired "$candidate_identity" "$bad_previous"
before="$(sha256sum "$RA_CHECKPOINT" | awk '{print $1}')"
if ra_run_abort >/dev/null 2>&1; then fail "previous identity mismatch was accepted"; fi
assert_no_mutation "$before"

# 4. An installed package version outside the exact candidate/previous set is ambiguous.
reset_case unknown-installed
fixture_installed_version=9.9.9
write_legacy podlaz.release-acceptance-checkpoint.v3 releasing
before="$(sha256sum "$RA_CHECKPOINT" | awk '{print $1}')"
if ra_run_abort >/dev/null 2>&1; then fail "unknown installed package version was accepted"; fi
assert_no_mutation "$before"

# 5. Retained Network Session/transaction/other authority that v3/v4 cannot prove is legacy-ambiguous.
for retained in continuation transaction hook; do
  reset_case "retained-$retained"
  write_legacy podlaz.release-acceptance-checkpoint.v4 releasing
  case "$retained" in
    continuation) printf '{"schema_version":"podlaz.network-session-state.v1"}\n' >"$RA_CONTINUATION" ;;
    transaction) mkdir -p "$RA_TRANSACTIONS"; printf '{"owner":"podlaz","state":"committed"}\n' >"$RA_TRANSACTIONS/fixture.json" ;;
    hook) mkdir -p "$RA_ROLLBACK_HOOK_DIR" ;;
  esac
  before="$(sha256sum "$RA_CHECKPOINT" | awk '{print $1}')"
  if ra_run_abort >/dev/null 2>&1; then fail "retained $retained authority was accepted"; fi
  assert_no_mutation "$before"
done

reset_case unsupported-authority-shape
jq -n --arg schema podlaz.release-acceptance-checkpoint.v4 --argjson candidate "$candidate_identity" '{schema_version:$schema,candidate:$candidate,mutations:{fixture_a:{state:"acquired",kind:"network_fixture",identity:{}}},scenarios:{},private:{}}' >"$RA_CHECKPOINT"
chmod 0600 "$RA_CHECKPOINT"
before="$(sha256sum "$RA_CHECKPOINT" | awk '{print $1}')"
if ra_run_abort >/dev/null 2>&1; then fail "unsupported legacy mutation authority was accepted"; fi
assert_no_mutation "$before"

# Clean-boundary proof is mandatory before any package mutation.
reset_case inactive-proof-required
fixture_installed_version=1.0.0
inactive_ok=0
write_legacy podlaz.release-acceptance-checkpoint.v3 acquired
before="$(sha256sum "$RA_CHECKPOINT" | awk '{print $1}')"
if ra_run_abort >/dev/null 2>&1; then fail "missing product inactivity proof was accepted"; fi
assert_no_mutation "$before"

reset_case ordinary-network-proof-required
fixture_installed_version=1.0.0
ordinary_ok=0
write_legacy podlaz.release-acceptance-checkpoint.v3 acquired
before="$(sha256sum "$RA_CHECKPOINT" | awk '{print $1}')"
if ra_run_abort >/dev/null 2>&1; then fail "missing ordinary-network proof was accepted"; fi
assert_no_mutation "$before"

# 6. Unknown/future/malformed schemas never mutate the checkpoint or package state.
for schema in podlaz.release-acceptance-checkpoint.v2 podlaz.release-acceptance-checkpoint.v6 vendor.future.v1; do
  reset_case "unknown-${schema//[^A-Za-z0-9]/-}"
  write_legacy "$schema" releasing
  before="$(sha256sum "$RA_CHECKPOINT" | awk '{print $1}')"
  if ra_run_abort >/dev/null 2>&1; then fail "unknown schema $schema was accepted"; fi
  assert_no_mutation "$before"
done
reset_case malformed
printf '{not-json\n' >"$RA_CHECKPOINT"
chmod 0600 "$RA_CHECKPOINT"
before="$(sha256sum "$RA_CHECKPOINT" | awk '{print $1}')"
if ra_run_abort >/dev/null 2>&1; then fail "malformed checkpoint was accepted"; fi
assert_no_mutation "$before"

# 7. Symlink/wrong-owner/wrong-mode checkpoints are rejected before parsing.
reset_case symlink
printf '{not-json\n' >"$TMP/symlink-target"
ln -s "$TMP/symlink-target" "$RA_CHECKPOINT"
if ra_run_abort >/dev/null 2>&1; then fail "symlink checkpoint was accepted"; fi
[[ -L "$RA_CHECKPOINT" && -f "$TMP/symlink-target" ]] || fail "symlink rejection mutated the target"

reset_case wrong-mode
write_legacy podlaz.release-acceptance-checkpoint.v3 releasing
chmod 0644 "$RA_CHECKPOINT"
before="$(sha256sum "$RA_CHECKPOINT" | awk '{print $1}')"
if ra_run_abort >/dev/null 2>&1; then fail "wrong-mode checkpoint was accepted"; fi
[[ "$(sha256sum "$RA_CHECKPOINT" | awk '{print $1}')" == "$before" ]] || fail "wrong-mode checkpoint was mutated"

reset_case wrong-owner
write_legacy podlaz.release-acceptance-checkpoint.v3 releasing
export RELEASE_ACCEPTANCE_TEST_CHECKPOINT_OWNER="$(( $(id -u) + 1 )):$(( $(id -g) + 1 ))"
before="$(sha256sum "$RA_CHECKPOINT" | awk '{print $1}')"
if ra_run_abort >/dev/null 2>&1; then fail "wrong-owner checkpoint was accepted"; fi
[[ "$(sha256sum "$RA_CHECKPOINT" | awk '{print $1}')" == "$before" ]] || fail "wrong-owner checkpoint was mutated"
unset RELEASE_ACCEPTANCE_TEST_CHECKPOINT_OWNER

reset_case oversized
python_size=$((1024 * 1024 + 1))
head -c "$python_size" /dev/zero | tr '\0' x >"$RA_CHECKPOINT"
chmod 0600 "$RA_CHECKPOINT"
before="$(sha256sum "$RA_CHECKPOINT" | awk '{print $1}')"
if ra_run_abort >/dev/null 2>&1; then fail "oversized checkpoint was accepted"; fi
[[ "$(sha256sum "$RA_CHECKPOINT" | awk '{print $1}')" == "$before" ]] || fail "oversized checkpoint was mutated"

# 8. Retirement unblocks a fresh v5 run through the normal new-run controller path.
reset_case fresh-v5
write_legacy podlaz.release-acceptance-checkpoint.v3 releasing
ra_run_new_fresh() {
  fresh_started=1
  jq -n --arg schema "$RA_SCHEMA" '{schema_version:$schema,mutations:{},scenarios:{},run_started_at:"2026-01-01T00:00:00Z",private:{service_active_before:true}}' >"$RA_CHECKPOINT"
  chmod 0600 "$RA_CHECKPOINT"
}
ra_run_new >/dev/null || fail "fresh run did not start after supported legacy retirement"
[[ "$fresh_started" == 1 ]] || fail "fresh current-schema path was not reached"
jq -e --arg schema "$RA_SCHEMA" '.schema_version==$schema' "$RA_CHECKPOINT" >/dev/null || fail "fresh run did not create current v5 state"

printf 'standalone_legacy_checkpoint_reconcile: PASS\n'
