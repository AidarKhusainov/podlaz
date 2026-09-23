RA_SCHEMA="podlaz.release-acceptance-checkpoint.v5"
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
RA_SUPPRESS_FINALIZER=0
RA_LAST_FAILURE_REASON=""
RA_SOAK_SAMPLES_FILE=""

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
USAGE
  if [[ "${RA_RELEASE_STANDALONE:-0}" == 1 ]]; then
    printf '%s\n' 'This is a generated standalone Bash file. It does not require a source checkout or Python.'
  else
    printf '%s\n' 'This launcher uses required adjacent Bash modules. It does not require Python.'
  fi
  cat <<'USAGE'
Optional developer diagnostic only; it is not a release gate or required release acceptance.
It never builds Podlaz, downloads packages, uses apt/apt-get, automatically reboots,
or broadly flushes route/rule/nftables state.
USAGE
}

ra_err() { printf 'release-laptop: %s\n' "$*" >&2; }
ra_remember_failure_reason() {
  RA_LAST_FAILURE_REASON="$1"
  if [[ -n "$RA_PRIVATE_DIR" && -d "$RA_PRIVATE_DIR" ]]; then
    printf '%s\n' "$1" | ra_artifact_file_write "$RA_PRIVATE_DIR/last-error-reason" 2>/dev/null || true
  fi
}

ra_die() { ra_remember_failure_reason "$*"; ra_err "$*"; return 1; }
ra_preflight_die() { RA_LAST_FAILURE_REASON="input_preflight"; ra_err "$*"; return 2; }

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
      -h|--help) return 64 ;;
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
  local required=(bash jq flock uname dpkg dpkg-deb dpkg-query sha256sum stat systemctl journalctl curl ip nft resolvectl getent runuser base64 awk sed grep find mktemp ps kill sleep date readlink dirname basename head tail cat mv chmod chown mkdir rm rmdir sync wc cut tr timeout sort)
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

ra_artifact_as_user() {
  if ((EUID == 0)) && [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]] && [[ "$RA_UID" != 0 ]]; then
    runuser -u "$RA_USER" -- "$@"
  else
    "$@"
  fi
}

ra_artifact_write_user_path() {
  local path="$1"
  if ((EUID == 0)) && [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]] && [[ "$RA_UID" != 0 ]]; then
    runuser -u "$RA_USER" -- bash -c 'cat >"$1"' _ "$path"
  else
    cat >"$path"
  fi
}

ra_artifact_append_user_path() {
  local path="$1"
  if ((EUID == 0)) && [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]] && [[ "$RA_UID" != 0 ]]; then
    runuser -u "$RA_USER" -- bash -c 'cat >>"$1"' _ "$path"
  else
    cat >>"$path"
  fi
}

ra_artifact_dir_validate() {
  local path="$1" uid gid mode
  [[ -d "$path" && ! -L "$path" ]] || return 1
  uid="$(stat -Lc '%u' "$path")" || return 1
  gid="$(stat -Lc '%g' "$path")" || return 1
  mode="$(stat -Lc '%a' "$path")" || return 1
  [[ "$uid" == "$RA_UID" && "$gid" == "$RA_GID" && "$mode" == 700 ]]
}

ra_artifact_file_validate() {
  local path="$1" uid gid mode
  [[ -f "$path" && ! -L "$path" ]] || return 1
  uid="$(stat -Lc '%u' "$path")" || return 1
  gid="$(stat -Lc '%g' "$path")" || return 1
  mode="$(stat -Lc '%a' "$path")" || return 1
  [[ "$uid" == "$RA_UID" && "$gid" == "$RA_GID" && "$mode" == 600 ]]
}

