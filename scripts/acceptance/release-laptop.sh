#!/usr/bin/env bash
set -Euo pipefail

RA_SCHEMA="podlaz.release-acceptance-checkpoint.v4"
RA_SERVICE="podlazd.service"
RA_SOCKET="/run/podlaz/podlazd.sock"
RA_CONTINUATION="/run/podlaz/network-session-continuation.json"
RA_TRANSACTIONS="/run/podlaz/transactions"
RA_BOOT_ATTEMPT="/run/podlaz/boot-autostart-attempt.json"
RA_BOOT_MANIFEST="/var/lib/podlaz/boot-autostart-manifest.json"
RA_STATE_DIR="/var/lib/podlaz-release-acceptance"
RA_CHECKPOINT=""
RA_LOCK_FILE=""
RA_TERMINAL_URI='vless://00000000-0000-4000-8000-000000000001@vpn.invalid:443?security=tls&type=tcp&sni=vpn.invalid#ReleaseAcceptanceFailure'
RA_TERMINAL_NAME="ReleaseAcceptanceFailure"
RA_PROBE_URL="https://example.com/"
RA_ROLLBACK_HOOK_DIR="/run/podlaz/release-acceptance-rollback"
RA_ROLLBACK_OVERRIDE="/etc/systemd/system/podlazd.service.d/99-release-acceptance-rollback.conf"
RA_TERMINAL_HOOK_DIR="/run/podlaz/release-acceptance-terminal"
RA_TERMINAL_OVERRIDE="/etc/systemd/system/podlazd.service.d/99-release-acceptance-terminal.conf"
RA_CAPTURE=""
RA_CAPTURE_RC=0
RA_MODE="new"
RA_CANDIDATE=""
RA_PREVIOUS_DEB=""
RA_PROFILE=""
RA_ARTIFACT_DIR=""
RA_ARTIFACT_DIR_EXPLICIT=0
RA_SOAK_MINUTES=60
RA_ALLOW_WIFI=1
RA_ALLOW_SUSPEND=1
RA_REBOOT_PHASES=1
RA_USER=""
RA_UID=""
RA_GID=""
RA_HOME=""
RA_USER_STATE_HOME=""
RA_PRIVATE_DIR=""
RA_PUBLIC_DIR=""
RA_TRANSCRIPT=""
RA_LOCK_FD=9
RA_PRIVACY_WATCH_PID=""
RA_PRIVACY_WATCH_STOP=""
RA_PRIVACY_WATCH_FAIL=""
RA_BACKGROUND_PID=""
RA_FINALIZER_ACTIVE=0
RA_CURRENT_SCENARIO=""
RA_RC_PAUSED=20

ra_usage() {
  cat <<'USAGE'
Usage:
  sudo ./release-laptop.sh CANDIDATE.deb [options]
  sudo ./release-laptop.sh CANDIDATE.deb --restart [options]
  sudo ./release-laptop.sh --resume
  sudo ./release-laptop.sh --abort

Options:
  --previous-deb PATH       Exact strictly-lower Podlaz .deb when a lower release is not already installed
  --profile ID              Existing TUN-capable profile id; auto-select only when exactly one is usable
  --artifact-dir PATH       Evidence root inside the original user's home/state tree
  --soak-minutes N          Active soak duration, 1..1440; any value other than 60 caps result at PARTIAL_PASS
  --skip-wifi-reconnect     Skip controlled NetworkManager reconnect; caps result at PARTIAL_PASS
  --skip-suspend            Skip bounded rtcwake suspend/resume; caps result at PARTIAL_PASS
  --no-reboot-phases        Skip three real reboot phases; caps result at PARTIAL_PASS
  --restart                 Exact safe cleanup of an existing run, then start a new run
  --resume                  Resume one persisted supported boundary
  --abort                   Restore exact harness-owned state and abandon the run
  -h, --help                Show this help

This is one standalone Bash file. It does not require a source checkout or Python.
It never builds Podlaz, downloads packages, uses apt/apt-get, automatically reboots,
or broadly flushes route/rule/nftables state.
USAGE
}

ra_err() { printf 'release-laptop: %s\n' "$*" >&2; }
ra_die() { ra_err "$*"; return 1; }
ra_preflight_die() { ra_err "$*"; return 2; }

ra_cli_parse() {
  local positional=()
  while (($#)); do
    case "$1" in
      --resume)
        [[ "$RA_MODE" == new ]] || { ra_err "run modes are mutually exclusive"; return 2; }
        RA_MODE=resume
        ;;
      --abort)
        [[ "$RA_MODE" == new ]] || { ra_err "run modes are mutually exclusive"; return 2; }
        RA_MODE=abort
        ;;
      --restart)
        [[ "$RA_MODE" == new ]] || { ra_err "run modes are mutually exclusive"; return 2; }
        RA_MODE=restart
        ;;
      --previous-deb)
        shift
        (($#)) || { ra_err "--previous-deb requires a path"; return 2; }
        RA_PREVIOUS_DEB="$1"
        ;;
      --profile)
        shift
        (($#)) || { ra_err "--profile requires an id"; return 2; }
        RA_PROFILE="$1"
        ;;
      --artifact-dir)
        shift
        (($#)) || { ra_err "--artifact-dir requires a path"; return 2; }
        RA_ARTIFACT_DIR="$1"
        RA_ARTIFACT_DIR_EXPLICIT=1
        ;;
      --soak-minutes)
        shift
        (($#)) || { ra_err "--soak-minutes requires an integer"; return 2; }
        RA_SOAK_MINUTES="$1"
        ;;
      --skip-wifi-reconnect) RA_ALLOW_WIFI=0 ;;
      --skip-suspend) RA_ALLOW_SUSPEND=0 ;;
      --no-reboot-phases) RA_REBOOT_PHASES=0 ;;
      -h|--help) ra_usage; return 64 ;;
      --*) ra_err "unknown option: $1"; return 2 ;;
      *) positional+=("$1") ;;
    esac
    shift
  done

  if [[ "$RA_MODE" == resume || "$RA_MODE" == abort ]]; then
    if ((${#positional[@]})) || [[ -n "$RA_PREVIOUS_DEB$RA_PROFILE$RA_ARTIFACT_DIR" ]] || [[ "$RA_SOAK_MINUTES" != 60 ]] || ((RA_ALLOW_WIFI == 0 || RA_ALLOW_SUSPEND == 0 || RA_REBOOT_PHASES == 0)); then
      ra_err "--resume/--abort do not accept new-run inputs"
      return 2
    fi
    return 0
  fi

  ((${#positional[@]} == 1)) || { ra_err "candidate .deb is required for a new/restart run"; return 2; }
  RA_CANDIDATE="${positional[0]}"
  [[ "$RA_SOAK_MINUTES" =~ ^[0-9]+$ ]] || { ra_err "--soak-minutes must be an integer"; return 2; }
  ((RA_SOAK_MINUTES >= 1 && RA_SOAK_MINUTES <= 1440)) || { ra_err "--soak-minutes must be between 1 and 1440"; return 2; }
}

ra_require_root_and_user() {
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" == 1 ]]; then
    RA_USER="${SUDO_USER:-tester}"
    RA_UID="${RELEASE_ACCEPTANCE_TEST_UID:-$(id -u)}"
    RA_GID="${RELEASE_ACCEPTANCE_TEST_GID:-$(id -g)}"
    RA_HOME="${RELEASE_ACCEPTANCE_TEST_HOME:-${HOME:-/tmp}}"
    return 0
  fi
  ((EUID == 0)) || { ra_preflight_die "must be run with sudo/root"; return 2; }
  RA_USER="${SUDO_USER:-}"
  [[ -n "$RA_USER" && "$RA_USER" != root ]] || { ra_preflight_die "SUDO_USER must identify the original non-root user"; return 2; }
  local line
  if ! line="$(getent passwd "$RA_USER")"; then
    ra_preflight_die "SUDO_USER does not resolve through the account database"
    return 2
  fi
  IFS=: read -r _ _ RA_UID RA_GID _ RA_HOME _ <<<"$line"
  [[ "$RA_UID" =~ ^[0-9]+$ && "$RA_UID" != 0 && -d "$RA_HOME" ]] || { ra_preflight_die "invalid original user boundary"; return 2; }
}

ra_init_paths() {
  RA_USER_STATE_HOME="${RELEASE_ACCEPTANCE_USER_STATE_HOME:-$RA_HOME/.local/state}"
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" == 1 ]]; then
    RA_STATE_DIR="${RELEASE_ACCEPTANCE_STATE_DIR:-/var/lib/podlaz-release-acceptance}"
  else
    RA_STATE_DIR="/var/lib/podlaz-release-acceptance"
  fi
  RA_CHECKPOINT="$RA_STATE_DIR/current.json"
  RA_LOCK_FILE="$RA_STATE_DIR/lock"
  [[ -n "$RA_ARTIFACT_DIR" ]] || RA_ARTIFACT_DIR="$RA_USER_STATE_HOME/podlaz/release-acceptance/artifacts"
}

ra_require_tools() {
  local required=(bash jq flock uname dpkg dpkg-deb dpkg-query sha256sum stat systemctl journalctl curl ip nft resolvectl getent runuser base64 awk sed grep find mktemp ps kill sleep date readlink dirname basename head tail cat mv chmod chown mkdir rm rmdir sync wc cut tr timeout)
  local tool
  for tool in "${required[@]}"; do
    command -v "$tool" >/dev/null 2>&1 || { ra_preflight_die "required host tool is missing: $tool"; return 2; }
  done
}

ra_state_dir_prepare() {
  mkdir -p -- "$RA_STATE_DIR" || return 1
  chmod 0700 -- "$RA_STATE_DIR" || return 1
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]]; then chown root:root -- "$RA_STATE_DIR" || return 1; fi
  : >"$RA_LOCK_FILE" || return 1
  chmod 0600 "$RA_LOCK_FILE" || return 1
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]]; then chown root:root "$RA_LOCK_FILE" || return 1; fi
}

ra_lock_acquire() {
  ra_state_dir_prepare || return 1
  exec 9>"$RA_LOCK_FILE"
  flock -n 9 || { ra_die "another release acceptance operation is already running"; return 1; }
}

ra_validate_artifact_root() {
  local requested="$1" resolved allowed component cur rest
  [[ -n "$requested" && "$requested" == /* ]] || { ra_preflight_die "--artifact-dir must be an absolute path"; return 2; }
  resolved="$(readlink -m -- "$requested")" || return 2
  allowed="$(readlink -m -- "$RA_HOME")" || return 2
  case "$resolved" in
    "$allowed"|"$allowed"/*) ;;
    *) ra_preflight_die "--artifact-dir must stay inside the original user's home"; return 2 ;;
  esac
  cur="/"
  rest="${resolved#/}"
  IFS=/ read -r -a parts <<<"$rest"
  for component in "${parts[@]}"; do
    [[ -n "$component" ]] || continue
    cur="${cur%/}/$component"
    if [[ -e "$cur" || -L "$cur" ]]; then
      [[ ! -L "$cur" && -d "$cur" ]] || { ra_preflight_die "artifact path contains a symlink/non-directory component: $cur"; return 2; }
    fi
  done
  RA_ARTIFACT_DIR="$resolved"
}

ra_secure_user_mkdir() {
  local path="$1"
  [[ ! -e "$path" || ( -d "$path" && ! -L "$path" ) ]] || { ra_die "refuse unsafe artifact directory: $path"; return 1; }
  mkdir -p -- "$path" || return 1
  chmod 0700 "$path" || return 1
  if ((EUID == 0)) && [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]]; then chown "$RA_UID:$RA_GID" "$path" || return 1; fi
}

ra_artifacts_init_new() {
  local run_id="$1"
  ra_validate_artifact_root "$RA_ARTIFACT_DIR" || return $?
  RA_PRIVATE_DIR="$RA_ARTIFACT_DIR/$run_id/private"
  RA_PUBLIC_DIR="$RA_ARTIFACT_DIR/$run_id/public"
  RA_TRANSCRIPT="$RA_PRIVATE_DIR/commands.log"
  ra_secure_user_mkdir "$RA_ARTIFACT_DIR" || return 1
  ra_secure_user_mkdir "$RA_ARTIFACT_DIR/$run_id" || return 1
  ra_secure_user_mkdir "$RA_PRIVATE_DIR" || return 1
  ra_secure_user_mkdir "$RA_PUBLIC_DIR" || return 1
  : >"$RA_TRANSCRIPT"
  chmod 0600 "$RA_TRANSCRIPT" || return 1
  if ((EUID == 0)) && [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]]; then chown "$RA_UID:$RA_GID" "$RA_TRANSCRIPT" || return 1; fi
}

ra_artifacts_from_state() {
  local root run_id
  root="$(jq -er '.private.artifact_root' "$RA_CHECKPOINT")" || return 1
  run_id="$(jq -er '.run_id' "$RA_CHECKPOINT")" || return 1
  ra_validate_artifact_root "$root" || return 1
  RA_ARTIFACT_DIR="$root"
  RA_PRIVATE_DIR="$root/$run_id/private"
  RA_PUBLIC_DIR="$root/$run_id/public"
  RA_TRANSCRIPT="$RA_PRIVATE_DIR/commands.log"
  [[ -d "$RA_PRIVATE_DIR" && ! -L "$RA_PRIVATE_DIR" ]] || { ra_die "private evidence directory identity is invalid"; return 1; }
}

ra_log_command() {
  local rc="$1"
  shift
  [[ -n "$RA_TRANSCRIPT" ]] || return 0
  {
    printf '[%s] rc=%s argv=' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$rc"
    printf '%q ' "$@"
    printf '\n'
    [[ -z "$RA_CAPTURE" ]] || printf '%s\n' "$RA_CAPTURE"
  } >>"$RA_TRANSCRIPT"
}

ra_capture() {
  local out rc
  if out="$("$@" 2>&1)"; then rc=0; else rc=$?; fi
  RA_CAPTURE="$out"
  RA_CAPTURE_RC="$rc"
  ra_log_command "$rc" "$@"
  return "$rc"
}

ra_capture_user() {
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" == 1 ]]; then
    ra_capture "$@"
    return $?
  fi
  local out rc
  if out="$(runuser -u "$RA_USER" -- env HOME="$RA_HOME" XDG_STATE_HOME="$RA_USER_STATE_HOME" "$@" 2>&1)"; then rc=0; else rc=$?; fi
  RA_CAPTURE="$out"
  RA_CAPTURE_RC="$rc"
  ra_log_command "$rc" runuser -u "$RA_USER" -- "$@"
  return "$rc"
}

ra_checkpoint_exists() {
  [[ -e "$RA_CHECKPOINT" ]] || return 1
  [[ -f "$RA_CHECKPOINT" && ! -L "$RA_CHECKPOINT" ]] || { ra_die "checkpoint is not a regular file"; return 1; }
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]]; then
    [[ "$(stat -Lc '%u:%g:%a' "$RA_CHECKPOINT")" == "0:0:600" ]] || { ra_die "checkpoint ownership/mode is invalid"; return 1; }
  fi
}

ra_state_replace_text() {
  local payload="$1" tmp
  ra_state_dir_prepare || return 1
  tmp="$(mktemp "$RA_STATE_DIR/.current.json.XXXXXX")" || return 1
  printf '%s\n' "$payload" >"$tmp" || { rm -f "$tmp"; return 1; }
  chmod 0600 "$tmp" || { rm -f "$tmp"; return 1; }
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]]; then chown root:root "$tmp" || { rm -f "$tmp"; return 1; }; fi
  jq -e . "$tmp" >/dev/null || { rm -f "$tmp"; return 1; }
  sync -f "$tmp" 2>/dev/null || true
  mv -f "$tmp" "$RA_CHECKPOINT" || return 1
  sync -f "$RA_STATE_DIR" 2>/dev/null || true
}

ra_state_jq() {
  local filter="$1" payload
  shift
  payload="$(jq "$filter" "$@" "$RA_CHECKPOINT")" || return 1
  ra_state_replace_text "$payload"
}

ra_state_remove() {
  [[ -e "$RA_CHECKPOINT" ]] || return 0
  ra_checkpoint_exists || return 1
  rm -f "$RA_CHECKPOINT" || return 1
  sync -f "$RA_STATE_DIR" 2>/dev/null || true
}

ra_boot_id() {
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" == 1 && -n "${RELEASE_ACCEPTANCE_TEST_BOOT_ID:-}" ]]; then
    printf '%s' "$RELEASE_ACCEPTANCE_TEST_BOOT_ID"
    return 0
  fi
  cat /proc/sys/kernel/random/boot_id
}

ra_state_init() {
  local run_id="$1" candidate_json="$2" installed_before="$3" manifest_json="$4" profile="$5" boot started payload
  boot="$(ra_boot_id)" || return 1
  started="$(date -u +%Y-%m-%dT%H:%M:%SZ)" || return 1
  payload="$(jq -n \
    --arg schema "$RA_SCHEMA" \
    --arg run_id "$run_id" \
    --arg phase preparing-lower-release \
    --arg started "$started" \
    --arg user "$RA_USER" \
    --argjson uid "$RA_UID" \
    --argjson gid "$RA_GID" \
    --arg home "$RA_HOME" \
    --argjson candidate "$candidate_json" \
    --arg boot "$boot" \
    --arg artifact "$RA_ARTIFACT_DIR" \
    --arg previous_deb "$RA_PREVIOUS_DEB" \
    --arg profile "$profile" \
    --argjson soak "$RA_SOAK_MINUTES" \
    --argjson wifi "$RA_ALLOW_WIFI" \
    --argjson suspend "$RA_ALLOW_SUSPEND" \
    --argjson reboots "$RA_REBOOT_PHASES" \
    --arg installed "$installed_before" \
    --argjson manifest "$manifest_json" \
    '{schema_version:$schema,run_id:$run_id,run_started_at:$started,starting_boot_id:$boot,previous_boot_id:$boot,phase:$phase,current_scenario:"",last_failure:null,user:{name:$user,uid:$uid,gid:$gid,home:$home},candidate:$candidate,mutations:{},scenarios:{},private:{artifact_root:$artifact,installed_before:$installed,boot_manifest:$manifest,run_config:{previous_deb:$previous_deb,profile:$profile,soak_minutes:$soak,allow_wifi_reconnect:($wifi==1),allow_suspend:($suspend==1),reboot_phases:($reboots==1)}}}')" || return 1
  ra_state_replace_text "$payload"
}

ra_state_require_schema() {
  jq -e --arg s "$RA_SCHEMA" '.schema_version==$s and (.mutations|type)=="object" and (.scenarios|type)=="object" and (.run_started_at|type)=="string"' "$RA_CHECKPOINT" >/dev/null || { ra_die "unsupported or corrupt acceptance checkpoint"; return 1; }
}

ra_set_phase() { ra_state_jq '.phase=$phase' --arg phase "$1"; }

ra_record() {
  local name="$1" outcome="$2" reason="${3:-}" evidence="${4:-{}}"
  ra_state_jq '.scenarios[$name]=((.scenarios[$name]//{})+{name:$name,outcome:$outcome,reason:$reason,evidence:$evidence})' \
    --arg name "$name" --arg outcome "$outcome" --arg reason "$reason" --argjson evidence "$evidence"
}

ra_scenario_set_state() {
  local name="$1" state="$2"
  case "$state" in pending|prepared|running|verifying|passed|failed) ;; *) ra_die "invalid scenario state: $state"; return 1 ;; esac
  ra_state_jq '.scenarios[$name]=((.scenarios[$name]//{name:$name})+{state:$state})|.current_scenario=$name' --arg name "$name" --arg state "$state"
}

ra_scenario_clear_current() { ra_state_jq '.current_scenario=""'; }

ra_mut_begin_acquire() {
  local name="$1" kind="$2" identity="$3" existing
  existing="$(jq -r --arg n "$name" '.mutations[$n].state//"released"' "$RA_CHECKPOINT")" || return 1
  [[ "$existing" == released ]] || { ra_die "mutation $name already owns authority"; return 1; }
  ra_state_jq '.mutations[$name]={state:"acquiring",kind:$kind,scenario:$scenario,identity:$identity}' \
    --arg name "$name" --arg kind "$kind" --arg scenario "$RA_CURRENT_SCENARIO" --argjson identity "$identity"
}

ra_mut_transition() {
  local name="$1" expected="$2" target="$3" state
  state="$(jq -er --arg n "$name" '.mutations[$n].state' "$RA_CHECKPOINT")" || return 1
  [[ "$state" == "$expected" ]] || { ra_die "mutation $name: expected $expected, got $state"; return 1; }
  ra_state_jq '.mutations[$name].state=$target' --arg name "$name" --arg target "$target"
}
ra_mut_mark_acquired() { ra_mut_transition "$1" acquiring acquired; }
ra_mut_begin_release() { ra_mut_transition "$1" acquired releasing; }
ra_mut_mark_released() {
  local state
  state="$(jq -er --arg n "$1" '.mutations[$n].state' "$RA_CHECKPOINT")" || return 1
  [[ "$state" == acquiring || "$state" == releasing ]] || { ra_die "mutation $1 cannot become released from $state"; return 1; }
  ra_state_jq '.mutations[$name].state="released"' --arg name "$1"
}

ra_pkg_sha() { sha256sum -- "$1" | awk '{print $1}'; }
ra_pkg_field() { ra_capture dpkg-deb --field "$1" "$2" || return 1; [[ -n "$RA_CAPTURE" ]] || return 1; printf '%s' "$RA_CAPTURE"; }

ra_pkg_verify_sibling_checksum() {
  local path="$1" digest="$2" sums result count expected
  sums="$(dirname "$path")/SHA256SUMS"
  [[ -e "$sums" ]] || return 0
  [[ -f "$sums" && ! -L "$sums" ]] || { ra_preflight_die "sibling SHA256SUMS is not a regular file"; return 2; }
  result="$(awk -v b="$(basename "$path")" '$2==b || $2=="*"b {c++;v=$1} END{print c+0":"v}' "$sums")" || return 2
  count="${result%%:*}"
  expected="${result#*:}"
  ((count==0)) || [[ "$count" == 1 && "$expected" == "$digest" ]] || { ra_preflight_die "SHA256SUMS record does not match supplied package"; return 2; }
}

ra_pkg_inspect() {
  local path="$1" abs package version arch native digest dev inode
  [[ -f "$path" && ! -L "$path" ]] || { ra_preflight_die "package must be a regular non-symlink file: $path"; return 2; }
  abs="$(readlink -f -- "$path")" || return 2
  package="$(ra_pkg_field "$abs" Package)" || return 2
  version="$(ra_pkg_field "$abs" Version)" || return 2
  arch="$(ra_pkg_field "$abs" Architecture)" || return 2
  [[ "$package" == podlaz ]] || { ra_preflight_die "unexpected package name: $package"; return 2; }
  ra_capture dpkg --print-architecture || return 2
  native="$RA_CAPTURE"
  [[ "$arch" == "$native" ]] || { ra_preflight_die "package architecture $arch does not match host $native"; return 2; }
  digest="$(ra_pkg_sha "$abs")" || return 2
  ra_pkg_verify_sibling_checksum "$abs" "$digest" || return $?
  dev="$(stat -Lc '%d' "$abs")" || return 2
  inode="$(stat -Lc '%i' "$abs")" || return 2
  jq -cn --arg path "$abs" --arg package "$package" --arg version "$version" --arg architecture "$arch" --arg sha256 "$digest" --argjson device "$dev" --argjson inode "$inode" '{path:$path,package:$package,version:$version,architecture:$architecture,sha256:$sha256,device:$device,inode:$inode}'
}

ra_pkg_assert_identity() {
  local identity="$1" path dev inode digest
  path="$(jq -r '.path' <<<"$identity")" || return 1
  [[ -f "$path" && ! -L "$path" ]] || { ra_die "supplied package disappeared or changed type"; return 1; }
  dev="$(stat -Lc '%d' "$path")" || return 1
  inode="$(stat -Lc '%i' "$path")" || return 1
  digest="$(ra_pkg_sha "$path")" || return 1
  jq -e --argjson d "$dev" --argjson i "$inode" --arg s "$digest" '.device==$d and .inode==$i and .sha256==$s' <<<"$identity" >/dev/null || { ra_die "supplied package identity changed after preflight"; return 1; }
}

ra_pkg_installed_version() {
  local status version
  if ! ra_capture dpkg-query -W '-f=${Status}\t${Version}\n' podlaz; then
    if [[ "$RA_CAPTURE_RC" == 1 ]]; then printf ''; return 0; fi
    ra_die "cannot classify Podlaz package database state"
    return 1
  fi
  IFS=$'\t' read -r status version <<<"$RA_CAPTURE"
  [[ "$status" == "install ok installed" && -n "$version" ]] || { ra_die "podlaz package database state is not conclusively installed"; return 1; }
  printf '%s' "$version"
}

ra_pkg_install_exact() {
  local identity="$1" path version installed
  ra_pkg_assert_identity "$identity" || return 1
  path="$(jq -r '.path' <<<"$identity")" || return 1
  version="$(jq -r '.version' <<<"$identity")" || return 1
  ra_capture dpkg -i "$path" || { ra_die "dpkg -i supplied Podlaz package failed"; return 1; }
  installed="$(ra_pkg_installed_version)" || return 1
  [[ "$installed" == "$version" ]] || { ra_die "installed package version does not match supplied package"; return 1; }
}
ra_pkg_lt() { dpkg --compare-versions "$1" lt "$2"; }
ra_pkg_gt() { dpkg --compare-versions "$1" gt "$2"; }

ra_preflight_release_boundary() {
  local candidate="$1" previous="$2" installed="$3" cv pv
  cv="$(jq -er '.version' <<<"$candidate")" || return 2
  if [[ -n "$installed" ]] && ra_pkg_gt "$installed" "$cv"; then
    ra_preflight_die "installed Podlaz version is newer than candidate; refusing downgrade"
    return 2
  fi
  if [[ -n "$previous" ]]; then
    pv="$(jq -er '.version' <<<"$previous")" || return 2
    ra_pkg_lt "$pv" "$cv" || { ra_preflight_die "--previous-deb is not strictly lower than candidate"; return 2; }
  fi
  if [[ -n "$installed" && "$installed" != "$cv" ]] && ra_pkg_lt "$installed" "$cv"; then return 0; fi
  [[ -n "$previous" ]] || { ra_preflight_die "full lower-release qualification requires an installed lower release or --previous-deb"; return 2; }
}

ra_product() {
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" == 1 ]]; then ra_capture podlaz "$@"; else ra_capture_user /usr/bin/podlaz "$@"; fi
}
ra_profile_ids_json() { ra_product profile list --json || return 1; jq -ce 'select(.schema_version=="v1")|[.profiles[]?|select(type=="object" and .id)|.id]' <<<"$RA_CAPTURE"; }
ra_profile_validate() { ra_product profile validate "$1" --mode tun --json || return 1; jq -e '.schema_version=="v1" and .valid==true' <<<"$RA_CAPTURE" >/dev/null; }

ra_profile_select() {
  local explicit="$1" ids id
  local valid=()
  ids="$(ra_profile_ids_json)" || return 1
  if [[ -n "$explicit" ]]; then
    jq -e --arg id "$explicit" 'index($id)!=null' <<<"$ids" >/dev/null || return 1
    ra_profile_validate "$explicit" || return 1
    printf '%s' "$explicit"
    return 0
  fi
  while IFS= read -r id; do
    if [[ -n "$id" ]] && ra_profile_validate "$id"; then valid+=("$id"); fi
  done < <(jq -r '.[]' <<<"$ids")
  ((${#valid[@]}==1)) || { ra_die "expected exactly one usable TUN profile, found ${#valid[@]}"; return 1; }
  printf '%s' "${valid[0]}"
}

ra_connect() { ra_product connect --mode tun "$1" >/dev/null || { ra_die "Podlaz TUN connect failed"; return 1; }; }
ra_disconnect() { ra_product disconnect >/dev/null || { ra_die "Podlaz disconnect failed"; return 1; }; }

ra_status_json() {
  local payload
  ra_capture curl --fail --silent --max-time 5 --unix-socket "$RA_SOCKET" http://localhost/v1/status || return 1
  payload="$(jq -ce . <<<"$RA_CAPTURE")" || return 1
  if [[ -n "$RA_PRIVATE_DIR" && -d "$RA_PRIVATE_DIR" ]]; then
    printf '%s\n' "$payload" >"$RA_PRIVATE_DIR/last-status.json" 2>/dev/null || true
    chmod 0600 "$RA_PRIVATE_DIR/last-status.json" 2>/dev/null || true
  fi
  printf '%s' "$payload"
}

ra_status_classify() {
  local target="$1" payload="$2"
  jq -r --arg target "$target" '
    if type!="object" or (.connection|type)!="string" then "INCOMPATIBLE"
    elif (.transactions? != null and (.transactions|type)!="array") then "INCOMPATIBLE"
    elif (.tun_health? != null and (.tun_health|type)!="object") then "INCOMPATIBLE"
    else
      . as $s |
      ($s.transactions // []) as $txs |
      ([ $txs[]? | select((.requires_cleanup//false)==true) ] | length) as $cleanup |
      ([ $txs[]? | select(.state=="committed" and (.requires_cleanup//false)==false) ] | length) as $committed_count |
      (($s.active_transaction_id // "") | tostring) as $active_id |
      (if $active_id!="" then any($txs[]?; (.id//"")==$active_id and .state=="committed" and (.requires_cleanup//false)==false) else $committed_count>0 end) as $committed |
      (($s.tun_health.state // "") | tostring) as $health |
      (($s.terminal_reason // "") | tostring) as $terminal |
      (($s.lifecycle_phase // "") | tostring) as $phase |
      if $target=="active" or $target=="active-legacy" then
        if $cleanup>0 or $health=="cleanup-required" or $health=="degraded" or ($s.connection=="inactive" and $phase!="connecting") or $terminal!="" then "TERMINAL_IMPOSSIBLE"
        elif $s.connection=="active" and ($s.mode//"")=="tun" and $committed and (($target=="active-legacy" and ($health=="" or $health=="verified")) or ($target=="active" and $health=="verified")) then "TARGET_REACHED"
        elif $phase=="connecting" then "PROGRESS_POSSIBLE"
        elif $health=="revalidating" and $s.connection=="active" and ($s.mode//"")=="tun" and $committed then "PROGRESS_POSSIBLE"
        elif $s.connection=="error (core exited)" and ($health=="revalidating" or $target=="active-legacy") then "PROGRESS_POSSIBLE"
        elif $s.connection=="active" then "INCOMPATIBLE"
        else "INCOMPATIBLE" end
      elif $target=="inactive" then
        if $cleanup>0 or $health=="cleanup-required" then "TERMINAL_IMPOSSIBLE"
        elif $s.connection=="inactive" and ($active_id=="") and $committed_count==0 then "TARGET_REACHED"
        elif $phase=="connecting" or $s.connection=="active" or $s.connection=="error (core exited)" then "PROGRESS_POSSIBLE"
        else "INCOMPATIBLE" end
      else "INCOMPATIBLE" end
    end' <<<"$payload" 2>/dev/null || printf 'INCOMPATIBLE\n'
}

ra_wait_status() {
  local target="$1" timeout="$2" deadline payload classification
  deadline=$((SECONDS+timeout))
  while ((SECONDS<deadline)); do
    if payload="$(ra_status_json 2>/dev/null)"; then
      classification="$(ra_status_classify "$target" "$payload")" || classification=INCOMPATIBLE
      case "$classification" in
        TARGET_REACHED) return 0 ;;
        TERMINAL_IMPOSSIBLE) ra_die "daemon status reached terminal/impossible state while waiting for $target"; return 1 ;;
        INCOMPATIBLE) ra_die "daemon status schema/state is incompatible while waiting for $target"; return 1 ;;
        PROGRESS_POSSIBLE) ;;
        *) ra_die "unknown status classification: $classification"; return 1 ;;
      esac
    fi
    sleep 1
  done
  ra_die "daemon status did not converge to $target"
  return 1
}
ra_wait_active() { ra_wait_status active "${1:-120}"; }
ra_wait_active_legacy() { ra_wait_status active-legacy "${1:-120}"; }
ra_wait_inactive() { ra_wait_status inactive "${1:-90}"; }

ra_main_pid() {
  ra_capture systemctl show -p MainPID --value "$RA_SERVICE" || return 1
  [[ "$RA_CAPTURE" =~ ^[0-9]+$ && "$RA_CAPTURE" -gt 1 ]] || return 1
  printf '%s' "$RA_CAPTURE"
}

ra_wait_new_pid() {
  local old="$1" timeout="${2:-60}" deadline current
  deadline=$((SECONDS+timeout))
  while ((SECONDS<deadline)); do
    current="$(ra_main_pid 2>/dev/null || true)"
    if [[ -n "$current" && "$current" != "$old" ]]; then printf '%s' "$current"; return 0; fi
    sleep 1
  done
  return 1
}

ra_verify_ordinary_network() {
  ra_capture ip -4 route show table main default || return 1
  [[ -n "$RA_CAPTURE" ]] || return 1
  ra_capture getent ahostsv4 example.com || return 1
  [[ -n "$RA_CAPTURE" ]] || return 1
  ra_capture timeout 5 bash -c 'exec 3<>/dev/tcp/example.com/443; exec 3>&-' || return 1
  ra_capture curl -4 -fsS --connect-timeout 5 --max-time 10 "$RA_PROBE_URL" -o /dev/null || return 1
}

ra_preflight_clean_boundary() {
  local installed="$1"
  if [[ -z "$installed" ]]; then
    ra_verify_ordinary_network || { ra_preflight_die "ordinary network is not usable before mutation"; return 2; }
    return 0
  fi
  ra_wait_inactive 5 || { ra_preflight_die "Podlaz must be conclusively disconnected before release acceptance"; return 2; }
  ra_verify_ordinary_network || { ra_preflight_die "ordinary network is not usable before mutation"; return 2; }
}

ra_boot_manifest_capture() {
  if [[ ! -e "$RA_BOOT_MANIFEST" ]]; then jq -cn '{enabled:false}'; return 0; fi
  [[ -f "$RA_BOOT_MANIFEST" && ! -L "$RA_BOOT_MANIFEST" ]] || return 1
  local size mode uid gid sha payload
  size="$(stat -Lc '%s' "$RA_BOOT_MANIFEST")" || return 1
  ((size<=65536)) || return 1
  mode="$(stat -Lc '%a' "$RA_BOOT_MANIFEST")" || return 1
  uid="$(stat -Lc '%u' "$RA_BOOT_MANIFEST")" || return 1
  gid="$(stat -Lc '%g' "$RA_BOOT_MANIFEST")" || return 1
  sha="$(ra_pkg_sha "$RA_BOOT_MANIFEST")" || return 1
  payload="$(base64 -w0 "$RA_BOOT_MANIFEST")" || return 1
  jq -cn --arg mode "$mode" --argjson uid "$uid" --argjson gid "$gid" --arg sha "$sha" --arg payload "$payload" '{enabled:true,mode:$mode,uid:$uid,gid:$gid,sha256:$sha,payload_b64:$payload}'
}

ra_manifest_matches_snapshot() {
  local snap="$1" expected
  if [[ "$(jq -r '.enabled' <<<"$snap")" != true ]]; then [[ ! -e "$RA_BOOT_MANIFEST" && ! -L "$RA_BOOT_MANIFEST" ]]; return; fi
  [[ -f "$RA_BOOT_MANIFEST" && ! -L "$RA_BOOT_MANIFEST" ]] || return 1
  expected="$(jq -r '.sha256' <<<"$snap")" || return 1
  [[ "$(ra_pkg_sha "$RA_BOOT_MANIFEST")" == "$expected" ]]
}

ra_boot_manifest_restore() {
  local snap="$1"
  if [[ "$(jq -r '.enabled' <<<"$snap")" != true ]]; then
    if [[ -e "$RA_BOOT_MANIFEST" || -L "$RA_BOOT_MANIFEST" ]]; then
      [[ -f "$RA_BOOT_MANIFEST" && ! -L "$RA_BOOT_MANIFEST" ]] || return 1
      rm -f "$RA_BOOT_MANIFEST" || return 1
      sync -f "$(dirname "$RA_BOOT_MANIFEST")" 2>/dev/null || true
    fi
    return 0
  fi
  local dir tmp mode uid gid expected
  dir="$(dirname "$RA_BOOT_MANIFEST")"
  mkdir -p "$dir" || return 1
  tmp="$(mktemp "$dir/.boot-autostart-manifest.XXXXXX")" || return 1
  jq -r '.payload_b64' <<<"$snap" | base64 -d >"$tmp" || { rm -f "$tmp"; return 1; }
  expected="$(jq -r '.sha256' <<<"$snap")" || { rm -f "$tmp"; return 1; }
  [[ "$(ra_pkg_sha "$tmp")" == "$expected" ]] || { rm -f "$tmp"; return 1; }
  mode="$(jq -r '.mode' <<<"$snap")" || { rm -f "$tmp"; return 1; }
  uid="$(jq -r '.uid' <<<"$snap")" || { rm -f "$tmp"; return 1; }
  gid="$(jq -r '.gid' <<<"$snap")" || { rm -f "$tmp"; return 1; }
  chmod "$mode" "$tmp" || { rm -f "$tmp"; return 1; }
  chown "$uid:$gid" "$tmp" || { rm -f "$tmp"; return 1; }
  sync -f "$tmp" 2>/dev/null || true
  mv -f "$tmp" "$RA_BOOT_MANIFEST" || return 1
  sync -f "$dir" 2>/dev/null || true
  ra_manifest_matches_snapshot "$snap"
}

ra_privacy_baseline() {
  ra_capture ip -4 route show table main default || return 1
  local uplink host=example.com port=443 ip
  uplink="$(awk '{for(i=1;i<=NF;i++)if($i=="dev"){print $(i+1);exit}}' <<<"$RA_CAPTURE")"
  [[ -n "$uplink" && "$uplink" != podlaz0 ]] || return 1
  ra_capture getent ahostsv4 "$host" || return 1
  ip="$(awk 'NR==1{print $1}' <<<"$RA_CAPTURE")"
  [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || return 1
  ra_capture curl -4 -fsS --interface "$uplink" --connect-timeout 3 --max-time 5 --resolve "$host:$port:$ip" "$RA_PROBE_URL" -o /dev/null || return 1
  jq -cn --arg uplink "$uplink" --arg host "$host" --argjson port "$port" --arg ip "$ip" '{uplink:$uplink,host:$host,port:$port,ip:$ip}'
}

ra_privacy_direct_probe() {
  local b="$1" uplink host port ip
  uplink="$(jq -r '.uplink' <<<"$b")" || return 1
  host="$(jq -r '.host' <<<"$b")" || return 1
  port="$(jq -r '.port' <<<"$b")" || return 1
  ip="$(jq -r '.ip' <<<"$b")" || return 1
  if ra_capture curl -4 -fsS --interface "$uplink" --connect-timeout 2 --max-time 3 --resolve "$host:$port:$ip" "$RA_PROBE_URL" -o /dev/null; then printf '0'; else printf '%s' "$RA_CAPTURE_RC"; fi
}

ra_privacy_verify_rule() {
  local rule="$1" kind="$2" protection="$3" tun
  case "$kind" in
    loopback) jq -e 'any(.expr[]?; .accept?!=null) and (.expr|tostring|contains("oifname")) and (.expr|tostring|contains("lo"))' <<<"$rule" >/dev/null ;;
    tun-egress) tun="$(jq -r '.tun_interface' <<<"$protection")"; jq -e --arg tun "$tun" 'any(.expr[]?; .accept?!=null) and (.expr|tostring|contains("oifname")) and (.expr|tostring|contains($tun))' <<<"$rule" >/dev/null ;;
    dhcp4) jq -e 'any(.expr[]?; .accept?!=null) and (.expr|tostring|contains("ipv4")) and (.expr|tostring|contains("udp")) and (.expr|tostring|contains("68")) and (.expr|tostring|contains("67"))' <<<"$rule" >/dev/null ;;
    dhcp6) jq -e 'any(.expr[]?; .accept?!=null) and (.expr|tostring|contains("ipv6")) and (.expr|tostring|contains("udp")) and (.expr|tostring|contains("546")) and (.expr|tostring|contains("547"))' <<<"$rule" >/dev/null ;;
    ipv6-link-control) jq -e 'any(.expr[]?; .accept?!=null) and (.expr|tostring|contains("icmpv6")) and (.expr|tostring|contains("nd-router-solicit")) and (.expr|tostring|contains("nd-neighbor-solicit")) and (.expr|tostring|contains("nd-neighbor-advert"))' <<<"$rule" >/dev/null ;;
    block-direct) jq -e 'any(.expr[]?; .reject?!=null)' <<<"$rule" >/dev/null ;;
    bootstrap) jq -e --argjson p "$protection" 'any(.expr[]?; .accept?!=null) and ([.expr[]?.match? | select(type=="object") | .right | tostring] | any(. as $v | $p.bootstrap_ipv4 | index($v)!=null))' <<<"$rule" >/dev/null ;;
    *) return 1 ;;
  esac
}

ra_privacy_local_proof() {
  [[ -f "$RA_CONTINUATION" && ! -L "$RA_CONTINUATION" ]] || return 1
  local protection table nftjson rules owner rule count expected bootstrap_seen=0
  protection="$(jq -ce 'select(.schema_version=="podlaz.network-session-state.v1" and .owner=="podlaz" and (.intent=="resume" or .intent=="terminal"))|.protection|select(type=="object" and (.state=="armed" or .state=="arming" or .state=="removing") and .composition_version==1 and .family=="inet" and (.bootstrap_ipv4|type)=="array" and (.bootstrap_ipv4|length)>0 and (.tun_interface|type)=="string")' "$RA_CONTINUATION" 2>/dev/null)" || return 1
  table="$(jq -r '.table' <<<"$protection")" || return 1
  [[ "$table" =~ ^podlaz_pe_[0-9a-f]{12}(_[1-9][0-9]{0,2})?$ ]] || return 1
  ra_capture nft -j list table inet "$table" || return 1
  nftjson="$RA_CAPTURE"
  jq -e --arg t "$table" '([.nftables[]?.table?|select(.family=="inet" and .name==$t)]|length)==1 and ([.nftables[]?.chain?|select(.family=="inet" and .table==$t and .name=="output" and .type=="filter" and .hook=="output" and (.prio|tonumber)==-10)]|length)==1' <<<"$nftjson" >/dev/null || return 1
  rules="$(jq -c --arg t "$table" '[.nftables[]?.rule?|select(.family=="inet" and .table==$t and .chain=="output")]' <<<"$nftjson")" || return 1
  expected=$(( $(jq '.bootstrap_ipv4|length' <<<"$protection") + 6 ))
  [[ "$(jq length <<<"$rules")" == "$expected" ]] || return 1
  for owner in loopback tun-egress dhcp4 dhcp6 ipv6-link-control block-direct; do
    count="$(jq --arg c "podlaz:privacy-envelope:$owner" '[.[]|select(.comment==$c)]|length' <<<"$rules")" || return 1
    [[ "$count" == 1 ]] || return 1
    rule="$(jq -c --arg c "podlaz:privacy-envelope:$owner" '.[]|select(.comment==$c)' <<<"$rules")" || return 1
    ra_privacy_verify_rule "$rule" "$owner" "$protection" || return 1
  done
  while IFS= read -r rule; do
    [[ -n "$rule" ]] || continue
    ra_privacy_verify_rule "$rule" bootstrap "$protection" || return 1
    ((bootstrap_seen+=1))
  done < <(jq -c '.[]|select(.comment=="podlaz:privacy-envelope:bootstrap")' <<<"$rules")
  [[ "$bootstrap_seen" == "$(jq '.bootstrap_ipv4|length' <<<"$protection")" ]]
}

ra_privacy_require_protected() {
  local baseline rc
  baseline="$(jq -ce '.private.privacy_baseline' "$RA_CHECKPOINT")" || return 1
  rc="$(ra_privacy_direct_probe "$baseline")" || return 1
  [[ "$rc" != 0 ]] || { ra_die "direct_egress_leak"; return 1; }
  ra_privacy_local_proof || { ra_die "inconclusive_local_privacy_authority"; return 1; }
}

ra_privacy_require_ordinary() {
  local baseline rc
  baseline="$(jq -ce '.private.privacy_baseline' "$RA_CHECKPOINT")" || return 1
  rc="$(ra_privacy_direct_probe "$baseline")" || return 1
  [[ "$rc" == 0 ]] || { ra_die "ordinary_direct_egress_not_restored"; return 1; }
  ra_verify_ordinary_network
}

ra_privacy_watch_start() {
  local label="$1"
  RA_PRIVACY_WATCH_STOP="$RA_PRIVATE_DIR/privacy-$label.stop"
  RA_PRIVACY_WATCH_FAIL="$RA_PRIVATE_DIR/privacy-$label.fail"
  rm -f "$RA_PRIVACY_WATCH_STOP" "$RA_PRIVACY_WATCH_FAIL"
  (
    while [[ ! -e "$RA_PRIVACY_WATCH_STOP" ]]; do
      if ! ra_privacy_require_protected >/dev/null 2>&1; then printf 'privacy proof failed during %s\n' "$label" >"$RA_PRIVACY_WATCH_FAIL"; exit 1; fi
      sleep 1
    done
  ) &
  RA_PRIVACY_WATCH_PID=$!
}

ra_privacy_watch_cancel() {
  if [[ -n "$RA_PRIVACY_WATCH_PID" ]]; then
    : >"$RA_PRIVACY_WATCH_STOP" 2>/dev/null || true
    wait "$RA_PRIVACY_WATCH_PID" 2>/dev/null || true
  fi
  RA_PRIVACY_WATCH_PID=""
  if [[ -n "$RA_BACKGROUND_PID" ]]; then
    kill "$RA_BACKGROUND_PID" 2>/dev/null || true
    wait "$RA_BACKGROUND_PID" 2>/dev/null || true
    RA_BACKGROUND_PID=""
  fi
}

ra_privacy_watch_stop() {
  [[ -n "$RA_PRIVACY_WATCH_PID" ]] || return 0
  : >"$RA_PRIVACY_WATCH_STOP" || return 1
  wait "$RA_PRIVACY_WATCH_PID" || true
  RA_PRIVACY_WATCH_PID=""
  [[ ! -s "$RA_PRIVACY_WATCH_FAIL" ]] || { ra_die "privacy protection failed during recovery window"; return 1; }
  ra_privacy_require_protected
}

ra_fixture_spec() {
  case "$1" in
    fixture_a) jq -cn '{name:"fixture_a",tun:"podlaz-accept-a0",cidr:"198.18.0.1/32",table:"51820",route:"198.51.100.254/32",rule_a:"198.51.100.254/32",rule_b:"198.51.100.253/32",priority_a:9999,priority_b:10000,nft_table:"podlaz_accept_a",dns_link:"podlaz-accept-adns0",dns_server:"192.0.2.53",dns_domain:"~accept-a.invalid"}' ;;
    fixture_b) jq -cn '{name:"fixture_b",tun:"podlaz-accept-b0",cidr:"198.18.62.1/32",table:"51962",route:"203.0.113.62/32",rule_a:"203.0.113.62/32",rule_b:"203.0.113.63/32",priority_a:10999,priority_b:11000,nft_table:"podlaz_accept_b",dns_link:"podlaz-accept-bdns0",dns_server:"192.0.2.54",dns_domain:"~accept-b.invalid"}' ;;
    *) return 1 ;;
  esac
}

ra_fixture_assert_free() {
  local spec="$1" tun dns nft pa pb table count
  tun="$(jq -r '.tun' <<<"$spec")"; dns="$(jq -r '.dns_link' <<<"$spec")"; nft="$(jq -r '.nft_table' <<<"$spec")"; pa="$(jq -r '.priority_a' <<<"$spec")"; pb="$(jq -r '.priority_b' <<<"$spec")"; table="$(jq -r '.table' <<<"$spec")"
  ra_capture ip -j -d link show || return 1
  count="$(jq --arg a "$tun" --arg b "$dns" '[.[]?|select(.ifname==$a or .ifname==$b)]|length' <<<"$RA_CAPTURE")" || return 1
  [[ "$count" == 0 ]] || return 1
  ra_capture nft -j list tables || return 1
  count="$(jq --arg n "$nft" '[.nftables[]?.table?|select(.family=="inet" and .name==$n)]|length' <<<"$RA_CAPTURE")" || return 1
  [[ "$count" == 0 ]] || return 1
  ra_capture ip -4 rule show || return 1
  count="$(awk -v a="$pa" -v b="$pb" '$1==a":"||$1==b":"{n++} END{print n+0}' <<<"$RA_CAPTURE")" || return 1
  [[ "$count" == 0 ]] || return 1
  ra_capture ip -j -4 route show table all || return 1
  count="$(jq --arg t "$table" '[.[]?|select(((.table//"")|tostring)==$t)]|length' <<<"$RA_CAPTURE")" || return 1
  [[ "$count" == 0 ]]
}

ra_fixture_acquire() {
  local name="$1" spec identity tun cidr table route rule_a rule_b pa pb nft dns server domain
  spec="$(ra_fixture_spec "$name")" || return 1
  ra_fixture_assert_free "$spec" || { ra_die "fixture identity is not free"; return 1; }
  identity="$(jq -c '.+{current_route:.route}' <<<"$spec")" || return 1
  ra_mut_begin_acquire "$name" network_fixture "$identity" || return 1
  tun="$(jq -r '.tun' <<<"$spec")"; cidr="$(jq -r '.cidr' <<<"$spec")"; table="$(jq -r '.table' <<<"$spec")"; route="$(jq -r '.route' <<<"$spec")"; rule_a="$(jq -r '.rule_a' <<<"$spec")"; rule_b="$(jq -r '.rule_b' <<<"$spec")"; pa="$(jq -r '.priority_a' <<<"$spec")"; pb="$(jq -r '.priority_b' <<<"$spec")"; nft="$(jq -r '.nft_table' <<<"$spec")"; dns="$(jq -r '.dns_link' <<<"$spec")"; server="$(jq -r '.dns_server' <<<"$spec")"; domain="$(jq -r '.dns_domain' <<<"$spec")"
  ra_capture ip tuntap add dev "$tun" mode tun || return 1
  ra_capture ip link set dev "$tun" up || return 1
  ra_capture ip -4 address add "$cidr" dev "$tun" || return 1
  ra_capture ip -4 route add blackhole "$route" table "$table" || return 1
  ra_capture ip -4 rule add priority "$pa" to "$rule_a" lookup "$table" || return 1
  ra_capture ip -4 rule add priority "$pb" to "$rule_b" lookup "$table" || return 1
  ra_capture nft add table inet "$nft" || return 1
  ra_capture ip link add "$dns" type dummy || return 1
  ra_capture ip link set dev "$dns" up || return 1
  ra_capture resolvectl dns "$dns" "$server" || return 1
  ra_capture resolvectl domain "$dns" "$domain" || return 1
  ra_capture resolvectl default-route "$dns" no || return 1
  ra_fixture_verify "$name" || return 1
  ra_mut_mark_acquired "$name"
}

ra_fixture_verify() {
  local name="$1" spec tun cidr table route nft dns server pa pb rule_a rule_b
  spec="$(jq -ce --arg n "$name" '.mutations[$n].identity' "$RA_CHECKPOINT")" || return 1
  tun="$(jq -r '.tun' <<<"$spec")"; cidr="$(jq -r '.cidr' <<<"$spec")"; table="$(jq -r '.table' <<<"$spec")"; route="$(jq -r '.current_route' <<<"$spec")"; nft="$(jq -r '.nft_table' <<<"$spec")"; dns="$(jq -r '.dns_link' <<<"$spec")"; server="$(jq -r '.dns_server' <<<"$spec")"; pa="$(jq -r '.priority_a' <<<"$spec")"; pb="$(jq -r '.priority_b' <<<"$spec")"; rule_a="$(jq -r '.rule_a' <<<"$spec")"; rule_b="$(jq -r '.rule_b' <<<"$spec")"
  ra_capture ip -j -d link show dev "$tun" || return 1
  jq -e --arg n "$tun" 'length==1 and .[0].ifname==$n and .[0].linkinfo.info_kind=="tun"' <<<"$RA_CAPTURE" >/dev/null || return 1
  ra_capture ip -4 address show dev "$tun" || return 1
  grep -Fq "$cidr" <<<"$RA_CAPTURE" || return 1
  ra_capture ip -j -4 route show table "$table" || return 1
  jq -e --arg dst "$route" --arg t "$table" 'length==1 and .[0].type=="blackhole" and .[0].dst==$dst and (.[0].table|tostring)==$t' <<<"$RA_CAPTURE" >/dev/null || return 1
  ra_capture ip -4 rule show priority "$pa" || return 1
  grep -Eq "^${pa}: .*to ${rule_a//./\.} .*lookup $table" <<<"$RA_CAPTURE" || return 1
  ra_capture ip -4 rule show priority "$pb" || return 1
  grep -Eq "^${pb}: .*to ${rule_b//./\.} .*lookup $table" <<<"$RA_CAPTURE" || return 1
  ra_capture nft -j list table inet "$nft" || return 1
  jq -e --arg n "$nft" '[.nftables[]?|select(has("metainfo")|not)] as $x|($x|length)==1 and $x[0].table.family=="inet" and $x[0].table.name==$n' <<<"$RA_CAPTURE" >/dev/null || return 1
  ra_capture ip -j -d link show dev "$dns" || return 1
  jq -e --arg n "$dns" 'length==1 and .[0].ifname==$n and .[0].linkinfo.info_kind=="dummy"' <<<"$RA_CAPTURE" >/dev/null || return 1
  ra_capture resolvectl status "$dns" --no-pager || return 1
  grep -Fq "$server" <<<"$RA_CAPTURE"
}

ra_fixture_churn() {
  local name="$1" spec table current alternate
  spec="$(jq -ce --arg n "$name" '.mutations[$n].identity' "$RA_CHECKPOINT")" || return 1
  table="$(jq -r '.table' <<<"$spec")"; current="$(jq -r '.current_route' <<<"$spec")"
  if [[ "$name" == fixture_b ]]; then alternate="203.0.113.64/32"; else alternate="198.51.100.252/32"; fi
  ra_capture ip -4 route replace blackhole "$alternate" table "$table" || return 1
  ra_capture ip -4 route del blackhole "$current" table "$table" || return 1
  ra_state_jq '.mutations[$n].identity.current_route=$route' --arg n "$name" --arg route "$alternate"
}

ra_observe_link_exact() {
  local name="$1" kind="$2" count observed ifname
  if ! ra_capture ip -j -d link show dev "$name"; then
    case "$RA_CAPTURE" in
      ""|*"does not exist"*|*"Cannot find device"*|*"No such device"*) printf 'absent'; return 0 ;;
      *) return 1 ;;
    esac
  fi
  count="$(jq length <<<"$RA_CAPTURE" 2>/dev/null)" || return 1
  [[ "$count" == 1 ]] || return 1
  ifname="$(jq -r '.[0].ifname//""' <<<"$RA_CAPTURE")"; observed="$(jq -r '.[0].linkinfo.info_kind//""' <<<"$RA_CAPTURE")"
  [[ "$ifname" == "$name" && "$observed" == "$kind" ]] || return 1
  printf 'present'
}

ra_fixture_release_partial() {
  local name="$1" spec state tun dns table route pa pb rule_a rule_b nft tun_state dns_state n line route_state=absent rule_a_state=absent rule_b_state=absent nft_state=absent
  spec="$(jq -ce --arg n "$name" '.mutations[$n].identity' "$RA_CHECKPOINT")" || return 1
  state="$(jq -r --arg n "$name" '.mutations[$n].state' "$RA_CHECKPOINT")" || return 1
  [[ "$state" == acquiring || "$state" == releasing ]] || return 1
  tun="$(jq -r '.tun' <<<"$spec")"; dns="$(jq -r '.dns_link' <<<"$spec")"; table="$(jq -r '.table' <<<"$spec")"; route="$(jq -r '.current_route' <<<"$spec")"; pa="$(jq -r '.priority_a' <<<"$spec")"; pb="$(jq -r '.priority_b' <<<"$spec")"; rule_a="$(jq -r '.rule_a' <<<"$spec")"; rule_b="$(jq -r '.rule_b' <<<"$spec")"; nft="$(jq -r '.nft_table' <<<"$spec")"
  tun_state="$(ra_observe_link_exact "$tun" tun)" || return 1
  dns_state="$(ra_observe_link_exact "$dns" dummy)" || return 1
  ra_capture ip -j -4 route show table "$table" || return 1
  n="$(jq length <<<"$RA_CAPTURE")" || return 1
  if [[ "$n" != 0 ]]; then
    [[ "$n" == 1 ]] || return 1
    jq -e --arg dst "$route" --arg t "$table" '.[0].type=="blackhole" and .[0].dst==$dst and (.[0].table|tostring)==$t' <<<"$RA_CAPTURE" >/dev/null || return 1
    route_state=present
  fi
  ra_capture ip -4 rule show priority "$pa" || return 1
  if [[ -n "$RA_CAPTURE" ]]; then line="$RA_CAPTURE"; [[ "$(awk 'NF{n++} END{print n+0}' <<<"$line")" == 1 ]] && grep -Eq "^${pa}: .*to ${rule_a//./\.} .*lookup $table" <<<"$line" || return 1; rule_a_state=present; fi
  ra_capture ip -4 rule show priority "$pb" || return 1
  if [[ -n "$RA_CAPTURE" ]]; then line="$RA_CAPTURE"; [[ "$(awk 'NF{n++} END{print n+0}' <<<"$line")" == 1 ]] && grep -Eq "^${pb}: .*to ${rule_b//./\.} .*lookup $table" <<<"$line" || return 1; rule_b_state=present; fi
  if ra_capture nft -j list table inet "$nft"; then
    jq -e --arg n "$nft" '[.nftables[]?|select(has("metainfo")|not)] as $x|($x|length)==1 and $x[0].table.family=="inet" and $x[0].table.name==$n' <<<"$RA_CAPTURE" >/dev/null || return 1
    nft_state=present
  else
    case "$RA_CAPTURE" in ""|*"No such file or directory"*|*"does not exist"*) ;; *) return 1 ;; esac
  fi
  if [[ "$dns_state" == present ]]; then ra_capture resolvectl revert "$dns" || return 1; ra_capture ip link del dev "$dns" || return 1; fi
  [[ "$nft_state" == absent ]] || ra_capture nft delete table inet "$nft" || return 1
  [[ "$rule_b_state" == absent ]] || ra_capture ip -4 rule del priority "$pb" to "$rule_b" lookup "$table" || return 1
  [[ "$rule_a_state" == absent ]] || ra_capture ip -4 rule del priority "$pa" to "$rule_a" lookup "$table" || return 1
  [[ "$route_state" == absent ]] || ra_capture ip -4 route del blackhole "$route" table "$table" || return 1
  [[ "$tun_state" == absent ]] || ra_capture ip link del dev "$tun" || return 1
  ra_mut_mark_released "$name"
}

ra_fixture_release() {
  local name="$1" state
  state="$(jq -r --arg n "$name" '.mutations[$n].state//"released"' "$RA_CHECKPOINT")" || return 1
  [[ "$state" != released ]] || return 0
  if [[ "$state" == acquired ]]; then ra_fixture_verify "$name" || return 1; ra_mut_begin_release "$name" || return 1; fi
  ra_fixture_release_partial "$name"
}

ra_collision_require_disjoint() {
  local fixture="$1" spec address table pa pb hits=0 path tx
  spec="$(jq -ce --arg n "$fixture" '.mutations[$n].identity' "$RA_CHECKPOINT")" || return 1
  address="$(jq -r '.cidr|split("/")[0]' <<<"$spec")"; table="$(jq -r '.table' <<<"$spec")"; pa="$(jq -r '.priority_a' <<<"$spec")"; pb="$(jq -r '.priority_b' <<<"$spec")"
  [[ -d "$RA_TRANSACTIONS" ]] || return 1
  while IFS= read -r -d '' path; do
    tx="$(cat "$path")" || return 1
    if jq -e '.owner=="podlaz" and .state=="committed"' <<<"$tx" >/dev/null 2>&1; then
      ((hits+=1))
      if jq -e --arg a "$address" --arg t "$table" --argjson pa "$pa" --argjson pb "$pb" '..|scalars|select((tostring)==$a or (tostring)==$t or .==$pa or .==$pb)' <<<"$tx" >/dev/null 2>&1; then ra_die "candidate allocation collides with occupied fixture resources"; return 1; fi
    fi
  done < <(find "$RA_TRANSACTIONS" -maxdepth 1 -type f -name '*.json' -print0 2>/dev/null)
  [[ "$hits" == 1 ]] || { ra_die "expected exactly one committed candidate transaction"; return 1; }
  ra_fixture_verify "$fixture"
}

ra_hook_allowed_name() { case "$1" in rollback-pause.arm|rollback-pause.ready|rollback-pause.continue|terminal-failure.trigger|terminal-data-plane-clean.ready|terminal-data-plane-clean.continue|privacy-teardown.ready|privacy-teardown.continue) return 0 ;; *) return 1 ;; esac; }

ra_systemd_hook_acquire() {
  local name="$1" path="$2" hook_dir="$3" content="$4" expected identity
  [[ ! -e "$path" && ! -L "$path" && ! -e "$hook_dir" && ! -L "$hook_dir" ]] || { ra_die "acceptance systemd hook path is already occupied"; return 1; }
  expected="$(printf '%s' "$content" | sha256sum | awk '{print $1}')" || return 1
  identity="$(jq -cn --arg path "$path" --arg hook "$hook_dir" --arg sha "$expected" '{path:$path,hook_dir:$hook,sha256:$sha}')" || return 1
  ra_mut_begin_acquire "$name" systemd_dropin "$identity" || return 1
  mkdir -p "$(dirname "$path")" "$hook_dir" || return 1
  chmod 0700 "$hook_dir" || return 1
  printf '%s' "$content" >"$path" || return 1
  chmod 0644 "$path" || return 1
  [[ "$(ra_pkg_sha "$path")" == "$expected" ]] || return 1
  ra_capture systemctl daemon-reload || return 1
  ra_mut_mark_acquired "$name"
}

ra_systemd_hook_cleanup() {
  local name="$1" state path hook_dir expected child base
  state="$(jq -r --arg n "$name" '.mutations[$n].state' "$RA_CHECKPOINT")" || return 1
  path="$(jq -r --arg n "$name" '.mutations[$n].identity.path' "$RA_CHECKPOINT")"; hook_dir="$(jq -r --arg n "$name" '.mutations[$n].identity.hook_dir' "$RA_CHECKPOINT")"; expected="$(jq -r --arg n "$name" '.mutations[$n].identity.sha256' "$RA_CHECKPOINT")"
  [[ "$state" != acquired ]] || ra_mut_begin_release "$name" || return 1
  if [[ -e "$path" || -L "$path" ]]; then [[ -f "$path" && ! -L "$path" && "$(ra_pkg_sha "$path")" == "$expected" ]] || { ra_die "acceptance systemd drop-in identity drifted"; return 1; }; rm -f "$path" || return 1; fi
  if [[ -e "$hook_dir" || -L "$hook_dir" ]]; then
    [[ -d "$hook_dir" && ! -L "$hook_dir" ]] || return 1
    while IFS= read -r -d '' child; do
      [[ -f "$child" && ! -L "$child" ]] || return 1
      base="$(basename "$child")"
      ra_hook_allowed_name "$base" || { ra_die "foreign hook marker: $base"; return 1; }
      rm -f "$child" || return 1
    done < <(find "$hook_dir" -mindepth 1 -maxdepth 1 -print0)
    rmdir "$hook_dir" || return 1
  fi
  ra_capture systemctl daemon-reload || return 1
  state="$(jq -r --arg n "$name" '.mutations[$n].state' "$RA_CHECKPOINT")"
  if [[ "$state" == acquiring || "$state" == releasing ]]; then ra_mut_mark_released "$name"; fi
}

ra_is_remote_session() { [[ -n "${SSH_CONNECTION:-}${SSH_CLIENT:-}${SSH_TTY:-}" ]]; }
ra_nm_connection_active() { local connection="$1" device="$2"; ra_capture nmcli -t -f NAME,DEVICE connection show --active || return 1; awk -F: -v c="$connection" -v d="$device" '$1==c&&$2==d{found=1} END{exit !found}' <<<"$RA_CAPTURE"; }

ra_nm_reconcile() {
  local name="$1" state connection device
  state="$(jq -r --arg n "$name" '.mutations[$n].state' "$RA_CHECKPOINT")"; connection="$(jq -r --arg n "$name" '.mutations[$n].identity.connection' "$RA_CHECKPOINT")"; device="$(jq -r --arg n "$name" '.mutations[$n].identity.device' "$RA_CHECKPOINT")"
  if ra_nm_connection_active "$connection" "$device"; then
    if [[ "$state" == acquired ]]; then ra_mut_begin_release "$name" || return 1; fi
    state="$(jq -r --arg n "$name" '.mutations[$n].state' "$RA_CHECKPOINT")"
    if [[ "$state" == acquiring || "$state" == releasing ]]; then ra_mut_mark_released "$name"; fi
    return 0
  fi
  if [[ "$state" == acquiring ]]; then ra_mut_mark_acquired "$name" || return 1; fi
  state="$(jq -r --arg n "$name" '.mutations[$n].state' "$RA_CHECKPOINT")"
  [[ "$state" != acquired ]] || ra_mut_begin_release "$name" || return 1
  ra_capture nmcli connection up "$connection" || return 1
  ra_nm_connection_active "$connection" "$device" || return 1
  ra_mut_mark_released "$name"
}

ra_wifi_reconnect_once() {
  ((RA_ALLOW_WIFI==1)) || { ra_record wifi_reconnect SKIP_USER_REQUEST; return 0; }
  if ra_is_remote_session; then ra_record wifi_reconnect SKIP_REMOTE_SESSION "remote session"; return 0; fi
  command -v nmcli >/dev/null 2>&1 || { ra_record wifi_reconnect SKIP_HOST_CAPABILITY "nmcli unavailable"; return 0; }
  ra_capture nmcli -t -f NAME,TYPE,DEVICE connection show --active || { ra_record wifi_reconnect SKIP_HOST_CAPABILITY "NetworkManager query failed"; return 0; }
  local line connection device identity
  line="$(awk -F: '$2=="802-11-wireless"&&$3!=""{print;exit}' <<<"$RA_CAPTURE")"
  [[ -n "$line" ]] || { ra_record wifi_reconnect SKIP_HOST_CAPABILITY "no active Wi-Fi connection"; return 0; }
  IFS=: read -r connection _ device <<<"$line"
  identity="$(jq -cn --arg connection "$connection" --arg device "$device" '{connection:$connection,device:$device}')" || return 1
  ra_mut_begin_acquire wifi_reconnect networkmanager_connection "$identity" || return 1
  ra_capture nmcli connection down "$connection" || return 1
  ra_mut_mark_acquired wifi_reconnect || return 1
  ra_capture nmcli connection up "$connection" || return 1
  ra_nm_connection_active "$connection" "$device" || return 1
  ra_mut_begin_release wifi_reconnect || return 1
  ra_mut_mark_released wifi_reconnect || return 1
  ra_wait_active 120 || return 1
  ra_privacy_require_protected || return 1
  ra_record wifi_reconnect PASS
}

ra_suspend_once() {
  ((RA_ALLOW_SUSPEND==1)) || { ra_record suspend_resume SKIP_USER_REQUEST; return 0; }
  if ra_is_remote_session; then ra_record suspend_resume SKIP_REMOTE_SESSION "remote session"; return 0; fi
  command -v rtcwake >/dev/null 2>&1 || { ra_record suspend_resume SKIP_HOST_CAPABILITY "rtcwake unavailable"; return 0; }
  if ! ra_capture rtcwake -m mem -s 15; then ra_record suspend_resume SKIP_HOST_CAPABILITY "bounded rtcwake suspend unavailable"; return 0; fi
  ra_wait_active 180 || return 1
  ra_privacy_require_protected || return 1
  ra_record suspend_resume PASS
}

ra_lifecycle_graceful_restart() {
  local before
  before="$(ra_main_pid)" || return 1
  ra_state_jq '.scenarios.graceful_restart.private.before_pid=$pid' --argjson pid "$before" || return 1
  ra_privacy_watch_start graceful_restart
  ra_capture systemctl restart "$RA_SERVICE" || { ra_privacy_watch_cancel; return 1; }
  ra_wait_new_pid "$before" >/dev/null || { ra_privacy_watch_cancel; return 1; }
  ra_wait_active || { ra_privacy_watch_cancel; return 1; }
  ra_privacy_watch_stop
}

ra_lifecycle_unexpected_death() {
  local before
  before="$(ra_main_pid)" || return 1
  ra_state_jq '.scenarios.daemon_kill.private.before_pid=$pid' --argjson pid "$before" || return 1
  ra_privacy_watch_start daemon_kill
  ra_capture systemctl kill --kill-who=main --signal=SIGKILL "$RA_SERVICE" || { ra_privacy_watch_cancel; return 1; }
  ra_wait_new_pid "$before" >/dev/null || { ra_privacy_watch_cancel; return 1; }
  ra_wait_active || { ra_privacy_watch_cancel; return 1; }
  ra_privacy_watch_stop
}

ra_service_is_active() {
  if ra_capture systemctl is-active --quiet "$RA_SERVICE"; then return 0; fi
  [[ "$RA_CAPTURE_RC" == 3 ]] && return 1
  return 2
}

ra_lifecycle_stop_start() {
  ra_state_jq '.scenarios.stop_start_no_reconnect.private.stop_requested=true' || return 1
  ra_capture systemctl stop "$RA_SERVICE" || return 1
  ra_state_jq '.scenarios.stop_start_no_reconnect.private.stop_completed=true' || return 1
  ra_verify_inactive_boundary || return 1
  ra_privacy_require_ordinary || return 1
  ra_capture systemctl start "$RA_SERVICE" || return 1
  ra_state_jq '.scenarios.stop_start_no_reconnect.private.start_completed=true' || return 1
  ra_wait_inactive 90
}

ra_lifecycle_reinstall() {
  local candidate="$1" before identity
  before="$(ra_main_pid)" || return 1
  ra_state_jq '.scenarios.reinstall.private.before_pid=$pid' --argjson pid "$before" || return 1
  identity="$(jq -cn --argjson c "$candidate" --argjson pid "$before" '{candidate:$c,before_pid:$pid}')" || return 1
  ra_mut_begin_acquire reinstall_package candidate_package "$identity" || return 1
  ra_privacy_watch_start reinstall
  ra_pkg_install_exact "$candidate" || { ra_privacy_watch_cancel; return 1; }
  ra_mut_mark_acquired reinstall_package || { ra_privacy_watch_cancel; return 1; }
  ra_state_jq '.scenarios.reinstall.private.dpkg_completed=true' || { ra_privacy_watch_cancel; return 1; }
  ra_wait_new_pid "$before" >/dev/null || { ra_privacy_watch_cancel; return 1; }
  ra_wait_active || { ra_privacy_watch_cancel; return 1; }
  ra_privacy_watch_stop || return 1
  ra_mut_begin_release reinstall_package || return 1
  ra_mut_mark_released reinstall_package
}

ra_wait_file() { local path="$1" deadline=$((SECONDS+$2)); while ((SECONDS<deadline)); do [[ -f "$path" && ! -L "$path" ]] && return 0; sleep 1; done; return 1; }

ra_exact_rolling_back_count() {
  [[ -d "$RA_TRANSACTIONS" ]] || { printf '0'; return 0; }
  local count=0 path
  while IFS= read -r -d '' path; do
    if jq -e '.owner=="podlaz" and .state=="rolling_back" and (.rollback|type)=="object" and ([.rollback.tun_addresses[]?,.rollback.routes[]?,.rollback.policy_rules[]?,.rollback.dns[]?,.rollback.nftables[]?,.rollback.generated_configs[]?,.rollback.child_processes[]?]|any(.owner=="podlaz"))' "$path" >/dev/null 2>&1; then ((count+=1)); fi
  done < <(find "$RA_TRANSACTIONS" -maxdepth 1 -type f -name '*.json' -print0 2>/dev/null)
  printf '%s' "$count"
}

ra_lifecycle_rollback_interruption() {
  local profile="$1" content old restart_pid
  ra_disconnect || return 1
  ra_wait_inactive || return 1
  ra_privacy_require_ordinary || return 1
  printf -v content '[Service]\nEnvironment=PODLAZ_E2E_TUN_ROLLBACK_PAUSE=true\nEnvironment=PODLAZ_E2E_TUN_ROLLBACK_PAUSE_DIR=%s\nEnvironment=PODLAZ_E2E_TUN_ROLLBACK_PAUSE_TIMEOUT_SECONDS=180\n' "$RA_ROLLBACK_HOOK_DIR"
  ra_systemd_hook_acquire rollback_hook "$RA_ROLLBACK_OVERRIDE" "$RA_ROLLBACK_HOOK_DIR" "$content" || return 1
  ra_capture systemctl restart "$RA_SERVICE" || return 1
  ra_wait_inactive || return 1
  ra_connect "$profile" || return 1
  ra_wait_active || return 1
  ra_privacy_require_protected || return 1
  printf 'armed\n' >"$RA_ROLLBACK_HOOK_DIR/rollback-pause.arm" || return 1
  old="$(ra_main_pid)" || return 1
  ra_privacy_watch_start rollback
  systemctl restart "$RA_SERVICE" >>"$RA_TRANSCRIPT" 2>&1 &
  restart_pid=$!
  RA_BACKGROUND_PID="$restart_pid"
  ra_wait_file "$RA_ROLLBACK_HOOK_DIR/rollback-pause.ready" 60 || return 1
  [[ "$(ra_exact_rolling_back_count)" == 1 ]] || return 1
  ra_capture kill -KILL "$old" || return 1
  wait "$restart_pid" || true
  RA_BACKGROUND_PID=""
  ra_wait_new_pid "$old" 120 >/dev/null || return 1
  ra_wait_active 180 || return 1
  ra_privacy_watch_stop || return 1
  ra_systemd_hook_cleanup rollback_hook
}

ra_runtime_terminal_failure() {
  local profile="$1" content before
  ra_wait_inactive || return 1
  ra_privacy_require_ordinary || return 1
  printf -v content '[Service]\nEnvironment=PODLAZ_E2E_TUN_TERMINAL_FAILURE=true\nEnvironment=PODLAZ_E2E_TUN_TERMINAL_FAILURE_DIR=%s\nEnvironment=PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE=true\nEnvironment=PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE_DIR=%s\nEnvironment=PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE_TIMEOUT_SECONDS=180\n' "$RA_TERMINAL_HOOK_DIR" "$RA_TERMINAL_HOOK_DIR"
  ra_systemd_hook_acquire terminal_hook "$RA_TERMINAL_OVERRIDE" "$RA_TERMINAL_HOOK_DIR" "$content" || return 1
  ra_capture systemctl restart "$RA_SERVICE" || return 1
  ra_wait_inactive || return 1
  ra_connect "$profile" || return 1
  ra_wait_active || return 1
  ra_privacy_require_protected || return 1
  printf 'trigger\n' >"$RA_TERMINAL_HOOK_DIR/terminal-failure.trigger" || return 1
  ra_wait_file "$RA_TERMINAL_HOOK_DIR/terminal-data-plane-clean.ready" 90 || return 1
  ra_privacy_require_protected || return 1
  printf 'continue\n' >"$RA_TERMINAL_HOOK_DIR/terminal-data-plane-clean.continue" || return 1
  ra_wait_inactive 120 || return 1
  ra_systemd_hook_cleanup terminal_hook || return 1
  ra_privacy_require_ordinary || return 1
  before="$(ra_main_pid)" || return 1
  ra_capture systemctl restart "$RA_SERVICE" || return 1
  ra_wait_new_pid "$before" >/dev/null || return 1
  ra_wait_inactive 30
}

ra_cgroup_path() { local pid="$1" rel; rel="$(awk -F: '$1==0{print $3}' "/proc/$pid/cgroup" 2>/dev/null)"; [[ -n "$rel" ]] || return 1; printf '/sys/fs/cgroup%s' "$rel"; }
ra_xray_pid() { local parent="$1" pid ppid exe; local found=(); while read -r pid ppid; do [[ "$ppid" == "$parent" ]] || continue; exe="$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)"; [[ "$exe" == /usr/lib/podlaz/xray ]] && found+=("$pid"); done < <(ps -eo pid=,ppid=); ((${#found[@]}==1)) || return 1; printf '%s' "${found[0]}"; }
ra_proc_sample() { local pid="$1" role="$2" rss pss threads fds cpu; rss="$(ps -o rss= -p "$pid" | tr -d ' ')"; pss="$(awk '/^Pss:/{print $2}' "/proc/$pid/smaps_rollup" 2>/dev/null || printf 0)"; threads="$(awk '/^Threads:/{print $2}' "/proc/$pid/status" 2>/dev/null || printf 0)"; fds="$(find "/proc/$pid/fd" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l)"; cpu="$(awk '{print $14+$15}' "/proc/$pid/stat" 2>/dev/null || printf 0)"; jq -cn --arg role "$role" --argjson pid "$pid" --argjson rss_kb "${rss:-0}" --argjson pss_kb "${pss:-0}" --argjson threads "${threads:-0}" --argjson fds "$fds" --argjson cpu_ticks "${cpu:-0}" '{role:$role,pid:$pid,rss_kb:$rss_kb,pss_kb:$pss_kb,threads:$threads,fds:$fds,cpu_ticks:$cpu_ticks}'; }
ra_resource_sample() { local tag="$1" daemon xray cgroup mem=0 peak=0 pids=0 cpu=0 d x; daemon="$(ra_main_pid)" || return 1; xray="$(ra_xray_pid "$daemon")" || return 1; cgroup="$(ra_cgroup_path "$daemon")" || return 1; [[ -r "$cgroup/memory.current" ]] && mem="$(cat "$cgroup/memory.current")"; [[ -r "$cgroup/memory.peak" ]] && peak="$(cat "$cgroup/memory.peak")"; [[ -r "$cgroup/pids.current" ]] && pids="$(cat "$cgroup/pids.current")"; [[ -r "$cgroup/cpu.stat" ]] && cpu="$(awk '$1=="usage_usec"{print $2}' "$cgroup/cpu.stat")"; d="$(ra_proc_sample "$daemon" daemon)"; x="$(ra_proc_sample "$xray" xray)"; jq -cn --arg tag "$tag" --argjson elapsed "$SECONDS" --argjson daemon "$d" --argjson xray "$x" --argjson memory_current "$mem" --argjson memory_peak "$peak" --argjson pids_current "$pids" --argjson cpu_usage_usec "${cpu:-0}" '{tag:$tag,elapsed:$elapsed,daemon:$daemon,xray:$xray,service:{memory_current:$memory_current,memory_peak:$memory_peak,pids_current:$pids_current,cpu_usage_usec:$cpu_usage_usec}}'; }
ra_inactive_sample() { local tag="$1" daemon cgroup mem=0 pids=0 fds=0; daemon="$(ra_main_pid)" || return 1; cgroup="$(ra_cgroup_path "$daemon")" || return 1; [[ -r "$cgroup/memory.current" ]] && mem="$(cat "$cgroup/memory.current")"; [[ -r "$cgroup/pids.current" ]] && pids="$(cat "$cgroup/pids.current")"; fds="$(find "/proc/$daemon/fd" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l)"; jq -cn --arg tag "$tag" --argjson memory_current "$mem" --argjson pids_current "$pids" --argjson daemon_fds "$fds" '{tag:$tag,memory_current:$memory_current,pids_current:$pids_current,daemon_fds:$daemon_fds}'; }

ra_soak_run() {
  local seconds interval doctor_interval start next next_doctor elapsed wifi_done=0 suspend_done=0 fixture_done=0 sample
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" == 1 ]]; then seconds="${RELEASE_ACCEPTANCE_TEST_SOAK_SECONDS:-3}"; interval=1; doctor_interval=2; else seconds=$((RA_SOAK_MINUTES*60)); interval=60; doctor_interval=600; fi
  start=$SECONDS; next=$SECONDS; next_doctor=$SECONDS
  : >"$RA_PRIVATE_DIR/soak-samples.jsonl"
  while ((SECONDS-start<seconds)); do
    elapsed=$((SECONDS-start))
    if ((SECONDS>=next)); then
      ra_wait_active 30 || return 1
      ra_privacy_require_protected || return 1
      ra_capture_user getent ahostsv4 example.com || return 1
      ra_capture_user curl -4 -fsS --max-time 10 "$RA_PROBE_URL" -o /dev/null || return 1
      sample="$(ra_resource_sample soak)" || return 1
      printf '%s\n' "$sample" >>"$RA_PRIVATE_DIR/soak-samples.jsonl"
      next=$((SECONDS+interval))
    fi
    if ((SECONDS>=next_doctor)); then ra_product doctor --tun --json >/dev/null || return 1; next_doctor=$((SECONDS+doctor_interval)); fi
    if ((fixture_done==0 && elapsed>=seconds/3)); then ra_fixture_acquire fixture_b || return 1; ra_fixture_churn fixture_b || return 1; ra_wait_active 60 || return 1; ra_privacy_require_protected || return 1; ra_fixture_verify fixture_b || return 1; ra_record active_coexistence PASS; fixture_done=1; fi
    if ((wifi_done==0 && elapsed>=seconds/2)); then ra_wifi_reconnect_once || return 1; wifi_done=1; fi
    if ((suspend_done==0 && elapsed>=(seconds*2)/3)); then ra_suspend_once || return 1; suspend_done=1; fi
    sleep 1
  done
  ((fixture_done==1)) || { ra_fixture_acquire fixture_b || return 1; ra_fixture_churn fixture_b || return 1; ra_wait_active || return 1; ra_fixture_verify fixture_b || return 1; ra_record active_coexistence PASS; }
  ((wifi_done==1)) || ra_wifi_reconnect_once || return 1
  ((suspend_done==1)) || ra_suspend_once || return 1
  ra_fixture_release fixture_b || return 1
  ra_record resource_soak PASS "" "$(jq -cn --argjson measured "$((SECONDS-start))" --argjson samples "$(wc -l <"$RA_PRIVATE_DIR/soak-samples.jsonl")" '{measured_seconds:$measured,samples:$samples}')"
}

ra_second_session_observe() {
  local seconds start sample
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" == 1 ]]; then seconds=2; else seconds=300; fi
  start=$SECONDS
  : >"$RA_PRIVATE_DIR/second-session.jsonl"
  while ((SECONDS-start<seconds)); do
    ra_wait_active 30 || return 1
    ra_privacy_require_protected || return 1
    sample="$(ra_resource_sample second_session)" || return 1
    printf '%s\n' "$sample" >>"$RA_PRIVATE_DIR/second-session.jsonl"
    if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" == 1 ]]; then sleep 1; else sleep 30; fi
  done
  ra_record reconnect_resource_nonaccumulation PASS "" "$(jq -cn --argjson samples "$(wc -l <"$RA_PRIVATE_DIR/second-session.jsonl")" '{samples:$samples,classification:"observation_no_structural_ownership_leak"}')"
}

ra_package_setup_prepare() {
  local candidate="$1" previous="$2" installed="$3" cv pv identity
  cv="$(jq -r '.version' <<<"$candidate")" || return 1
  if [[ -n "$installed" ]] && ra_pkg_gt "$installed" "$cv"; then ra_preflight_die "installed Podlaz version is newer than candidate; refusing downgrade"; return 2; fi
  if [[ -n "$installed" && "$installed" != "$cv" ]] && ra_pkg_lt "$installed" "$cv"; then return 0; fi
  [[ -n "$previous" ]] || { ra_die "full lower-release qualification requires an installed lower release or --previous-deb"; return 1; }
  pv="$(jq -r '.version' <<<"$previous")" || return 1
  ra_pkg_lt "$pv" "$cv" || { ra_die "--previous-deb is not strictly lower than candidate"; return 1; }
  identity="$(jq -cn --argjson p "$previous" --argjson c "$candidate" '{previous:$p,candidate:$c}')" || return 1
  ra_mut_begin_acquire package_setup previous_package "$identity" || return 1
  ra_pkg_install_exact "$previous" || return 1
  ra_wait_inactive 90 || return 1
  ra_mut_mark_acquired package_setup
}

ra_package_setup_reconcile_abort() {
  local state identity candidate installed cv pv
  state="$(jq -r '.mutations.package_setup.state//"released"' "$RA_CHECKPOINT")" || return 1
  [[ "$state" != released ]] || return 0
  identity="$(jq -ce '.mutations.package_setup.identity' "$RA_CHECKPOINT")" || return 1
  candidate="$(jq -c '.candidate' <<<"$identity")" || return 1
  cv="$(jq -r '.candidate.version' <<<"$identity")" || return 1
  pv="$(jq -r '.previous.version' <<<"$identity")" || return 1
  installed="$(ra_pkg_installed_version)" || return 1
  if [[ "$installed" == "$cv" ]]; then
    if [[ "$state" == acquired ]]; then ra_mut_begin_release package_setup || return 1; fi
    state="$(jq -r '.mutations.package_setup.state' "$RA_CHECKPOINT")"
    if [[ "$state" == acquiring || "$state" == releasing ]]; then ra_mut_mark_released package_setup; fi
    return 0
  fi
  [[ "$installed" == "$pv" ]] || { ra_die "package_setup installed version is neither previous nor candidate"; return 1; }
  if [[ "$state" == acquiring ]]; then ra_mut_mark_acquired package_setup || return 1; fi
  state="$(jq -r '.mutations.package_setup.state' "$RA_CHECKPOINT")"
  [[ "$state" != acquired ]] || ra_mut_begin_release package_setup || return 1
  ra_pkg_assert_identity "$candidate" || return 1
  ra_pkg_install_exact "$candidate" || return 1
  ra_wait_inactive 90 || return 1
  ra_mut_mark_released package_setup
}

ra_package_setup_release_after_candidate() {
  local state installed cv
  state="$(jq -r '.mutations.package_setup.state//"released"' "$RA_CHECKPOINT")" || return 1
  [[ "$state" != released ]] || return 0
  installed="$(ra_pkg_installed_version)" || return 1
  cv="$(jq -r '.candidate.version' "$RA_CHECKPOINT")" || return 1
  [[ "$installed" == "$cv" ]] || return 1
  if [[ "$state" == acquired ]]; then ra_mut_begin_release package_setup || return 1; elif [[ "$state" != acquiring && "$state" != releasing ]]; then return 1; fi
  ra_mut_mark_released package_setup
}

ra_candidate_upgrade_begin() {
  local candidate="$1" installed="$2" identity
  identity="$(jq -cn --argjson c "$candidate" --arg installed "$installed" '{candidate:$c,installed_before:$installed,applied:false}')" || return 1
  ra_mut_begin_acquire candidate_upgrade candidate_package "$identity"
}

ra_candidate_upgrade_reconcile_cleanup() {
  local state candidate before installed cv
  state="$(jq -r '.mutations.candidate_upgrade.state//"released"' "$RA_CHECKPOINT")" || return 1
  [[ "$state" != released ]] || return 0
  candidate="$(jq -ce '.mutations.candidate_upgrade.identity.candidate' "$RA_CHECKPOINT")" || return 1
  before="$(jq -r '.mutations.candidate_upgrade.identity.installed_before' "$RA_CHECKPOINT")" || return 1
  cv="$(jq -r '.version' <<<"$candidate")" || return 1
  installed="$(ra_pkg_installed_version)" || return 1
  if [[ "$installed" == "$cv" ]]; then
    ra_state_jq '.mutations.candidate_upgrade.identity.applied=true' || return 1
    if [[ "$state" == acquiring ]]; then ra_mut_mark_acquired candidate_upgrade || return 1; state=acquired; fi
    [[ "$state" != acquired ]] || ra_mut_begin_release candidate_upgrade || return 1
    ra_mut_mark_released candidate_upgrade
    return 0
  fi
  if [[ "$installed" == "$before" ]]; then
    [[ "$state" == acquiring ]] || { ra_die "candidate upgrade regressed after acquisition"; return 1; }
    ra_state_jq '.mutations.candidate_upgrade.identity.applied=false' || return 1
    ra_mut_mark_released candidate_upgrade
    return 0
  fi
  ra_die "candidate upgrade package state is ambiguous"
  return 1
}

ra_reinstall_package_reconcile_cleanup() {
  local state candidate installed cv
  state="$(jq -r '.mutations.reinstall_package.state//"released"' "$RA_CHECKPOINT")" || return 1
  [[ "$state" != released ]] || return 0
  candidate="$(jq -ce '.mutations.reinstall_package.identity.candidate' "$RA_CHECKPOINT")" || return 1
  cv="$(jq -r '.version' <<<"$candidate")" || return 1
  installed="$(ra_pkg_installed_version)" || return 1
  [[ "$installed" == "$cv" ]] || { ra_die "same-candidate reinstall left an unexpected package version"; return 1; }
  if [[ "$state" == acquiring ]]; then
    local before current
    before="$(jq -r '.mutations.reinstall_package.identity.before_pid' "$RA_CHECKPOINT")" || return 1
    current="$(ra_main_pid 2>/dev/null || true)"
    [[ -n "$current" && "$current" != "$before" ]] || { ra_die "cannot prove whether interrupted same-candidate reinstall completed"; return 1; }
    ra_mut_mark_acquired reinstall_package || return 1
    state=acquired
  fi
  [[ "$state" != acquired ]] || ra_mut_begin_release reinstall_package || return 1
  ra_mut_mark_released reinstall_package
}

ra_terminal_profile_is_exact() { ra_product profile show "$1" --json || return 1; jq -e --arg name "$RA_TERMINAL_NAME" '.schema_version=="v1" and .status=="ok" and .profile.name==$name and .profile.server=="vpn.invalid" and (.profile.port|tonumber)==443 and (.profile.protocol|ascii_downcase)=="vless"' <<<"$RA_CAPTURE" >/dev/null; }

ra_terminal_profile_create() {
  local baseline after additions id imported
  baseline="$(ra_profile_ids_json)" || return 1
  ra_state_jq '.private.terminal_profile_acquisition={state:"acquiring",baseline_ids:$ids}' --argjson ids "$baseline" || return 1
  ra_product profile import "$RA_TERMINAL_URI" || return 1
  imported="$(sed -n 's/^Imported profile:[[:space:]]*//p' <<<"$RA_CAPTURE" | head -n1)"
  after="$(ra_profile_ids_json)" || return 1
  additions="$(jq -cn --argjson a "$after" --argjson b "$baseline" '$a-$b')" || return 1
  [[ "$(jq length <<<"$additions")" == 1 ]] || return 1
  id="$(jq -r '.[0]' <<<"$additions")"
  [[ -z "$imported" || "$imported" == "$id" ]] || return 1
  ra_terminal_profile_is_exact "$id" || return 1
  ra_state_jq '.private.terminal_profile=$id|.private.terminal_profile_acquisition.state="acquired"|.private.terminal_profile_acquisition.profile_id=$id' --arg id "$id" || return 1
  printf '%s' "$id"
}

ra_terminal_profile_reconcile() {
  local acq state baseline current additions id
  acq="$(jq -ce '.private.terminal_profile_acquisition//empty' "$RA_CHECKPOINT" 2>/dev/null || true)"
  [[ -n "$acq" ]] || return 0
  state="$(jq -r '.state' <<<"$acq")"; baseline="$(jq -c '.baseline_ids' <<<"$acq")"
  current="$(ra_profile_ids_json)" || return 1
  if [[ "$state" == acquiring ]]; then
    additions="$(jq -cn --argjson a "$current" --argjson b "$baseline" '$a-$b')" || return 1
    if [[ "$(jq length <<<"$additions")" == 0 ]]; then ra_state_jq 'del(.private.terminal_profile_acquisition)'; return 0; fi
    [[ "$(jq length <<<"$additions")" == 1 ]] || return 1
    id="$(jq -r '.[0]' <<<"$additions")"
    ra_terminal_profile_is_exact "$id" || return 1
    ra_state_jq '.private.terminal_profile=$id|.private.terminal_profile_acquisition.state="acquired"|.private.terminal_profile_acquisition.profile_id=$id' --arg id "$id" || return 1
  else id="$(jq -r '.profile_id' <<<"$acq")"; fi
  current="$(ra_profile_ids_json)" || return 1
  if jq -e --arg id "$id" 'index($id)!=null' <<<"$current" >/dev/null; then
    ra_terminal_profile_is_exact "$id" || return 1
    ra_state_jq '.private.terminal_profile_acquisition.state="releasing"' || return 1
    ra_product profile delete "$id" --yes || return 1
  fi
  ra_state_jq 'del(.private.terminal_profile,.private.terminal_profile_acquisition)'
}

ra_autostart_set_owned() {
  local name="$1" action="$2" profile="${3:-}" pre post identity
  pre="$(ra_boot_manifest_capture)" || return 1
  identity="$(jq -cn --arg action "$action" --arg profile "$profile" --argjson pre "$pre" '{action:$action,profile:$profile,pre_manifest:$pre}')" || return 1
  ra_mut_begin_acquire "$name" autostart_policy "$identity" || return 1
  if [[ "$action" == disable ]]; then ra_product autostart disable >/dev/null || return 1; else ra_product autostart enable --mode tun "$profile" >/dev/null || return 1; fi
  post="$(ra_boot_manifest_capture)" || return 1
  ra_state_jq '.mutations[$n].identity.owned_manifest=$post' --arg n "$name" --argjson post "$post" || return 1
  ra_mut_mark_acquired "$name"
}

ra_autostart_release_owned() {
  local name="$1" state pre owned
  state="$(jq -r --arg n "$name" '.mutations[$n].state//"released"' "$RA_CHECKPOINT")" || return 1
  [[ "$state" != released ]] || return 0
  pre="$(jq -ce --arg n "$name" '.mutations[$n].identity.pre_manifest' "$RA_CHECKPOINT")" || return 1
  owned="$(jq -ce --arg n "$name" '.mutations[$n].identity.owned_manifest//empty' "$RA_CHECKPOINT" 2>/dev/null || true)"
  if [[ "$state" == acquiring ]]; then
    if ra_manifest_matches_snapshot "$pre"; then
      ra_mut_mark_released "$name"
      return 0
    fi
    if [[ -n "$owned" ]] && ra_manifest_matches_snapshot "$owned"; then
      ra_mut_mark_acquired "$name" || return 1
      state=acquired
    else
      ra_die "autostart acquisition is ambiguous"
      return 1
    fi
  fi
  if [[ "$state" == acquired ]]; then
    [[ -n "$owned" ]] && ra_manifest_matches_snapshot "$owned" || { ra_die "autostart policy drifted from exact owned state"; return 1; }
    ra_mut_begin_release "$name" || return 1
    state=releasing
  fi
  if [[ "$state" == releasing ]]; then
    if ra_manifest_matches_snapshot "$pre"; then ra_mut_mark_released "$name"; return 0; fi
    [[ -n "$owned" ]] && ra_manifest_matches_snapshot "$owned" || { ra_die "autostart release is ambiguous"; return 1; }
    ra_boot_manifest_restore "$pre" || return 1
    ra_manifest_matches_snapshot "$pre" || return 1
    ra_mut_mark_released "$name"
  fi
}

ra_restore_original_policy() {
  local entries name state
  entries="$(jq -r '.mutations|to_entries|reverse[]|select(.value.kind=="autostart_policy")|[.key,.value.state]|@tsv' "$RA_CHECKPOINT")" || return 1
  while IFS=$'\t' read -r name state; do [[ -n "$name" && "$state" != released ]] || continue; ra_autostart_release_owned "$name" || return 1; done <<<"$entries"
  ra_terminal_profile_reconcile || return 1
  local original
  original="$(jq -ce '.private.boot_manifest' "$RA_CHECKPOINT")" || return 1
  ra_manifest_matches_snapshot "$original" || { ra_die "original autostart manifest was not restored exactly"; return 1; }
}

ra_cleanup_owned_mutations() {
  local entries name kind state
  entries="$(jq -r '.mutations|to_entries|reverse[]|[.key,.value.kind,.value.state]|@tsv' "$RA_CHECKPOINT")" || return 1
  while IFS=$'\t' read -r name kind state; do
    [[ -n "$name" && "$state" != released ]] || continue
    case "$kind" in
      previous_package|candidate_package|autostart_policy) ;;
      network_fixture) ra_fixture_release "$name" || return 1 ;;
      systemd_dropin) ra_systemd_hook_cleanup "$name" || return 1 ;;
      networkmanager_connection) ra_nm_reconcile "$name" || return 1 ;;
      *) ra_die "unsupported owned mutation kind: $kind"; return 1 ;;
    esac
  done <<<"$entries"
}