ra_artifact_parent_validate() {
  local path="$1" root parent canonical
  root="$(readlink -m -- "$RA_ARTIFACT_DIR")" || return 1
  canonical="$(readlink -m -- "$path")" || return 1
  case "$canonical" in "$root"|"$root"/*) ;; *) return 1 ;; esac
  parent="$(dirname -- "$canonical")"
  [[ "$canonical" != "$root" ]] || return 0
  ra_artifact_dir_validate "$parent"
}

ra_secure_user_mkdir() {
  local path="$1"
  if [[ -e "$path" || -L "$path" ]]; then
    ra_artifact_dir_validate "$path" || { ra_die "refuse unsafe or drifted artifact directory: $path"; return 1; }
    return 0
  fi
  ra_artifact_as_user mkdir -p -- "$path" || return 1
  ra_artifact_as_user chmod 0700 -- "$path" || return 1
  ra_artifact_dir_validate "$path" || { ra_die "artifact directory identity is invalid after creation: $path"; return 1; }
}

ra_artifact_dir_ensure() {
  local path="$1"
  ra_artifact_parent_validate "$path" || return 1
  if [[ -e "$path" || -L "$path" ]]; then
    ra_artifact_dir_validate "$path"
    return $?
  fi
  ra_artifact_as_user mkdir -- "$path" || return 1
  ra_artifact_as_user chmod 0700 -- "$path" || return 1
  ra_artifact_dir_validate "$path"
}

ra_artifact_file_write() {
  local path="$1" parent base tmp
  ra_artifact_parent_validate "$path" || return 1
  if [[ -e "$path" || -L "$path" ]]; then
    ra_artifact_file_validate "$path" || return 1
  fi
  parent="$(dirname -- "$path")"
  base="$(basename -- "$path")"
  tmp="$(ra_artifact_as_user mktemp "$parent/.${base}.tmp.XXXXXX")" || return 1
  ra_artifact_as_user chmod 0600 -- "$tmp" || { ra_artifact_as_user rm -f -- "$tmp"; return 1; }
  ra_artifact_file_validate "$tmp" || { ra_artifact_as_user rm -f -- "$tmp"; return 1; }
  if ! ra_artifact_write_user_path "$tmp"; then
    ra_artifact_as_user rm -f -- "$tmp" || true
    return 1
  fi
  sync -f "$tmp" 2>/dev/null || true
  ra_artifact_as_user mv -f -- "$tmp" "$path" || { ra_artifact_as_user rm -f -- "$tmp" || true; return 1; }
  ra_artifact_file_validate "$path" || return 1
  sync -f "$parent" 2>/dev/null || true
}

ra_artifact_file_append() {
  local path="$1"
  if [[ ! -e "$path" && ! -L "$path" ]]; then
    ra_artifact_file_write "$path"
    return $?
  fi
  ra_artifact_parent_validate "$path" || return 1
  ra_artifact_file_validate "$path" || return 1
  ra_artifact_append_user_path "$path" || return 1
  ra_artifact_file_validate "$path"
}

ra_artifacts_init_new() {
  local run_id="$1"
  ra_validate_artifact_root "$RA_ARTIFACT_DIR" || return $?
  RA_PRIVATE_DIR="$RA_ARTIFACT_DIR/$run_id/private"
  RA_PUBLIC_DIR="$RA_ARTIFACT_DIR/$run_id/public"
  RA_TRANSCRIPT="$RA_PRIVATE_DIR/commands.log"
  ra_secure_user_mkdir "$RA_ARTIFACT_DIR" || return 1
  ra_artifact_dir_ensure "$RA_ARTIFACT_DIR/$run_id" || return 1
  ra_artifact_dir_ensure "$RA_PRIVATE_DIR" || return 1
  ra_artifact_dir_ensure "$RA_PUBLIC_DIR" || return 1
  printf '' | ra_artifact_file_write "$RA_TRANSCRIPT"
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
  } | ra_artifact_file_append "$RA_TRANSCRIPT"
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

ra_boot_id_normalize() {
  local raw="${1,,}" canonical
  canonical="${raw//-/}"
  [[ "$canonical" =~ ^[0-9a-f]{32}$ ]] || return 1
  printf '%s' "$canonical"
}

ra_service_state_capture() {
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" == 1 ]]; then
    printf '%s' "${RELEASE_ACCEPTANCE_TEST_SERVICE_ACTIVE_BEFORE:-true}"
    return 0
  fi
  if ra_capture systemctl is-active --quiet "$RA_SERVICE"; then printf 'true'; return 0; fi
  [[ "$RA_CAPTURE_RC" == 3 ]] || return 1
  printf 'false'
}

ra_state_init() {
  local run_id="$1" candidate_json="$2" installed_before="$3" manifest_json="$4" profile="$5" boot started service_active_before payload
  boot="$(ra_boot_id)" || return 1
  started="$(date -u +%Y-%m-%dT%H:%M:%SZ)" || return 1
  service_active_before="$(ra_service_state_capture)" || { ra_die "host_state_service_unknown"; return 1; }
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
    --argjson service_active_before "$service_active_before" \
    '{schema_version:$schema,run_id:$run_id,run_started_at:$started,starting_boot_id:$boot,current_boot_id:$boot,previous_boot_id:$boot,phase:$phase,current_scenario:"",last_failure:null,user:{name:$user,uid:$uid,gid:$gid,home:$home},candidate:$candidate,mutations:{},scenarios:{},private:{artifact_root:$artifact,installed_before:$installed,service_active_before:$service_active_before,boot_manifest:$manifest,run_config:{previous_deb:$previous_deb,profile:$profile,soak_minutes:$soak,allow_wifi_reconnect:($wifi==1),allow_suspend:($suspend==1),reboot_phases:($reboots==1)}}}')" || return 1
  ra_state_replace_text "$payload"
}

ra_state_require_schema() {
  jq -e --arg s "$RA_SCHEMA" '.schema_version==$s and (.mutations|type)=="object" and (.scenarios|type)=="object" and (.run_started_at|type)=="string" and (.private.service_active_before|type)=="boolean"' "$RA_CHECKPOINT" >/dev/null || { ra_die "status_contract_incompatible checkpoint"; return 1; }
}

ra_set_phase() { ra_state_jq '.phase=$phase' --arg phase "$1"; }

ra_record() {
  local name="$1" outcome="$2" reason="${3:-}" evidence="${4:-{}}"
  ra_state_jq '.scenarios[$name]=((.scenarios[$name]//{})+{name:$name,outcome:$outcome,reason:$reason,evidence:$evidence})' \
    --arg name "$name" --arg outcome "$outcome" --arg reason "$reason" --argjson evidence "$evidence"
}

ra_scenario_set_state() {
  local name="$1" state="$2"
  case "$state" in pending|prepared|running|verifying|passed|failed) ;; *) ra_die "internal_invalid_scenario_state: $state"; return 1 ;; esac
  ra_state_jq '.scenarios[$name]=((.scenarios[$name]//{name:$name})+{state:$state})|.current_scenario=$name' --arg name "$name" --arg state "$state"
}
ra_scenario_clear_current() { ra_state_jq '.current_scenario=""'; }

ra_mut_begin_acquire() {
  local name="$1" kind="$2" identity="$3" existing
  existing="$(jq -r --arg n "$name" '.mutations[$n].state//"released"' "$RA_CHECKPOINT")" || return 1
  [[ "$existing" == released ]] || { ra_die "ownership mutation $name already owns authority"; return 1; }
  ra_state_jq '.mutations[$name]={state:"acquiring",kind:$kind,scenario:$scenario,identity:$identity}' --arg name "$name" --arg kind "$kind" --arg scenario "$RA_CURRENT_SCENARIO" --argjson identity "$identity"
}
ra_mut_transition() { local name="$1" expected="$2" target="$3" state; state="$(jq -er --arg n "$name" '.mutations[$n].state' "$RA_CHECKPOINT")" || return 1; [[ "$state" == "$expected" ]] || { ra_die "internal mutation $name: expected $expected, got $state"; return 1; }; ra_state_jq '.mutations[$name].state=$target' --arg name "$name" --arg target "$target"; }
ra_mut_mark_acquired() { ra_mut_transition "$1" acquiring acquired; }
ra_mut_begin_release() { ra_mut_transition "$1" acquired releasing; }
ra_mut_mark_released() { local state; state="$(jq -er --arg n "$1" '.mutations[$n].state' "$RA_CHECKPOINT")" || return 1; [[ "$state" == acquiring || "$state" == releasing ]] || { ra_die "internal mutation $1 cannot become released from $state"; return 1; }; ra_state_jq '.mutations[$name].state="released"' --arg name "$1"; }

ra_pkg_sha() { sha256sum -- "$1" | awk '{print $1}'; }
ra_pkg_field() { ra_capture dpkg-deb --field "$1" "$2" || return 1; [[ -n "$RA_CAPTURE" ]] || return 1; printf '%s' "$RA_CAPTURE"; }
ra_pkg_verify_sibling_checksum() { local path="$1" digest="$2" sums result count expected; sums="$(dirname "$path")/SHA256SUMS"; [[ -e "$sums" ]] || return 0; [[ -f "$sums" && ! -L "$sums" ]] || { ra_preflight_die "sibling SHA256SUMS is not a regular file"; return 2; }; result="$(awk -v b="$(basename "$path")" '$2==b || $2=="*"b {c++;v=$1} END{print c+0":"v}' "$sums")" || return 2; count="${result%%:*}"; expected="${result#*:}"; ((count==0)) || [[ "$count" == 1 && "$expected" == "$digest" ]] || { ra_preflight_die "SHA256SUMS record does not match supplied package"; return 2; }; }
ra_pkg_inspect() { local path="$1" abs package version arch native digest dev inode; [[ -f "$path" && ! -L "$path" ]] || { ra_preflight_die "package must be a regular non-symlink file: $path"; return 2; }; abs="$(readlink -f -- "$path")" || return 2; package="$(ra_pkg_field "$abs" Package)" || return 2; version="$(ra_pkg_field "$abs" Version)" || return 2; arch="$(ra_pkg_field "$abs" Architecture)" || return 2; [[ "$package" == podlaz ]] || { ra_preflight_die "unexpected package name: $package"; return 2; }; ra_capture dpkg --print-architecture || return 2; native="$RA_CAPTURE"; [[ "$arch" == "$native" ]] || { ra_preflight_die "package architecture $arch does not match host $native"; return 2; }; digest="$(ra_pkg_sha "$abs")" || return 2; ra_pkg_verify_sibling_checksum "$abs" "$digest" || return $?; dev="$(stat -Lc '%d' "$abs")" || return 2; inode="$(stat -Lc '%i' "$abs")" || return 2; jq -cn --arg path "$abs" --arg package "$package" --arg version "$version" --arg architecture "$arch" --arg sha256 "$digest" --argjson device "$dev" --argjson inode "$inode" '{path:$path,package:$package,version:$version,architecture:$architecture,sha256:$sha256,device:$device,inode:$inode}'; }
ra_pkg_assert_identity() { local identity="$1" path dev inode digest; path="$(jq -r '.path' <<<"$identity")" || return 1; [[ -f "$path" && ! -L "$path" ]] || { ra_die "input supplied package disappeared or changed type"; return 1; }; dev="$(stat -Lc '%d' "$path")" || return 1; inode="$(stat -Lc '%i' "$path")" || return 1; digest="$(ra_pkg_sha "$path")" || return 1; jq -e --argjson d "$dev" --argjson i "$inode" --arg s "$digest" '.device==$d and .inode==$i and .sha256==$s' <<<"$identity" >/dev/null || { ra_die "input supplied package identity changed after preflight"; return 1; }; }
ra_pkg_installed_version() { local status version; if ! ra_capture dpkg-query -W '-f=${Status}\t${Version}\n' podlaz; then if [[ "$RA_CAPTURE_RC" == 1 ]]; then printf ''; return 0; fi; ra_die "host_state_package_database_unknown"; return 1; fi; IFS=$'\t' read -r status version <<<"$RA_CAPTURE"; [[ "$status" == "install ok installed" && -n "$version" ]] || { ra_die "host_state_package_database_not_installed"; return 1; }; printf '%s' "$version"; }
ra_pkg_install_exact() { local identity="$1" path version installed; ra_pkg_assert_identity "$identity" || return 1; path="$(jq -r '.path' <<<"$identity")" || return 1; version="$(jq -r '.version' <<<"$identity")" || return 1; ra_capture dpkg -i "$path" || { ra_die "product dpkg -i supplied Podlaz package failed"; return 1; }; installed="$(ra_pkg_installed_version)" || return 1; [[ "$installed" == "$version" ]] || { ra_die "product installed package version does not match supplied package"; return 1; }; }
ra_pkg_lt() { dpkg --compare-versions "$1" lt "$2"; }
ra_pkg_gt() { dpkg --compare-versions "$1" gt "$2"; }

ra_preflight_release_boundary() { local candidate="$1" previous="$2" installed="$3" cv pv; cv="$(jq -er '.version' <<<"$candidate")" || return 2; if [[ -n "$installed" ]] && ra_pkg_gt "$installed" "$cv"; then ra_preflight_die "installed Podlaz version is newer than candidate; refusing downgrade"; return 2; fi; if [[ -n "$previous" ]]; then pv="$(jq -er '.version' <<<"$previous")" || return 2; ra_pkg_lt "$pv" "$cv" || { ra_preflight_die "--previous-deb is not strictly lower than candidate"; return 2; }; fi; if [[ -n "$installed" && "$installed" != "$cv" ]] && ra_pkg_lt "$installed" "$cv"; then return 0; fi; [[ -n "$previous" ]] || { ra_preflight_die "full lower-release qualification requires an installed lower release or --previous-deb"; return 2; }; }
ra_candidate_fault_seams_verify() { local candidate="$1" path tmp daemon seam missing="" rc=0; ra_pkg_assert_identity "$candidate" || { ra_preflight_die "candidate package identity changed before mandatory seam inspection"; return 2; }; path="$(jq -er '.path' <<<"$candidate")" || return 2; tmp="$(mktemp -d)" || return 2; if ! ra_capture dpkg-deb -x "$path" "$tmp"; then rm -rf -- "$tmp" || true; ra_preflight_die "candidate package could not be extracted for mandatory fault-injection seam inspection"; return 2; fi; daemon="$tmp/usr/bin/podlazd"; [[ -f "$daemon" && ! -L "$daemon" && -x "$daemon" ]] || { rm -rf -- "$tmp" || true; ra_preflight_die "candidate package does not contain expected podlazd"; return 2; }; for seam in PODLAZ_E2E_TUN_ROLLBACK_PAUSE PODLAZ_E2E_TUN_TERMINAL_FAILURE PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE; do if ! grep -aFq -- "$seam" "$daemon"; then missing="$seam"; rc=2; break; fi; done; rm -rf -- "$tmp" || return 2; ((rc==0)) || { ra_preflight_die "candidate daemon lacks mandatory release-acceptance fault-injection seam: $missing"; return 2; }; }