ra_require_mutations_released() {
  local pending
  pending="$(jq -r '[.mutations|to_entries[]|select(.value.state!="released")|.key]|join(",")' "$RA_CHECKPOINT")" || return 1
  [[ -z "$pending" ]] || { ra_die "owned mutation cleanup is incomplete: $pending"; return 1; }
}

ra_session_observe() {
  local payload cleanup active_id committed
  if payload="$(ra_status_json 2>/dev/null)"; then
    cleanup="$(jq '[.transactions[]?|select((.requires_cleanup//false)==true)]|length' <<<"$payload" 2>/dev/null)" || { printf 'ambiguous'; return 0; }
    active_id="$(jq -r '.active_transaction_id//""' <<<"$payload" 2>/dev/null)" || { printf 'ambiguous'; return 0; }
    committed="$(jq '[.transactions[]?|select(.state=="committed" and (.requires_cleanup//false)==false)]|length' <<<"$payload" 2>/dev/null)" || { printf 'ambiguous'; return 0; }
    if [[ "$(jq -r '.connection//""' <<<"$payload")" == active && "$(jq -r '.mode//""' <<<"$payload")" == tun && "$committed" -gt 0 ]]; then printf 'active'; return 0; fi
    if [[ "$(jq -r '.connection//""' <<<"$payload")" == inactive && "$cleanup" == 0 && -z "$active_id" && "$committed" == 0 ]]; then printf 'inactive'; return 0; fi
    printf 'ambiguous'; return 0
  fi
  if [[ -e "$RA_CONTINUATION" || -L "$RA_CONTINUATION" ]]; then printf 'ambiguous'; return 0; fi
  if [[ -d "$RA_TRANSACTIONS" ]]; then
    local path
    while IFS= read -r -d '' path; do
      if ! jq -e '(.requires_cleanup//false)==false and (.state!="committed") and (.state!="rolling_back") and (.state!="applying") and (.state!="verifying")' "$path" >/dev/null 2>&1; then printf 'ambiguous'; return 0; fi
    done < <(find "$RA_TRANSACTIONS" -maxdepth 1 -type f -name '*.json' -print0 2>/dev/null)
  fi
  printf 'inactive'
}

ra_safe_disconnect_if_owned() {
  local observed
  observed="$(ra_session_observe)" || return 1
  case "$observed" in
    inactive) return 0 ;;
    active) ra_disconnect || return 1; ra_wait_inactive 120 || return 1 ;;
    *) ra_die "acceptance session state is ambiguous; refusing cleanup mutation"; return 1 ;;
  esac
}

ra_verify_inactive_boundary() {
  local observed
  observed="$(ra_session_observe)" || return 1
  [[ "$observed" == inactive ]] || { ra_die "Podlaz is not conclusively inactive"; return 1; }
}

ra_package_cleanup_reconcile() {
  if jq -e '.mutations.candidate_upgrade and .mutations.candidate_upgrade.state!="released"' "$RA_CHECKPOINT" >/dev/null 2>&1; then ra_candidate_upgrade_reconcile_cleanup || return 1; fi
  if jq -e '.mutations.reinstall_package and .mutations.reinstall_package.state!="released"' "$RA_CHECKPOINT" >/dev/null 2>&1; then ra_reinstall_package_reconcile_cleanup || return 1; fi
  if jq -e '.mutations.package_setup and .mutations.package_setup.state!="released"' "$RA_CHECKPOINT" >/dev/null 2>&1; then ra_package_setup_reconcile_abort || return 1; fi
  return 0
}

ra_cleanup_expected_package_verify() {
  local installed initial candidate cv require_candidate=false
  installed="$(ra_pkg_installed_version)" || return 1
  initial="$(jq -r '.private.installed_before//""' "$RA_CHECKPOINT")" || return 1
  candidate="$(jq -ce '.candidate' "$RA_CHECKPOINT")" || return 1
  cv="$(jq -r '.version' <<<"$candidate")" || return 1
  if jq -e '.mutations.package_setup' "$RA_CHECKPOINT" >/dev/null 2>&1; then require_candidate=true; fi
  if jq -e '.mutations.reinstall_package' "$RA_CHECKPOINT" >/dev/null 2>&1; then require_candidate=true; fi
  if [[ "$(jq -r '.mutations.candidate_upgrade.identity.applied//false' "$RA_CHECKPOINT")" == true ]]; then require_candidate=true; fi
  if [[ "$require_candidate" == true ]]; then
    [[ "$installed" == "$cv" ]] || { ra_die "cleanup did not retain the candidate package required by owned package authority"; return 1; }
    return 0
  fi
  [[ "$installed" == "$cv" || "$installed" == "$initial" ]] || { ra_die "cleanup observed an unexpected Podlaz package version"; return 1; }
}

ra_safe_cleanup() {
  ra_privacy_watch_cancel
  ra_safe_disconnect_if_owned || return 1
  ra_cleanup_owned_mutations || return 1
  ra_restore_original_policy || return 1
  ra_package_cleanup_reconcile || return 1
  ra_cleanup_expected_package_verify || return 1
  ra_require_mutations_released || return 1
  ra_verify_inactive_boundary || return 1
  ra_privacy_require_ordinary || return 1
  [[ ! -e "$RA_ROLLBACK_OVERRIDE" && ! -L "$RA_ROLLBACK_OVERRIDE" && ! -e "$RA_TERMINAL_OVERRIDE" && ! -L "$RA_TERMINAL_OVERRIDE" ]] || { ra_die "acceptance systemd hooks remain after cleanup"; return 1; }
}

ra_public_scenarios_json() { jq '{scenarios:(.scenarios|with_entries(.value={outcome:(.value.outcome//"NOT_EXERCISED"),state:(.value.state//"")}))}' "$RA_CHECKPOINT"; }

ra_report_write() {
  local qualification="$1" summary="$RA_PUBLIC_DIR/summary.txt" report="$RA_PUBLIC_DIR/report.json" req="$RA_PUBLIC_DIR/requirements-observation.json"
  mkdir -p "$RA_PUBLIC_DIR" || return 1
  {
    printf 'Podlaz release laptop acceptance\nResult: %s\n' "$qualification"
    jq -r '.scenarios|to_entries|sort_by(.key)[]|"\(.key): \(.value.outcome//"NOT_EXERCISED")"' "$RA_CHECKPOINT"
  } >"$summary" || return 1
  jq --arg q "$qualification" '{schema_version:"podlaz.release-acceptance-report.v3",qualification:$q,soak_minutes:.private.run_config.soak_minutes,scenarios:(.scenarios|with_entries(.value={outcome:(.value.outcome//"NOT_EXERCISED"),state:(.value.state//"")}))}' "$RA_CHECKPOINT" >"$report" || return 1
  jq -n --arg arch "$(uname -m 2>/dev/null || true)" --arg kernel "$(uname -r 2>/dev/null || true)" --argjson soak "$RA_SOAK_MINUTES" '{schema_version:"podlaz.release-requirements-observation.v1",classification:"single-host-observation",architecture:$arch,kernel:$kernel,configured_soak_minutes:$soak}' >"$req" || return 1
  chmod 0600 "$summary" "$report" "$req" || return 1
  if ((EUID==0)) && [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]]; then chown "$RA_UID:$RA_GID" "$summary" "$report" "$req" || return 1; fi
}

ra_qualification() {
  jq -e '.scenarios[]?|select(.outcome=="FAIL")' "$RA_CHECKPOINT" >/dev/null && { printf 'FAIL'; return 0; }
  local required=(lower_release_upgrade privacy_active graceful_restart daemon_kill reinstall rollback_interruption stop_start_no_reconnect preconnect_coexistence active_coexistence resource_soak disconnect_cleanup coexistence_reconnect reconnect_resource_nonaccumulation runtime_terminal_convergence runtime_terminal_no_retry final_restoration) r
  if ((RA_REBOOT_PHASES==1)); then required+=(reboot_autostart_off reboot_autostart_on explicit_disconnect_no_same_boot_retry reboot_terminal_autostart terminal_no_same_boot_retry); fi
  for r in "${required[@]}"; do [[ "$(jq -r --arg r "$r" '.scenarios[$r].outcome//""' "$RA_CHECKPOINT")" == PASS ]] || { printf 'FAIL'; return 0; }; done
  if [[ "$RA_SOAK_MINUTES" != 60 ]] || ((RA_REBOOT_PHASES==0 || RA_ALLOW_WIFI==0 || RA_ALLOW_SUSPEND==0)); then printf 'PARTIAL_PASS'; return 0; fi
  local w s
  w="$(jq -r '.scenarios.wifi_reconnect.outcome//""' "$RA_CHECKPOINT")"; s="$(jq -r '.scenarios.suspend_resume.outcome//""' "$RA_CHECKPOINT")"
  [[ "$w" == PASS || "$w" == SKIP_HOST_CAPABILITY || "$w" == SKIP_REMOTE_SESSION ]] || { printf 'PARTIAL_PASS'; return 0; }
  [[ "$s" == PASS || "$s" == SKIP_HOST_CAPABILITY || "$s" == SKIP_REMOTE_SESSION ]] || { printf 'PARTIAL_PASS'; return 0; }
  printf 'QUALIFIED_PASS'
}

ra_final_restoration_verify() {
  local candidate installed
  candidate="$(jq -ce '.candidate' "$RA_CHECKPOINT")" || return 1
  installed="$(ra_pkg_installed_version)" || return 1
  [[ "$installed" == "$(jq -r '.version' <<<"$candidate")" ]] || return 1
  ra_verify_inactive_boundary || return 1
  ra_privacy_require_ordinary || return 1
  ra_require_mutations_released || return 1
  [[ ! -e "$RA_ROLLBACK_OVERRIDE" && ! -e "$RA_TERMINAL_OVERRIDE" ]]
}

ra_finalize() {
  ra_restore_original_policy || return 1
  ra_final_restoration_verify || { ra_record final_restoration FAIL "restoration verification failed" || true; return 1; }
  ra_record final_restoration PASS || return 1
  local q
  q="$(ra_qualification)" || return 1
  ra_report_write "$q" || return 1
  ra_set_phase complete || return 1
  if [[ "$q" != FAIL ]]; then ra_state_remove || return 1; fi
  printf '%s\n' "$q"
  [[ "$q" != FAIL ]]
}

ra_failure_class() {
  case "$1" in
    signal_*|interrupted*) printf 'INTERRUPTED' ;;
    *ownership*|*ambiguous*|*cleanup*) printf 'OWNERSHIP' ;;
    *schema*|*invariant*|*internal*) printf 'INTERNAL' ;;
    preflight*|input*) printf 'INPUT' ;;
    *) printf 'PRODUCT' ;;
  esac
}

ra_failure_record() {
  local reason="$1" exit_code="$2" class retry occurred scenario
  class="$(ra_failure_class "$reason")"
  retry=RESTART
  [[ "$class" != OWNERSHIP ]] || retry=MANUAL_DIAGNOSIS
  occurred="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  scenario="$(jq -r '.current_scenario//""' "$RA_CHECKPOINT" 2>/dev/null || true)"
  ra_state_jq '.last_failure={step:$step,scenario:$scenario,class:$class,exit_code:$exit,retry_policy:$retry,occurred_at:$at,cleanup_outcome:"pending"}' --arg step "$reason" --arg scenario "$scenario" --arg class "$class" --argjson exit "$exit_code" --arg retry "$retry" --arg at "$occurred"
}

ra_failure_capture_command() {
  local file="$1"
  shift
  local rc
  if ra_capture "$@"; then rc=0; else rc="$RA_CAPTURE_RC"; fi
  { printf 'rc=%s\n' "$rc"; printf '%s\n' "$RA_CAPTURE"; } >"$file" 2>/dev/null || true
  chmod 0600 "$file" 2>/dev/null || true
  return 0
}

ra_failure_copy_private_file() {
  local source="$1" target="$2" size
  [[ -f "$source" && ! -L "$source" ]] || return 0
  size="$(stat -Lc '%s' "$source" 2>/dev/null || printf '0')"
  [[ "$size" =~ ^[0-9]+$ && "$size" -le 1048576 ]] || return 0
  cat "$source" >"$target" 2>/dev/null || return 0
  chmod 0600 "$target" 2>/dev/null || true
}

ra_failure_bundle_capture() {
  local reason="${1:-failure}" exit_code="${2:-1}" bundle started path base
  [[ -n "$RA_PRIVATE_DIR" && -d "$RA_PRIVATE_DIR" ]] || return 0
  bundle="$RA_PRIVATE_DIR/failure"
  mkdir -p "$bundle" 2>/dev/null || return 0
  chmod 0700 "$bundle" 2>/dev/null || true
  cat "$RA_CHECKPOINT" >"$bundle/checkpoint-at-failure.json" 2>/dev/null || true
  chmod 0600 "$bundle/checkpoint-at-failure.json" 2>/dev/null || true
  if [[ -f "$RA_TRANSCRIPT" ]]; then tail -n 200 "$RA_TRANSCRIPT" >"$bundle/last-commands.log" 2>/dev/null || true; chmod 0600 "$bundle/last-commands.log" 2>/dev/null || true; fi
  local status
  if status="$(ra_status_json 2>/dev/null)"; then printf '%s\n' "$status" >"$bundle/last-status.json" 2>/dev/null || true; fi
  if ra_product doctor --tun --json >/dev/null; then :; fi
  { printf 'rc=%s\n' "$RA_CAPTURE_RC"; printf '%s\n' "$RA_CAPTURE"; } >"$bundle/doctor-tun.txt" 2>/dev/null || true
  ra_failure_capture_command "$bundle/systemctl-status.txt" systemctl status "$RA_SERVICE" --no-pager --full
  started="$(jq -r '.run_started_at//""' "$RA_CHECKPOINT" 2>/dev/null || true)"
  if [[ -n "$started" ]]; then ra_failure_capture_command "$bundle/journal.txt" journalctl -u "$RA_SERVICE" --since "$started" --no-pager -o short-iso -n 2000; fi
  ra_failure_capture_command "$bundle/package-state.txt" dpkg-query -W '-f=${Status}\t${Version}\t${Architecture}\n' podlaz
  jq '.mutations' "$RA_CHECKPOINT" >"$bundle/mutation-ledger.json" 2>/dev/null || true
  jq '{phase,current_scenario,last_failure,scenarios}' "$RA_CHECKPOINT" >"$bundle/run-state.json" 2>/dev/null || true
  ra_failure_copy_private_file "$RA_CONTINUATION" "$bundle/network-session-continuation.json"
  ra_failure_copy_private_file "$RA_BOOT_ATTEMPT" "$bundle/boot-autostart-attempt.json"
  if [[ -d "$RA_TRANSACTIONS" ]]; then
    mkdir -p "$bundle/transactions" 2>/dev/null || true
    while IFS= read -r -d '' path; do base="$(basename "$path")"; ra_failure_copy_private_file "$path" "$bundle/transactions/$base"; done < <(find "$RA_TRANSACTIONS" -maxdepth 1 -type f -name '*.json' -print0 2>/dev/null)
  fi
  ra_failure_capture_command "$bundle/ip-links.json" ip -j -d link show
  ra_failure_capture_command "$bundle/ip-routes.json" ip -j -4 route show table all
  ra_failure_capture_command "$bundle/ip-rules.txt" ip -4 rule show
  ra_failure_capture_command "$bundle/nft-ruleset.json" nft -j list ruleset
  ra_failure_capture_command "$bundle/resolved-status.txt" resolvectl status --no-pager
  jq -n --arg reason "$reason" --argjson exit "$exit_code" '{reason:$reason,exit_code:$exit}' >"$bundle/failure-envelope.json" 2>/dev/null || true
  chmod -R go-rwx "$bundle" 2>/dev/null || true
  return 0
}

ra_failure_finalize() {
  local reason="${1:-unexpected_failure}" exit_code="${2:-1}"
  if ((RA_FINALIZER_ACTIVE==1)); then return 1; fi
  ra_checkpoint_exists >/dev/null 2>&1 || return 1
  RA_FINALIZER_ACTIVE=1
  if ! ra_failure_record "$reason" "$exit_code"; then RA_FINALIZER_ACTIVE=0; return 1; fi
  ra_failure_bundle_capture "$reason" "$exit_code" || true
  if ! ra_state_jq '.phase="failure-cleanup-running"'; then RA_FINALIZER_ACTIVE=0; return 1; fi
  if ra_safe_cleanup; then
    if ! ra_state_jq '.phase="failed-clean"|.last_failure.cleanup_outcome="clean"'; then RA_FINALIZER_ACTIVE=0; return 1; fi
    if ! ra_report_write FAILED_CLEAN; then
      ra_state_jq '.phase="fail-cleanup-failed"|.last_failure.class="OWNERSHIP"|.last_failure.cleanup_outcome="report_failed"|.last_failure.retry_policy="MANUAL_DIAGNOSIS"' || true
      printf 'FAIL_CLEANUP_FAILED\n'
      RA_FINALIZER_ACTIVE=0
      return 1
    fi
    if ! ra_state_remove; then RA_FINALIZER_ACTIVE=0; return 1; fi
    printf 'FAILED_CLEAN\n'
    RA_FINALIZER_ACTIVE=0
    return 1
  fi
  ra_state_jq '.phase="fail-cleanup-failed"|.last_failure.class="OWNERSHIP"|.last_failure.cleanup_outcome="failed"|.last_failure.retry_policy="MANUAL_DIAGNOSIS"' || true
  ra_report_write FAIL_CLEANUP_FAILED || true
  printf 'FAIL_CLEANUP_FAILED\n'
  RA_FINALIZER_ACTIVE=0
  return 1
}

ra_signal_handler() {
  local signal="$1"
  if ra_checkpoint_exists >/dev/null 2>&1; then ra_failure_finalize "signal_${signal}" 130 || true; fi
  return 1
}

ra_existing_checkpoint_classify() {
  ra_checkpoint_exists >/dev/null 2>&1 || { printf 'none'; return 0; }
  if ! ra_state_require_schema >/dev/null 2>&1; then printf 'ambiguous'; return 0; fi
  local phase state current
  phase="$(jq -r '.phase//""' "$RA_CHECKPOINT")"
  case "$phase" in
    await-reboot-autostart-off|await-reboot-autostart-on|await-reboot-terminal) printf 'reboot-wait'; return 0 ;;
    fail-cleanup-failed) printf 'ambiguous'; return 0 ;;
    failure-cleanup-running|failed-cleanable|scenario-failed) printf 'cleanup-restart'; return 0 ;;
    preparing-lower-release|verifying-reboot-autostart-off|verifying-reboot-autostart-on|verifying-reboot-terminal) printf 'replay-safe'; return 0 ;;
    running-pre-reboot)
      current="$(jq -r '.current_scenario//""' "$RA_CHECKPOINT")"
      if [[ -z "$current" ]]; then printf 'replay-safe'; return 0; fi
      state="$(jq -r --arg n "$current" '.scenarios[$n].state//""' "$RA_CHECKPOINT")"
      case "$state" in pending|prepared|running|verifying|passed|"") printf 'replay-safe' ;; failed) printf 'cleanup-restart' ;; *) printf 'ambiguous' ;; esac
      return 0
      ;;
    complete|aborted-clean|failed-clean) printf 'cleanup-restart'; return 0 ;;
    *) printf 'ambiguous'; return 0 ;;
  esac
}

ra_checkpoint_candidate_rebind() {
  local candidate="$1"
  jq -e --argjson c "$candidate" '.candidate.package==$c.package and .candidate.version==$c.version and .candidate.architecture==$c.architecture and .candidate.sha256==$c.sha256' "$RA_CHECKPOINT" >/dev/null || return 1
  ra_state_jq '.candidate=$c|if .mutations.package_setup then .mutations.package_setup.identity.candidate=$c else . end|if .mutations.candidate_upgrade then .mutations.candidate_upgrade.identity.candidate=$c else . end' --argjson c "$candidate"
}

ra_checkpoint_run_config_compatible() {
  local profile soak wifi suspend reboots
  profile="$(jq -r '.private.selected_profile//.private.run_config.profile//""' "$RA_CHECKPOINT")"
  soak="$(jq -r '.private.run_config.soak_minutes//60' "$RA_CHECKPOINT")"
  wifi="$(jq -r 'if .private.run_config.allow_wifi_reconnect then 1 else 0 end' "$RA_CHECKPOINT")"
  suspend="$(jq -r 'if .private.run_config.allow_suspend then 1 else 0 end' "$RA_CHECKPOINT")"
  reboots="$(jq -r 'if .private.run_config.reboot_phases then 1 else 0 end' "$RA_CHECKPOINT")"
  [[ -z "$RA_PROFILE" || "$RA_PROFILE" == "$profile" ]] || return 1
  [[ "$RA_SOAK_MINUTES" == "$soak" && "$RA_ALLOW_WIFI" == "$wifi" && "$RA_ALLOW_SUSPEND" == "$suspend" && "$RA_REBOOT_PHASES" == "$reboots" ]] || return 1
  if ((RA_ARTIFACT_DIR_EXPLICIT==1)); then [[ "$(readlink -m "$RA_ARTIFACT_DIR")" == "$(readlink -m "$(jq -r '.private.artifact_root' "$RA_CHECKPOINT")")" ]] || return 1; fi
}

ra_reconcile_scenario_mutations() {
  local scenario="$1" entries name kind state
  entries="$(jq -r --arg s "$scenario" '.mutations|to_entries|reverse[]|select((.value.scenario//"")==$s and .value.state!="released")|[.key,.value.kind,.value.state]|@tsv' "$RA_CHECKPOINT")" || return 1
  while IFS=$'\t' read -r name kind state; do
    [[ -n "$name" ]] || continue
    case "$kind" in
      network_fixture) ra_fixture_release "$name" || return 1 ;;
      systemd_dropin) ra_systemd_hook_cleanup "$name" || return 1 ;;
      networkmanager_connection) ra_nm_reconcile "$name" || return 1 ;;
      candidate_package)
        case "$name" in candidate_upgrade) ra_candidate_upgrade_reconcile_cleanup || return 1 ;; reinstall_package) ra_reinstall_package_reconcile_cleanup || return 1 ;; *) return 1 ;; esac
        ;;
      autostart_policy) ra_autostart_release_owned "$name" || return 1 ;;
      *) ra_die "cannot reconcile scenario mutation $name/$kind"; return 1 ;;
    esac
  done <<<"$entries"
}

ra_recover_running_scenario() {
  local name="$1" before current observed stop_completed service_rc fixture_state profile
  case "$name" in
    graceful_restart|daemon_kill)
      ra_privacy_watch_cancel
      ra_wait_active 30 || return 1
      ra_privacy_require_protected || return 1
      ra_scenario_set_state "$name" prepared
      ;;
    stop_start_no_reconnect)
      stop_completed="$(jq -r '.scenarios.stop_start_no_reconnect.private.stop_completed//false' "$RA_CHECKPOINT")"
      if [[ "$stop_completed" == true ]]; then
        if ra_service_is_active; then
          ra_verify_inactive_boundary || return 1
          ra_privacy_require_ordinary || return 1
          ra_record stop_start_no_reconnect PASS
          ra_scenario_set_state "$name" passed
          return 0
        else
          service_rc=$?
          [[ "$service_rc" == 1 ]] || return 1
          ra_capture systemctl start "$RA_SERVICE" || return 1
          ra_wait_inactive 90 || return 1
          ra_privacy_require_ordinary || return 1
          ra_record stop_start_no_reconnect PASS
          ra_scenario_set_state "$name" passed
          return 0
        fi
      fi
      ra_scenario_set_state "$name" prepared
      ;;
    lower_release_upgrade)
      if jq -e '.mutations.candidate_upgrade and .mutations.candidate_upgrade.state!="released"' "$RA_CHECKPOINT" >/dev/null 2>&1; then
        local installed cv
        installed="$(ra_pkg_installed_version)" || return 1
        cv="$(jq -r '.candidate.version' "$RA_CHECKPOINT")"
        if [[ "$installed" == "$cv" ]]; then
          ra_candidate_upgrade_reconcile_cleanup || return 1
          ra_die "lower-release upgrade evidence was interrupted after candidate installation; restart is required"
          return 1
        fi
      fi
      observed="$(ra_session_observe)"
      if [[ "$observed" == active ]]; then ra_disconnect || return 1; ra_wait_inactive 120 || return 1; elif [[ "$observed" != inactive ]]; then return 1; fi
      ra_reconcile_scenario_mutations "$name" || return 1
      ra_scenario_set_state "$name" prepared
      ;;
    reinstall)
      if jq -e '.mutations.reinstall_package and .mutations.reinstall_package.state!="released"' "$RA_CHECKPOINT" >/dev/null 2>&1; then
        ra_reinstall_package_reconcile_cleanup || return 1
        observed="$(ra_session_observe)"
        if [[ "$observed" == active ]]; then ra_disconnect || return 1; ra_wait_inactive 120 || return 1; elif [[ "$observed" != inactive ]]; then return 1; fi
      fi
      ra_scenario_set_state "$name" prepared
      ;;
    disconnect_cleanup)
      fixture_state="$(jq -r '.mutations.fixture_a.state//"released"' "$RA_CHECKPOINT")"
      if [[ "$fixture_state" == released && "$(jq -r '.scenarios.disconnect_cleanup.outcome//""' "$RA_CHECKPOINT")" == PASS && "$(jq -r '.scenarios.coexistence_reconnect.outcome//""' "$RA_CHECKPOINT")" == PASS && "$(jq -r '.scenarios.reconnect_resource_nonaccumulation.outcome//""' "$RA_CHECKPOINT")" == PASS ]]; then
        ra_verify_inactive_boundary || return 1
        ra_privacy_require_ordinary || return 1
        ra_scenario_set_state "$name" passed
        return 0
      fi
      [[ "$fixture_state" != released ]] || { ra_die "disconnect cleanup lost its fixture boundary before evidence completed"; return 1; }
      observed="$(ra_session_observe)"
      if [[ "$observed" == inactive ]]; then
        profile="$(jq -r '.private.selected_profile' "$RA_CHECKPOINT")"
        ra_connect "$profile" || return 1
        ra_wait_active || return 1
        ra_collision_require_disjoint fixture_a || return 1
      elif [[ "$observed" != active ]]; then return 1; fi
      ra_scenario_set_state "$name" prepared
      ;;
    rollback_interruption|preconnect_coexistence|resource_soak|runtime_terminal_convergence|warmed_inactive_candidate_baseline)
      ra_privacy_watch_cancel
      if [[ "$name" == preconnect_coexistence || "$name" == rollback_interruption || "$name" == runtime_terminal_convergence || "$name" == warmed_inactive_candidate_baseline ]]; then
        observed="$(ra_session_observe)"
        if [[ "$observed" == active ]]; then ra_disconnect || return 1; ra_wait_inactive 120 || return 1; elif [[ "$observed" != inactive ]]; then return 1; fi
      fi
      ra_reconcile_scenario_mutations "$name" || return 1
      ra_scenario_set_state "$name" prepared
      ;;
    *) ra_reconcile_scenario_mutations "$name" || return 1; ra_scenario_set_state "$name" prepared ;;
  esac
}

ra_finish_verifying_scenario() {
  local name="$1"
  case "$name" in
    lower_release_upgrade)
      [[ "$(jq -r '.scenarios.lower_release_upgrade.outcome//""' "$RA_CHECKPOINT")" == PASS ]] || return 1
      [[ "$(jq -r '.scenarios.privacy_active.outcome//""' "$RA_CHECKPOINT")" == PASS ]] || return 1
      ra_wait_active 30 || return 1
      ra_privacy_require_protected || return 1
      ;;
    graceful_restart|daemon_kill|reinstall|preconnect_coexistence|resource_soak)
      ra_wait_active 30 || return 1
      ra_privacy_require_protected || return 1
      ;;
    warmed_inactive_candidate_baseline|stop_start_no_reconnect|runtime_terminal_convergence)
      ra_verify_inactive_boundary || return 1
      ;;
    disconnect_cleanup)
      ra_verify_inactive_boundary || return 1
      ra_privacy_require_ordinary || return 1
      ;;
    rollback_interruption) ra_wait_active 30 || return 1; ra_privacy_require_protected || return 1 ;;
    *) ;;
  esac
  ra_scenario_set_state "$name" passed || return 1
  ra_scenario_clear_current || return 1
  RA_CURRENT_SCENARIO=""
}

ra_scenario_run() {
  local name="$1" action="$2" state
  shift 2
  RA_CURRENT_SCENARIO="$name"
  state="$(jq -r --arg n "$name" '.scenarios[$n].state//""' "$RA_CHECKPOINT")" || return 1
  case "$state" in
    passed) ra_scenario_clear_current || return 1; RA_CURRENT_SCENARIO=""; return 0 ;;
    failed) ra_die "scenario $name already failed"; return 1 ;;
    verifying) ra_finish_verifying_scenario "$name"; return $? ;;
    running)
      ra_recover_running_scenario "$name" || { ra_scenario_set_state "$name" failed || true; return 1; }
      state="$(jq -r --arg n "$name" '.scenarios[$n].state//""' "$RA_CHECKPOINT")"
      if [[ "$state" == passed ]]; then ra_scenario_clear_current || return 1; RA_CURRENT_SCENARIO=""; return 0; fi
      if [[ "$state" == verifying ]]; then ra_finish_verifying_scenario "$name"; return $?; fi
      ;;
    "") ra_scenario_set_state "$name" pending || return 1 ;;
    pending|prepared) ;;
    *) ra_die "unsupported scenario state $name/$state"; return 1 ;;
  esac
  state="$(jq -r --arg n "$name" '.scenarios[$n].state//""' "$RA_CHECKPOINT")"
  [[ "$state" == prepared ]] || ra_scenario_set_state "$name" prepared || return 1
  ra_scenario_set_state "$name" running || return 1
  if ! "$action" "$@"; then ra_scenario_set_state "$name" failed || true; return 1; fi
  ra_scenario_set_state "$name" verifying || return 1
  ra_finish_verifying_scenario "$name"
}

ra_scenario_lower_upgrade() {
  local candidate profile cv installed before
  candidate="$(jq -ce '.candidate' "$RA_CHECKPOINT")" || return 1
  profile="$(jq -r '.private.selected_profile' "$RA_CHECKPOINT")"
  cv="$(jq -r '.version' <<<"$candidate")"
  installed="$(ra_pkg_installed_version)" || return 1
  if [[ "$installed" == "$cv" ]]; then
    ra_wait_active 120 || return 1
    ra_package_setup_release_after_candidate || return 1
    ra_profile_validate "$profile" || return 1
    ra_privacy_require_protected || return 1
    ra_record lower_release_upgrade PASS
    ra_record privacy_active PASS
    return 0
  fi
  ra_connect "$profile" || return 1
  ra_wait_active_legacy || return 1
  installed="$(ra_pkg_installed_version)" || return 1
  [[ "$installed" != "$cv" ]] || { ra_die "lower-release boundary was not established"; return 1; }
  before="$(ra_main_pid)" || return 1
  if ra_privacy_local_proof; then ra_record legacy_upgrade_privacy PASS; ra_privacy_watch_start lower_upgrade; fi
  ra_candidate_upgrade_begin "$candidate" "$installed" || return 1
  ra_pkg_install_exact "$candidate" || { ra_privacy_watch_cancel; return 1; }
  ra_state_jq '.mutations.candidate_upgrade.identity.applied=true' || { ra_privacy_watch_cancel; return 1; }
  ra_mut_mark_acquired candidate_upgrade || { ra_privacy_watch_cancel; return 1; }
  ra_wait_new_pid "$before" >/dev/null || { ra_privacy_watch_cancel; return 1; }
  ra_wait_active || { ra_privacy_watch_cancel; return 1; }
  if [[ -n "$RA_PRIVACY_WATCH_PID" ]]; then ra_privacy_watch_stop || return 1; else ra_record legacy_upgrade_privacy SKIP_RELEASE_CAPABILITY "lower release has no Privacy Envelope evidence"; ra_privacy_require_protected || return 1; fi
  ra_package_setup_release_after_candidate || return 1
  ra_profile_validate "$profile" || return 1
  ra_record lower_release_upgrade PASS || return 1
  ra_privacy_require_protected || return 1
  ra_record privacy_active PASS || return 1
  ra_mut_begin_release candidate_upgrade || return 1
  ra_mut_mark_released candidate_upgrade
}

ra_scenario_graceful_restart() { ra_lifecycle_graceful_restart || return 1; ra_record graceful_restart PASS; }
ra_scenario_daemon_kill() { ra_lifecycle_unexpected_death || return 1; ra_record daemon_kill PASS; }
ra_scenario_rollback() { local profile; profile="$(jq -r '.private.selected_profile' "$RA_CHECKPOINT")"; ra_lifecycle_rollback_interruption "$profile" || return 1; ra_record rollback_interruption PASS; }
ra_scenario_stop_start() { ra_lifecycle_stop_start || return 1; ra_record stop_start_no_reconnect PASS; }
ra_scenario_reinstall() { local candidate profile; candidate="$(jq -ce '.candidate' "$RA_CHECKPOINT")"; profile="$(jq -r '.private.selected_profile' "$RA_CHECKPOINT")"; ra_connect "$profile" || return 1; ra_wait_active || return 1; ra_lifecycle_reinstall "$candidate" || return 1; ra_record reinstall PASS; }

ra_scenario_warmed_baseline() {
  local inactive
  ra_disconnect || return 1
  ra_wait_inactive || return 1
  ra_privacy_require_ordinary || return 1
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]]; then sleep 5; fi
  inactive="$(ra_inactive_sample warmed_inactive)" || return 1
  ra_state_jq '.private.warmed_inactive=$v' --argjson v "$inactive" || return 1
  ra_record warmed_inactive_candidate_baseline PASS
}

ra_scenario_preconnect_coexistence() {
  local profile
  profile="$(jq -r '.private.selected_profile' "$RA_CHECKPOINT")"
  ra_fixture_acquire fixture_a || return 1
  ra_connect "$profile" || return 1
  ra_wait_active || return 1
  ra_privacy_require_protected || return 1
  ra_collision_require_disjoint fixture_a || return 1
  ra_record preconnect_coexistence PASS
}

ra_scenario_resource_soak() { ra_soak_run; }

ra_scenario_disconnect_cleanup() {
  local profile
  profile="$(jq -r '.private.selected_profile' "$RA_CHECKPOINT")"
  ra_disconnect || return 1
  ra_wait_inactive || return 1
  ra_privacy_require_ordinary || return 1
  ra_fixture_verify fixture_a || return 1
  ra_record disconnect_cleanup PASS
  ra_connect "$profile" || return 1
  ra_wait_active || return 1
  ra_collision_require_disjoint fixture_a || return 1
  ra_record coexistence_reconnect PASS
  ra_second_session_observe || return 1
  ra_disconnect || return 1
  ra_wait_inactive || return 1
  ra_privacy_require_ordinary || return 1
  ra_fixture_release fixture_a
}

ra_scenario_terminal() {
  local profile
  profile="$(jq -r '.private.selected_profile' "$RA_CHECKPOINT")"
  ra_runtime_terminal_failure "$profile" || return 1
  ra_record runtime_terminal_convergence PASS
  ra_record runtime_terminal_no_retry PASS
}

ra_run_pre_reboot() {
  ra_set_phase running-pre-reboot || return 1
  ra_scenario_run lower_release_upgrade ra_scenario_lower_upgrade || return 1
  ra_scenario_run graceful_restart ra_scenario_graceful_restart || return 1
  ra_scenario_run daemon_kill ra_scenario_daemon_kill || return 1
  ra_scenario_run rollback_interruption ra_scenario_rollback || return 1
  ra_scenario_run stop_start_no_reconnect ra_scenario_stop_start || return 1
  ra_scenario_run reinstall ra_scenario_reinstall || return 1
  ra_scenario_run warmed_inactive_candidate_baseline ra_scenario_warmed_baseline || return 1
  ra_scenario_run preconnect_coexistence ra_scenario_preconnect_coexistence || return 1
  ra_scenario_run resource_soak ra_scenario_resource_soak || return 1
  ra_scenario_run disconnect_cleanup ra_scenario_disconnect_cleanup || return 1
  ra_scenario_run runtime_terminal_convergence ra_scenario_terminal || return 1
  if ((RA_REBOOT_PHASES==0)); then
    ra_record reboot_autostart_off SKIP_USER_REQUEST
    ra_record reboot_autostart_on SKIP_USER_REQUEST
    ra_record explicit_disconnect_no_same_boot_retry SKIP_USER_REQUEST
    ra_record reboot_terminal_autostart SKIP_USER_REQUEST
    ra_record terminal_no_same_boot_retry SKIP_USER_REQUEST
    ra_finalize
  else
    ra_prepare_reboot_off
  fi
}

ra_prepare_reboot_off() {
  local boot
  RA_CURRENT_SCENARIO=reboot_autostart_off
  ra_autostart_set_owned autostart_disable disable || return 1
  RA_CURRENT_SCENARIO=""
  boot="$(ra_boot_id)" || return 1
  ra_state_jq '.previous_boot_id=$boot|.phase="await-reboot-autostart-off"' --arg boot "$boot" || return 1
  printf 'Release acceptance checkpoint saved.\nReboot the laptop, then run: sudo ./release-laptop.sh --resume\n'
}

ra_resume_require_new_boot() { local old current; old="$(jq -r '.previous_boot_id' "$RA_CHECKPOINT")"; current="$(ra_boot_id)"; [[ -n "$old" && "$current" != "$old" ]] || { ra_die "--resume requires a real reboot with a new boot_id"; return 1; }; }
ra_resume_verify_candidate() { local candidate installed; candidate="$(jq -ce '.candidate' "$RA_CHECKPOINT")" || return 1; installed="$(ra_pkg_installed_version)" || return 1; [[ "$installed" == "$(jq -r '.version' <<<"$candidate")" ]] || { ra_die "installed candidate changed across reboot"; return 1; }; }
ra_same_boot_restart_stays_inactive() { local before; before="$(ra_main_pid)" || return 1; ra_capture systemctl restart "$RA_SERVICE" || return 1; ra_wait_new_pid "$before" >/dev/null || return 1; ra_wait_inactive 30; }
ra_successful_boot_restart_continuity() { local before; before="$(ra_main_pid)" || return 1; ra_privacy_watch_start reboot_active_restart; ra_capture systemctl restart "$RA_SERVICE" || { ra_privacy_watch_cancel; return 1; }; ra_wait_new_pid "$before" >/dev/null || { ra_privacy_watch_cancel; return 1; }; ra_wait_active 120 || { ra_privacy_watch_cancel; return 1; }; ra_privacy_watch_stop; }

ra_preflight_capabilities() {
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]]; then
    [[ -r /sys/fs/cgroup/cgroup.controllers ]] || { ra_preflight_die "cgroup v2 accounting is required for release qualification"; return 2; }
  fi
  [[ ! -e "$RA_ROLLBACK_OVERRIDE" && ! -L "$RA_ROLLBACK_OVERRIDE" && ! -e "$RA_ROLLBACK_HOOK_DIR" && ! -L "$RA_ROLLBACK_HOOK_DIR" ]] || { ra_preflight_die "rollback fault-injection identity is already occupied"; return 2; }
  [[ ! -e "$RA_TERMINAL_OVERRIDE" && ! -L "$RA_TERMINAL_OVERRIDE" && ! -e "$RA_TERMINAL_HOOK_DIR" && ! -L "$RA_TERMINAL_HOOK_DIR" ]] || { ra_preflight_die "terminal fault-injection identity is already occupied"; return 2; }
  local spec
  spec="$(ra_fixture_spec fixture_a)" || return 2
  ra_fixture_assert_free "$spec" || { ra_preflight_die "fixture_a identity is not free"; return 2; }
  spec="$(ra_fixture_spec fixture_b)" || return 2
  ra_fixture_assert_free "$spec" || { ra_preflight_die "fixture_b identity is not free"; return 2; }
}

ra_run_new_fresh() {
  local candidate previous="" installed manifest run_id baseline profile
  candidate="$(ra_pkg_inspect "$RA_CANDIDATE")" || return $?
  [[ -z "$RA_PREVIOUS_DEB" ]] || previous="$(ra_pkg_inspect "$RA_PREVIOUS_DEB")" || return $?
  installed="$(ra_pkg_installed_version)" || return 1
  ra_preflight_release_boundary "$candidate" "$previous" "$installed" || return $?
  ra_preflight_clean_boundary "$installed" || return $?
  ra_validate_artifact_root "$RA_ARTIFACT_DIR" || return $?
  profile="$(ra_profile_select "$RA_PROFILE")" || { ra_preflight_die "profile selection/validation failed"; return 2; }
  ra_preflight_capabilities || return $?
  manifest="$(ra_boot_manifest_capture)" || return 2
  baseline="$(ra_privacy_baseline)" || { ra_preflight_die "direct-egress baseline could not be established"; return 2; }
  run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"
  ra_artifacts_init_new "$run_id" || return $?
  ra_state_init "$run_id" "$candidate" "$installed" "$manifest" "$profile" || return 1
  ra_state_jq '.private.privacy_baseline=$baseline|.private.selected_profile=$profile|.private.run_config.previous=$previous' --argjson baseline "$baseline" --arg profile "$profile" --argjson previous "${previous:-null}" || return 1
  ra_package_setup_prepare "$candidate" "$previous" "$installed" || return $?
  ra_run_pre_reboot
}

ra_retire_existing_run() {
  local result="$1"
  ra_artifacts_from_state || return 1
  ra_failure_bundle_capture "retire_${result}" 1 || true
  if ! ra_safe_cleanup; then
    ra_state_jq '.phase="fail-cleanup-failed"' || true
    ra_report_write FAIL_CLEANUP_FAILED || true
    printf 'FAIL_CLEANUP_FAILED\n'
    return 1
  fi
  ra_set_phase "${result,,}" || return 1
  ra_report_write "$result" || return 1
  ra_state_remove
}

ra_run_new() {
  local classification candidate
  if ! ra_checkpoint_exists >/dev/null 2>&1; then ra_run_new_fresh; return $?; fi
  classification="$(ra_existing_checkpoint_classify)"
  case "$classification" in
    reboot-wait)
      printf 'PAUSED: reboot required\nNext: sudo ./release-laptop.sh --resume\nTo discard this reboot evidence explicitly: sudo ./release-laptop.sh %q --restart\n' "$RA_CANDIDATE"
      return "$RA_RC_PAUSED"
      ;;
    ambiguous)
      ra_die "existing acceptance checkpoint has ambiguous ownership/state; use diagnostics or explicit --abort only after resolving authority"
      return 1
      ;;
    replay-safe)
      candidate="$(ra_pkg_inspect "$RA_CANDIDATE")" || return $?
      ra_checkpoint_run_config_compatible || { ra_die "new invocation is incompatible with replay-safe checkpoint"; return 1; }
      ra_checkpoint_candidate_rebind "$candidate" || { ra_die "candidate does not match replay-safe checkpoint"; return 1; }
      ra_artifacts_from_state || return 1
      ra_run_resume
      ;;
    cleanup-restart)
      ra_retire_existing_run RESTARTED_CLEAN || return 1
      ra_run_new_fresh
      ;;
    *) ra_die "unsupported checkpoint classification: $classification"; return 1 ;;
  esac
}

ra_resume_load_config() {
  RA_SOAK_MINUTES="$(jq -r '.private.run_config.soak_minutes//60' "$RA_CHECKPOINT")"
  RA_ALLOW_WIFI="$(jq -r 'if .private.run_config.allow_wifi_reconnect then 1 else 0 end' "$RA_CHECKPOINT")"
  RA_ALLOW_SUSPEND="$(jq -r 'if .private.run_config.allow_suspend then 1 else 0 end' "$RA_CHECKPOINT")"
  RA_REBOOT_PHASES="$(jq -r 'if .private.run_config.reboot_phases then 1 else 0 end' "$RA_CHECKPOINT")"
}

ra_resume_reboot_off_verify() {
  ra_resume_verify_candidate || return 1
  ra_wait_inactive 120 || return 1
  ra_verify_ordinary_network || return 1
  ra_record reboot_autostart_off PASS
  local profile current
  profile="$(jq -r '.private.selected_profile//""' "$RA_CHECKPOINT")"
  RA_CURRENT_SCENARIO=reboot_autostart_on
  ra_autostart_set_owned autostart_enable enable "$profile" || return 1
  RA_CURRENT_SCENARIO=""
  current="$(ra_boot_id)"
  ra_state_jq '.previous_boot_id=$boot|.phase="await-reboot-autostart-on"' --arg boot "$current" || return 1
  printf 'Release acceptance checkpoint advanced.\nReboot the laptop again, then run: sudo ./release-laptop.sh --resume\n'
}

ra_resume_reboot_on_verify() {
  ra_resume_verify_candidate || return 1
  ra_wait_active 180 || return 1
  ra_privacy_require_protected || return 1
  ra_successful_boot_restart_continuity || return 1
  ra_record reboot_autostart_on PASS
  ra_disconnect || return 1
  ra_wait_inactive || return 1
  ra_privacy_require_ordinary || return 1
  ra_same_boot_restart_stays_inactive || return 1
  ra_record explicit_disconnect_no_same_boot_retry PASS
  local terminal_id current
  terminal_id="$(ra_terminal_profile_create)" || return 1
  RA_CURRENT_SCENARIO=reboot_terminal_autostart
  ra_autostart_set_owned autostart_terminal enable "$terminal_id" || return 1
  RA_CURRENT_SCENARIO=""
  current="$(ra_boot_id)"
  ra_state_jq '.previous_boot_id=$boot|.phase="await-reboot-terminal"' --arg boot "$current" || return 1
  printf 'Release acceptance checkpoint advanced.\nReboot the laptop again, then run: sudo ./release-laptop.sh --resume\n'
}

ra_resume_reboot_terminal_verify() {
  ra_resume_verify_candidate || return 1
  ra_wait_inactive 180 || return 1
  [[ -f "$RA_BOOT_ATTEMPT" && ! -L "$RA_BOOT_ATTEMPT" ]] || return 1
  local attempt reason
  attempt="$(jq -ce . "$RA_BOOT_ATTEMPT")" || return 1
  [[ "$(jq -r '.state//""' <<<"$attempt")" == terminal ]] || return 1
  reason="$(jq -r '.terminal_reason//""' <<<"$attempt")"
  [[ "$reason" == connect_failed ]] || { ra_die "terminal autostart reason is not connect_failed"; return 1; }
  ra_privacy_require_ordinary || return 1
  ra_record reboot_terminal_autostart PASS "" "$(jq -c '{state,terminal_reason}' <<<"$attempt")"
  ra_same_boot_restart_stays_inactive || return 1
  ra_record terminal_no_same_boot_retry PASS
  ra_finalize
}

ra_run_resume() {
  ra_checkpoint_exists || { ra_preflight_die "no acceptance checkpoint exists"; return 2; }
  ra_state_require_schema || return 1
  ra_artifacts_from_state || return 1
  ra_resume_load_config || return 1
  local phase candidate previous installed
  phase="$(jq -r '.phase' "$RA_CHECKPOINT")"
  case "$phase" in
    preparing-lower-release)
      candidate="$(jq -ce '.candidate' "$RA_CHECKPOINT")" || return 1
      previous="$(jq -ce '.private.run_config.previous//empty' "$RA_CHECKPOINT" 2>/dev/null || true)"
      ra_package_setup_reconcile_abort || return 1
      installed="$(ra_pkg_installed_version)" || return 1
      ra_package_setup_prepare "$candidate" "$previous" "$installed" || return 1
      ra_run_pre_reboot
      ;;
    running-pre-reboot) ra_run_pre_reboot ;;
    await-reboot-autostart-off)
      ra_resume_require_new_boot || return 1
      ra_set_phase verifying-reboot-autostart-off || return 1
      ra_resume_reboot_off_verify
      ;;
    verifying-reboot-autostart-off) ra_resume_reboot_off_verify ;;
    await-reboot-autostart-on)
      ra_resume_require_new_boot || return 1
      ra_set_phase verifying-reboot-autostart-on || return 1
      ra_resume_reboot_on_verify
      ;;
    verifying-reboot-autostart-on) ra_resume_reboot_on_verify ;;
    await-reboot-terminal)
      ra_resume_require_new_boot || return 1
      ra_set_phase verifying-reboot-terminal || return 1
      ra_resume_reboot_terminal_verify
      ;;
    verifying-reboot-terminal) ra_resume_reboot_terminal_verify ;;
    *) ra_die "unsupported persisted phase: $phase"; return 1 ;;
  esac
}

ra_run_abort() {
  ra_checkpoint_exists || { ra_preflight_die "no acceptance checkpoint exists"; return 2; }
  ra_state_require_schema || return 1
  ra_artifacts_from_state || return 1
  ra_failure_bundle_capture explicit_abort 1 || true
  if ! ra_safe_cleanup; then
    ra_set_phase fail-cleanup-failed || true
    ra_report_write FAIL_CLEANUP_FAILED || true
    printf 'FAIL_CLEANUP_FAILED\n'
    return 1
  fi
  ra_verify_inactive_boundary || return 1
  ra_privacy_require_ordinary || return 1
  ra_set_phase aborted-clean || return 1
  ra_report_write ABORTED_CLEAN || return 1
  ra_state_remove || return 1
  printf 'ABORTED_CLEAN\n'
}

ra_run_restart() {
  if ra_checkpoint_exists >/dev/null 2>&1; then
    ra_state_require_schema || return 1
    ra_retire_existing_run RESTARTED_CLEAN || return 1
  fi
  RA_MODE=new
  ra_run_new_fresh
}

ra_phase_is_expected_pause() {
  local phase
  phase="$(jq -r '.phase//""' "$RA_CHECKPOINT" 2>/dev/null || true)"
  case "$phase" in await-reboot-autostart-off|await-reboot-autostart-on|await-reboot-terminal) return 0 ;; *) return 1 ;; esac
}

ra_main() {
  local rc
  ra_cli_parse "$@"
  rc=$?
  if [[ "$rc" == 64 ]]; then ra_usage; return 0; fi
  ((rc==0)) || { ra_usage >&2; return "$rc"; }
  ra_require_root_and_user || return $?
  ra_init_paths
  ra_require_tools || return $?
  ra_lock_acquire || return 1
  trap 'ra_signal_handler INT; exit 130' INT
  trap 'ra_signal_handler TERM; exit 143' TERM
  case "$RA_MODE" in
    new) ra_run_new; rc=$? ;;
    resume) ra_run_resume; rc=$? ;;
    abort) ra_run_abort; rc=$? ;;
    restart) ra_run_restart; rc=$? ;;
    *) rc=1 ;;
  esac
  trap - INT TERM
  if ((rc!=0)) && ((rc!=RA_RC_PAUSED)) && [[ "$RA_MODE" != abort ]] && ra_checkpoint_exists >/dev/null 2>&1 && ! ra_phase_is_expected_pause; then
    ra_failure_finalize "operation_failed" "$rc" || true
    return 1
  fi
  return "$rc"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  ra_main "$@"
  exit $?
fi
