#!/usr/bin/env bash
set -Eeuo pipefail

RA_SCHEMA="podlaz.release-acceptance-checkpoint.v2"
RA_SERVICE="podlazd.service"
RA_SOCKET="/run/podlaz/podlazd.sock"
RA_CONTINUATION="/run/podlaz/network-session-continuation.json"
RA_TRANSACTIONS="/run/podlaz/transactions"
RA_BOOT_ATTEMPT="/run/podlaz/boot-autostart-attempt.json"
RA_BOOT_MANIFEST="/var/lib/podlaz/boot-autostart-manifest.json"
RA_TERMINAL_URI='vless://00000000-0000-4000-8000-000000000001@vpn.invalid:443?security=tls&type=tcp&sni=vpn.invalid#ReleaseAcceptanceFailure'
RA_TERMINAL_NAME="ReleaseAcceptanceFailure"
RA_PROBE_URL="https://example.com/"

RA_CAPTURE=""
RA_MODE="new"
RA_CANDIDATE=""
RA_PREVIOUS_DEB=""
RA_PROFILE=""
RA_ARTIFACT_DIR=""
RA_SOAK_MINUTES=60
RA_ALLOW_WIFI=1
RA_ALLOW_SUSPEND=1
RA_REBOOT_PHASES=1
RA_USER=""
RA_UID=""
RA_GID=""
RA_HOME=""
RA_STATE_HOME=""
RA_STATE_DIR=""
RA_CHECKPOINT=""
RA_PRIVATE_DIR=""
RA_PUBLIC_DIR=""
RA_TRANSCRIPT=""

ra_usage() {
  cat <<'EOF'
Usage:
  sudo ./release-laptop.sh CANDIDATE.deb [options]
  sudo ./release-laptop.sh --resume
  sudo ./release-laptop.sh --abort

Options:
  --previous-deb PATH       Exact strictly-lower Podlaz .deb when a lower release is not already installed
  --profile ID              Existing TUN-capable profile id; auto-selects only when exactly one is usable
  --artifact-dir PATH       Evidence root (default: original user's state tree)
  --soak-minutes N          Active soak duration, 1..1440; below 60 caps result at PARTIAL_PASS
  --skip-wifi-reconnect     Skip controlled NetworkManager reconnect; caps result at PARTIAL_PASS
  --skip-suspend            Skip bounded rtcwake suspend/resume; caps result at PARTIAL_PASS
  --no-reboot-phases        Skip three real reboot phases; caps result at PARTIAL_PASS
  --resume                  Resume the persisted run after a supported boundary
  --abort                   Restore exact harness-owned state and abandon the run
  -h, --help                Show this help

This is one standalone Bash file. It does not require a source checkout or Python.
It never builds Podlaz, downloads packages, uses apt/apt-get, automatically reboots,
or broadly flushes route/rule/nftables state.
EOF
}

ra_err() { printf 'release-laptop: %s\n' "$*" >&2; }
ra_die() { ra_err "$*"; return 1; }
ra_preflight_die() { ra_err "$*"; return 2; }

ra_cli_parse() {
  local positional=()
  while (($#)); do
    case "$1" in
      --resume) [[ "$RA_MODE" == "new" ]] || { ra_err "run modes are mutually exclusive"; return 2; }; RA_MODE="resume" ;;
      --abort) [[ "$RA_MODE" == "new" ]] || { ra_err "run modes are mutually exclusive"; return 2; }; RA_MODE="abort" ;;
      --previous-deb) shift; (($#)) || { ra_err "--previous-deb requires a path"; return 2; }; RA_PREVIOUS_DEB="$1" ;;
      --profile) shift; (($#)) || { ra_err "--profile requires an id"; return 2; }; RA_PROFILE="$1" ;;
      --artifact-dir) shift; (($#)) || { ra_err "--artifact-dir requires a path"; return 2; }; RA_ARTIFACT_DIR="$1" ;;
      --soak-minutes) shift; (($#)) || { ra_err "--soak-minutes requires an integer"; return 2; }; RA_SOAK_MINUTES="$1" ;;
      --skip-wifi-reconnect) RA_ALLOW_WIFI=0 ;;
      --skip-suspend) RA_ALLOW_SUSPEND=0 ;;
      --no-reboot-phases) RA_REBOOT_PHASES=0 ;;
      -h|--help) ra_usage; exit 0 ;;
      --*) ra_err "unknown option: $1"; return 2 ;;
      *) positional+=("$1") ;;
    esac
    shift
  done

  if [[ "$RA_MODE" == "resume" || "$RA_MODE" == "abort" ]]; then
    if ((${#positional[@]})) || [[ -n "$RA_PREVIOUS_DEB$RA_PROFILE$RA_ARTIFACT_DIR" ]] || [[ "$RA_SOAK_MINUTES" != "60" ]] || ((RA_ALLOW_WIFI == 0 || RA_ALLOW_SUSPEND == 0 || RA_REBOOT_PHASES == 0)); then
      ra_err "--resume/--abort do not accept new-run inputs"
      return 2
    fi
    return 0
  fi

  ((${#positional[@]} == 1)) || { ra_err "candidate .deb is required for a new run"; return 2; }
  RA_CANDIDATE="${positional[0]}"
  [[ "$RA_SOAK_MINUTES" =~ ^[0-9]+$ ]] || { ra_err "--soak-minutes must be an integer"; return 2; }
  ((RA_SOAK_MINUTES >= 1 && RA_SOAK_MINUTES <= 1440)) || { ra_err "--soak-minutes must be between 1 and 1440"; return 2; }
}

ra_require_root_and_user() {
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" == "1" ]]; then
    RA_USER="${SUDO_USER:-tester}"
    RA_UID="${RELEASE_ACCEPTANCE_TEST_UID:-$(id -u)}"
    RA_GID="${RELEASE_ACCEPTANCE_TEST_GID:-$(id -g)}"
    RA_HOME="${RELEASE_ACCEPTANCE_TEST_HOME:-${HOME:-/tmp}}"
    return 0
  fi
  ((EUID == 0)) || { ra_preflight_die "must be run with sudo/root"; return 2; }
  RA_USER="${SUDO_USER:-}"
  [[ -n "$RA_USER" && "$RA_USER" != "root" ]] || { ra_preflight_die "SUDO_USER must identify the original non-root user"; return 2; }
  local passwd_line
  passwd_line="$(getent passwd "$RA_USER")" || { ra_preflight_die "SUDO_USER does not resolve through the account database: $RA_USER"; return 2; }
  IFS=: read -r _ _ RA_UID RA_GID _ RA_HOME _ <<<"$passwd_line"
  [[ "$RA_UID" =~ ^[0-9]+$ && "$RA_UID" != "0" ]] || { ra_preflight_die "original user must not be root"; return 2; }
  [[ -d "$RA_HOME" ]] || { ra_preflight_die "original user home does not exist"; return 2; }
}

ra_init_paths() {
  RA_STATE_HOME="${RELEASE_ACCEPTANCE_STATE_HOME:-$RA_HOME/.local/state}"
  RA_STATE_DIR="$RA_STATE_HOME/podlaz/release-acceptance"
  RA_CHECKPOINT="$RA_STATE_DIR/current.json"
  [[ -n "$RA_ARTIFACT_DIR" ]] || RA_ARTIFACT_DIR="$RA_STATE_DIR/artifacts"
}

ra_require_tools() {
  local required=(bash jq dpkg dpkg-deb dpkg-query sha256sum stat systemctl curl ip nft resolvectl getent runuser base64 awk sed grep find mktemp ps kill sleep date readlink dirname basename head cat mv chmod chown mkdir rm rmdir sync)
  local tool
  for tool in "${required[@]}"; do
    command -v "$tool" >/dev/null 2>&1 || { ra_preflight_die "required host tool is missing: $tool"; return 2; }
  done
}

ra_secure_mkdir() {
  local path="$1"
  mkdir -p -- "$path"
  chmod 0700 -- "$path"
  if ((EUID == 0)) && [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != "1" ]]; then
    chown "$RA_UID:$RA_GID" -- "$path"
  fi
}

ra_artifacts_init_new() {
  local run_id="$1"
  RA_PRIVATE_DIR="$RA_ARTIFACT_DIR/$run_id/private"
  RA_PUBLIC_DIR="$RA_ARTIFACT_DIR/$run_id/public"
  ra_secure_mkdir "$RA_ARTIFACT_DIR"
  ra_secure_mkdir "$RA_ARTIFACT_DIR/$run_id"
  ra_secure_mkdir "$RA_PRIVATE_DIR"
  ra_secure_mkdir "$RA_PUBLIC_DIR"
  RA_TRANSCRIPT="$RA_PRIVATE_DIR/commands.log"
  : >"$RA_TRANSCRIPT"
  chmod 0600 "$RA_TRANSCRIPT"
  if ((EUID == 0)) && [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != "1" ]]; then chown "$RA_UID:$RA_GID" "$RA_TRANSCRIPT"; fi
}

ra_artifacts_from_state() {
  local root run_id
  root="$(jq -er '.private.artifact_root' "$RA_CHECKPOINT")" || return 1
  run_id="$(jq -er '.run_id' "$RA_CHECKPOINT")" || return 1
  RA_ARTIFACT_DIR="$root"
  RA_PRIVATE_DIR="$root/$run_id/private"
  RA_PUBLIC_DIR="$root/$run_id/public"
  RA_TRANSCRIPT="$RA_PRIVATE_DIR/commands.log"
}

ra_log_command() {
  local rc="$1"; shift
  [[ -n "$RA_TRANSCRIPT" ]] || return 0
  {
    printf '[%s] rc=%s argv=' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$rc"
    printf '%q ' "$@"
    printf '\n'
    [[ -z "$RA_CAPTURE" ]] || printf '%s\n' "$RA_CAPTURE"
  } >>"$RA_TRANSCRIPT"
}

ra_capture() {
  local rc out
  set +e
  out="$("$@" 2>&1)"
  rc=$?
  set -e
  RA_CAPTURE="$out"
  ra_log_command "$rc" "$@"
  return "$rc"
}

ra_capture_user() {
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" == "1" ]]; then ra_capture "$@"; return; fi
  local rc out
  set +e
  out="$(runuser -u "$RA_USER" -- env HOME="$RA_HOME" XDG_STATE_HOME="$RA_STATE_HOME" "$@" 2>&1)"
  rc=$?
  set -e
  RA_CAPTURE="$out"
  ra_log_command "$rc" runuser -u "$RA_USER" -- "$@"
  return "$rc"
}

ra_checkpoint_exists() {
  [[ -e "$RA_CHECKPOINT" ]] || return 1
  [[ ! -L "$RA_CHECKPOINT" && -f "$RA_CHECKPOINT" ]] || { ra_die "checkpoint is not a regular file"; return 1; }
}

ra_state_replace_text() {
  local payload="$1" parent tmp
  parent="$(dirname -- "$RA_CHECKPOINT")"
  ra_secure_mkdir "$parent"
  tmp="$(mktemp "$parent/.current.json.XXXXXX")"
  printf '%s\n' "$payload" >"$tmp"
  chmod 0600 "$tmp"
  if ((EUID == 0)) && [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != "1" ]]; then chown "$RA_UID:$RA_GID" "$tmp"; fi
  jq -e . "$tmp" >/dev/null || { rm -f "$tmp"; ra_die "refuse to persist invalid checkpoint JSON"; return 1; }
  sync -f "$tmp" 2>/dev/null || true
  mv -f -- "$tmp" "$RA_CHECKPOINT"
  sync -f "$parent" 2>/dev/null || true
}

ra_state_jq() {
  local filter="$1" payload; shift
  payload="$(jq "$filter" "$@" "$RA_CHECKPOINT")" || return 1
  ra_state_replace_text "$payload"
}

ra_state_remove() {
  [[ -e "$RA_CHECKPOINT" ]] || return 0
  [[ ! -L "$RA_CHECKPOINT" && -f "$RA_CHECKPOINT" ]] || { ra_die "refuse to remove non-regular checkpoint"; return 1; }
  rm -f -- "$RA_CHECKPOINT"
  sync -f "$(dirname -- "$RA_CHECKPOINT")" 2>/dev/null || true
}

ra_boot_id() {
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" == "1" && -n "${RELEASE_ACCEPTANCE_TEST_BOOT_ID:-}" ]]; then printf '%s' "$RELEASE_ACCEPTANCE_TEST_BOOT_ID"; return; fi
  local value
  value="$(cat /proc/sys/kernel/random/boot_id)" || return 1
  [[ -n "$value" ]] || { ra_die "empty Linux boot_id"; return 1; }
  printf '%s' "$value"
}

ra_state_init() {
  local run_id="$1" candidate_json="$2" installed_before="$3" manifest_json="$4" profile="$5" payload boot
  boot="$(ra_boot_id)" || return 1
  payload="$(jq -n \
    --arg schema "$RA_SCHEMA" --arg run_id "$run_id" --arg phase "preparing-lower-release" \
    --arg user "$RA_USER" --argjson uid "$RA_UID" --argjson gid "$RA_GID" --arg home "$RA_HOME" \
    --argjson candidate "$candidate_json" --arg boot_id "$boot" --arg artifact_root "$RA_ARTIFACT_DIR" \
    --arg previous_deb "$RA_PREVIOUS_DEB" --arg profile "$profile" --argjson soak "$RA_SOAK_MINUTES" \
    --argjson wifi "$RA_ALLOW_WIFI" --argjson suspend "$RA_ALLOW_SUSPEND" --argjson reboots "$RA_REBOOT_PHASES" \
    --arg installed_before "$installed_before" --argjson manifest "$manifest_json" \
    '{schema_version:$schema,run_id:$run_id,phase:$phase,user:{name:$user,uid:$uid,gid:$gid,home:$home},candidate:$candidate,previous_boot_id:$boot_id,mutations:{},scenarios:{},private:{artifact_root:$artifact_root,installed_before:$installed_before,boot_manifest:$manifest,run_config:{previous_deb:$previous_deb,profile:$profile,soak_minutes:$soak,allow_wifi_reconnect:($wifi==1),allow_suspend:($suspend==1),reboot_phases:($reboots==1)}}}')" || return 1
  ra_state_replace_text "$payload"
}

ra_state_require_schema() {
  jq -e --arg s "$RA_SCHEMA" '.schema_version==$s and (.mutations|type)=="object" and (.scenarios|type)=="object"' "$RA_CHECKPOINT" >/dev/null || { ra_die "unsupported or corrupt acceptance checkpoint"; return 1; }
}

ra_set_phase() { ra_state_jq '.phase=$phase' --arg phase "$1"; }

ra_record() {
  local name="$1" outcome="$2" reason="${3:-}" evidence="${4:-{}}"
  ra_state_jq '.scenarios[$name]={name:$name,outcome:$outcome,reason:$reason,evidence:$evidence}' --arg name "$name" --arg outcome "$outcome" --arg reason "$reason" --argjson evidence "$evidence"
}

ra_mut_begin_acquire() {
  local name="$1" kind="$2" identity="$3" existing
  existing="$(jq -r --arg n "$name" '.mutations[$n].state // "released"' "$RA_CHECKPOINT")" || return 1
  [[ "$existing" == released ]] || { ra_die "mutation $name already owns authority"; return 1; }
  ra_state_jq '.mutations[$name]={state:"acquiring",kind:$kind,identity:$identity}' --arg name "$name" --arg kind "$kind" --argjson identity "$identity"
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

ra_pkg_field() {
  ra_capture dpkg-deb --field "$1" "$2" || return 1
  [[ -n "$RA_CAPTURE" ]] || return 1
  printf '%s' "$RA_CAPTURE"
}
ra_pkg_sha() { sha256sum -- "$1" | awk '{print $1}'; }

ra_pkg_verify_sibling_checksum() {
  local path="$1" digest="$2" dir base sums result count expected
  dir="$(dirname -- "$path")"; base="$(basename -- "$path")"; sums="$dir/SHA256SUMS"
  [[ -e "$sums" ]] || return 0
  [[ ! -L "$sums" && -f "$sums" ]] || { ra_preflight_die "sibling SHA256SUMS is not a regular file"; return 2; }
  result="$(awk -v b="$base" '$2==b || $2=="*"b {c++;v=$1} END{print c+0":"v}' "$sums")"
  count="${result%%:*}"; expected="${result#*:}"
  if ((count > 0)); then [[ "$count" == 1 && "$expected" == "$digest" ]] || { ra_preflight_die "SHA256SUMS record does not match supplied package"; return 2; }; fi
}

ra_pkg_inspect() {
  local path="$1" abs package version arch native digest dev inode
  [[ -e "$path" && ! -L "$path" && -f "$path" ]] || { ra_preflight_die "package must be a regular non-symlink file: $path"; return 2; }
  abs="$(readlink -f -- "$path")" || { ra_preflight_die "cannot resolve package path: $path"; return 2; }
  package="$(ra_pkg_field "$abs" Package)" || { ra_preflight_die "cannot read package name"; return 2; }
  version="$(ra_pkg_field "$abs" Version)" || { ra_preflight_die "cannot read package version"; return 2; }
  arch="$(ra_pkg_field "$abs" Architecture)" || { ra_preflight_die "cannot read package architecture"; return 2; }
  [[ "$package" == podlaz ]] || { ra_preflight_die "unexpected package name: $package"; return 2; }
  ra_capture dpkg --print-architecture || { ra_preflight_die "cannot read host architecture"; return 2; }
  native="$RA_CAPTURE"
  [[ "$arch" == "$native" ]] || { ra_preflight_die "package architecture $arch does not match host $native"; return 2; }
  digest="$(ra_pkg_sha "$abs")" || return 2
  ra_pkg_verify_sibling_checksum "$abs" "$digest" || return $?
  dev="$(stat -Lc '%d' -- "$abs")"; inode="$(stat -Lc '%i' -- "$abs")"
  jq -cn --arg path "$abs" --arg package "$package" --arg version "$version" --arg architecture "$arch" --arg sha256 "$digest" --argjson device "$dev" --argjson inode "$inode" '{path:$path,package:$package,version:$version,architecture:$architecture,sha256:$sha256,device:$device,inode:$inode}'
}

ra_pkg_assert_identity() {
  local identity="$1" path dev inode digest
  path="$(jq -r '.path' <<<"$identity")" || return 1
  [[ -f "$path" && ! -L "$path" ]] || { ra_die "supplied package disappeared or changed type"; return 1; }
  dev="$(stat -Lc '%d' -- "$path")"; inode="$(stat -Lc '%i' -- "$path")"; digest="$(ra_pkg_sha "$path")"
  jq -e --argjson d "$dev" --argjson i "$inode" --arg s "$digest" '.device==$d and .inode==$i and .sha256==$s' <<<"$identity" >/dev/null || { ra_die "supplied package identity changed after preflight"; return 1; }
}

ra_pkg_installed_version() {
  if ! ra_capture dpkg-query -W '-f=${Status}\t${Version}\n' podlaz; then printf ''; return 0; fi
  local status version
  IFS=$'\t' read -r status version <<<"$RA_CAPTURE"
  [[ "$status" == "install ok installed" && -n "$version" ]] || { ra_die "podlaz package database state is not conclusively installed"; return 1; }
  printf '%s' "$version"
}

ra_pkg_install_exact() {
  local identity="$1" path version installed
  ra_pkg_assert_identity "$identity" || return 1
  path="$(jq -r '.path' <<<"$identity")"; version="$(jq -r '.version' <<<"$identity")"
  ra_capture dpkg -i "$path" || { ra_die "dpkg -i supplied Podlaz package failed: $RA_CAPTURE"; return 1; }
  installed="$(ra_pkg_installed_version)" || return 1
  [[ "$installed" == "$version" ]] || { ra_die "installed package version does not match supplied package"; return 1; }
}
ra_pkg_lt() { dpkg --compare-versions "$1" lt "$2"; }

ra_product() {
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" == "1" ]]; then ra_capture podlaz "$@"; else ra_capture_user /usr/bin/podlaz "$@"; fi
}

ra_profile_ids_json() {
  ra_product profile list --json || return 1
  jq -ce 'select(.schema_version=="v1") | [.profiles[]? | select(type=="object" and .id) | .id]' <<<"$RA_CAPTURE"
}
ra_profile_validate() {
  ra_product profile validate "$1" --mode tun --json || return 1
  jq -e '.schema_version=="v1" and .valid==true' <<<"$RA_CAPTURE" >/dev/null
}
ra_profile_select() {
  local explicit="$1" ids id valid=()
  ids="$(ra_profile_ids_json)" || { ra_die "profile list did not return supported JSON"; return 1; }
  if [[ -n "$explicit" ]]; then
    jq -e --arg id "$explicit" 'index($id)!=null' <<<"$ids" >/dev/null || { ra_die "requested profile does not exist"; return 1; }
    ra_profile_validate "$explicit" || { ra_die "requested profile is not valid for TUN mode"; return 1; }
    printf '%s' "$explicit"; return 0
  fi
  while IFS= read -r id; do [[ -z "$id" ]] || { if ra_profile_validate "$id"; then valid+=("$id"); fi; }; done < <(jq -r '.[]' <<<"$ids")
  ((${#valid[@]} == 1)) || { ra_die "expected exactly one usable TUN profile, found ${#valid[@]}"; return 1; }
  printf '%s' "${valid[0]}"
}
ra_connect() { ra_product connect --mode tun "$1" >/dev/null || { ra_die "Podlaz TUN connect failed: $RA_CAPTURE"; return 1; }; }
ra_disconnect() { ra_product disconnect >/dev/null || { ra_die "Podlaz disconnect failed: $RA_CAPTURE"; return 1; }; }

ra_status_json() {
  ra_capture curl --fail --silent --max-time 5 --unix-socket "$RA_SOCKET" http://localhost/v1/status || return 1
  jq -ce . <<<"$RA_CAPTURE"
}
ra_wait_status() {
  local connection="$1" tun="$2" verified="$3" timeout="$4" deadline payload
  deadline=$((SECONDS+timeout))
  while ((SECONDS < deadline)); do
    if payload="$(ra_status_json 2>/dev/null)"; then
      if jq -e --arg c "$connection" --arg t "$tun" --argjson v "$verified" '.connection==$c and .tun==$t and (($v|not) or ((.tun_health.state//"")=="verified"))' <<<"$payload" >/dev/null; then return 0; fi
    fi
    sleep 1
  done
  ra_die "daemon status did not converge to $connection/$tun"; return 1
}
ra_wait_active() { ra_wait_status active active true "${1:-120}"; }
ra_wait_inactive() { ra_wait_status inactive disabled false "${1:-90}"; }
ra_main_pid() {
  ra_capture systemctl show -p MainPID --value "$RA_SERVICE" || return 1
  [[ "$RA_CAPTURE" =~ ^[0-9]+$ && "$RA_CAPTURE" -gt 1 ]] || return 1
  printf '%s' "$RA_CAPTURE"
}
ra_wait_new_pid() {
  local old="$1" timeout="${2:-60}" deadline current
  deadline=$((SECONDS+timeout))
  while ((SECONDS < deadline)); do current="$(ra_main_pid 2>/dev/null || true)"; if [[ -n "$current" && "$current" != "$old" ]]; then printf '%s' "$current"; return 0; fi; sleep 1; done
  ra_die "podlazd MainPID did not change"; return 1
}

ra_boot_manifest_capture() {
  if [[ ! -e "$RA_BOOT_MANIFEST" ]]; then jq -cn '{enabled:false}'; return 0; fi
  [[ -f "$RA_BOOT_MANIFEST" && ! -L "$RA_BOOT_MANIFEST" ]] || { ra_die "boot autostart manifest is not a regular file"; return 1; }
  local size mode uid gid sha payload
  size="$(stat -Lc '%s' "$RA_BOOT_MANIFEST")"; ((size <= 65536)) || { ra_die "boot autostart manifest is unexpectedly large"; return 1; }
  mode="$(stat -Lc '%a' "$RA_BOOT_MANIFEST")"; uid="$(stat -Lc '%u' "$RA_BOOT_MANIFEST")"; gid="$(stat -Lc '%g' "$RA_BOOT_MANIFEST")"
  sha="$(ra_pkg_sha "$RA_BOOT_MANIFEST")"; payload="$(base64 -w0 "$RA_BOOT_MANIFEST")"
  jq -cn --arg mode "$mode" --argjson uid "$uid" --argjson gid "$gid" --arg sha "$sha" --arg payload "$payload" '{enabled:true,mode:$mode,uid:$uid,gid:$gid,sha256:$sha,payload_b64:$payload}'
}
ra_boot_manifest_restore() {
  local snap="$1"
  if [[ "$(jq -r '.enabled' <<<"$snap")" != true ]]; then [[ ! -e "$RA_BOOT_MANIFEST" ]] || { ra_die "autostart manifest unexpectedly exists after restoring disabled policy"; return 1; }; return 0; fi
  local dir tmp mode uid gid sha expected
  dir="$(dirname "$RA_BOOT_MANIFEST")"; mkdir -p "$dir"; chmod 0700 "$dir"
  tmp="$(mktemp "$dir/.boot-autostart-manifest.XXXXXX")"
  jq -r '.payload_b64' <<<"$snap" | base64 -d >"$tmp" || { rm -f "$tmp"; ra_die "recorded autostart manifest payload is invalid"; return 1; }
  expected="$(jq -r '.sha256' <<<"$snap")"; sha="$(ra_pkg_sha "$tmp")"; [[ "$sha" == "$expected" ]] || { rm -f "$tmp"; ra_die "recorded autostart manifest checksum mismatch"; return 1; }
  mode="$(jq -r '.mode' <<<"$snap")"; uid="$(jq -r '.uid' <<<"$snap")"; gid="$(jq -r '.gid' <<<"$snap")"
  chmod "$mode" "$tmp"; chown "$uid:$gid" "$tmp" 2>/dev/null || true; sync -f "$tmp" 2>/dev/null || true; mv -f "$tmp" "$RA_BOOT_MANIFEST"; sync -f "$dir" 2>/dev/null || true
  [[ "$(ra_pkg_sha "$RA_BOOT_MANIFEST")" == "$expected" ]] || { ra_die "restored autostart manifest does not match original"; return 1; }
}

ra_privacy_baseline() {
  ra_capture ip -4 route show table main default || { ra_die "cannot read ordinary IPv4 default route"; return 1; }
  local uplink host=example.com port=443 ip
  uplink="$(awk '{for(i=1;i<=NF;i++) if($i=="dev"){print $(i+1); exit}}' <<<"$RA_CAPTURE")"
  [[ -n "$uplink" && "$uplink" != podlaz0 ]] || { ra_die "ordinary direct-uplink baseline is invalid"; return 1; }
  ra_capture getent ahostsv4 "$host" || { ra_die "cannot resolve direct probe target"; return 1; }
  ip="$(awk 'NR==1{print $1}' <<<"$RA_CAPTURE")"
  [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || { ra_die "direct probe did not resolve IPv4"; return 1; }
  ra_capture curl -4 -fsS --interface "$uplink" --connect-timeout 3 --max-time 5 --resolve "$host:$port:$ip" "$RA_PROBE_URL" -o /dev/null || { ra_die "ordinary direct-egress tripwire baseline is not reachable"; return 1; }
  jq -cn --arg uplink "$uplink" --arg host "$host" --argjson port "$port" --arg ip "$ip" '{uplink:$uplink,host:$host,port:$port,ip:$ip}'
}
ra_privacy_direct_probe() {
  local b="$1" uplink host port ip rc
  uplink="$(jq -r '.uplink' <<<"$b")"; host="$(jq -r '.host' <<<"$b")"; port="$(jq -r '.port' <<<"$b")"; ip="$(jq -r '.ip' <<<"$b")"
  set +e; ra_capture curl -4 -fsS --interface "$uplink" --connect-timeout 3 --max-time 5 --resolve "$host:$port:$ip" "$RA_PROBE_URL" -o /dev/null; rc=$?; set -e
  printf '%s' "$rc"
}
ra_privacy_local_proof() {
  [[ -f "$RA_CONTINUATION" && ! -L "$RA_CONTINUATION" ]] || return 1
  local protection table tun bootstrap_count nftjson rule_count bootstrap_rules
  protection="$(jq -ce 'select(.schema_version=="podlaz.network-session-state.v1" and .owner=="podlaz" and (.intent=="resume" or .intent=="terminal")) | .protection | select(type=="object" and (.state=="armed" or .state=="arming" or .state=="removing") and .composition_version==1 and .family=="inet" and (.bootstrap_ipv4|type)=="array" and (.bootstrap_ipv4|length)>0)' "$RA_CONTINUATION" 2>/dev/null)" || return 1
  table="$(jq -r '.table' <<<"$protection")"; tun="$(jq -r '.tun_interface' <<<"$protection")"
  [[ "$table" =~ ^podlaz_pe_[0-9a-f]{12}(_[1-9][0-9]{0,2})?$ && "$tun" =~ ^[A-Za-z0-9_.:-]+$ ]] || return 1
  bootstrap_count="$(jq '.bootstrap_ipv4|length' <<<"$protection")"
  ra_capture nft -j list table inet "$table" || return 1; nftjson="$RA_CAPTURE"
  jq -e --arg table "$table" '([.nftables[]? | select(has("table")) | .table | select(.family=="inet" and .name==$table)]|length)==1 and ([.nftables[]? | select(has("chain")) | .chain | select(.family=="inet" and .table==$table and .name=="output" and .type=="filter" and .hook=="output" and (.prio|tonumber)==-10)]|length)==1' <<<"$nftjson" >/dev/null || return 1
  rule_count="$(jq --arg table "$table" '[.nftables[]? | .rule? | select(.table==$table)]|length' <<<"$nftjson")" || return 1
  [[ "$rule_count" == "$((bootstrap_count+6))" ]] || return 1
  local owner
  for owner in loopback tun-egress dhcp4 dhcp6 ipv6-link-control block-direct; do
    [[ "$(jq --arg table "$table" --arg c "podlaz:privacy-envelope:$owner" '[.nftables[]? | .rule? | select(.table==$table and .comment==$c)]|length' <<<"$nftjson")" == 1 ]] || return 1
  done
  bootstrap_rules="$(jq --arg table "$table" '[.nftables[]? | .rule? | select(.table==$table and .comment=="podlaz:privacy-envelope:bootstrap")]|length' <<<"$nftjson")"
  [[ "$bootstrap_rules" == "$bootstrap_count" ]]
}
ra_privacy_require_protected() {
  local baseline rc
  baseline="$(jq -ce '.private.privacy_baseline' "$RA_CHECKPOINT")" || { ra_die "privacy baseline missing"; return 1; }
  rc="$(ra_privacy_direct_probe "$baseline")" || return 1
  [[ "$rc" != 0 ]] || { ra_die "direct_egress_leak"; return 1; }
  ra_privacy_local_proof || { ra_die "inconclusive_local_privacy_authority"; return 1; }
}
ra_privacy_require_ordinary() {
  local baseline rc
  baseline="$(jq -ce '.private.privacy_baseline' "$RA_CHECKPOINT")" || return 1
  rc="$(ra_privacy_direct_probe "$baseline")" || return 1
  [[ "$rc" == 0 ]] || { ra_die "ordinary_direct_egress_not_restored"; return 1; }
}

ra_fixture_spec() {
  case "$1" in
    fixture_a) jq -cn '{name:"fixture_a",tun:"podlaz-accept-a0",cidr:"198.18.0.1/32",table:"51820",route:"198.51.100.254/32",rule_a:"198.51.100.254/32",rule_b:"198.51.100.253/32",priority_a:9999,priority_b:10000,nft_table:"podlaz_accept_a",dns_link:"podlaz-accept-adns0",dns_server:"192.0.2.53",dns_domain:"~accept-a.invalid"}' ;;
    fixture_b) jq -cn '{name:"fixture_b",tun:"podlaz-accept-b0",cidr:"198.18.62.1/32",table:"51962",route:"203.0.113.62/32",rule_a:"203.0.113.62/32",rule_b:"203.0.113.63/32",priority_a:10999,priority_b:11000,nft_table:"podlaz_accept_b",dns_link:"podlaz-accept-bdns0",dns_server:"192.0.2.54",dns_domain:"~accept-b.invalid"}' ;;
    *) ra_die "unknown fixture: $1"; return 1 ;;
  esac
}
ra_fixture_assert_free() {
  local spec="$1" value p
  for p in tun dns_link; do value="$(jq -r ".$p" <<<"$spec")"; if ra_capture ip link show dev "$value" && [[ -n "$RA_CAPTURE" ]]; then ra_die "fixture identity already occupied: $value"; return 1; fi; done
  value="$(jq -r '.nft_table' <<<"$spec")"; if ra_capture nft list table inet "$value" && [[ -n "$RA_CAPTURE" ]]; then ra_die "fixture nft table already occupied"; return 1; fi
  for p in priority_a priority_b; do value="$(jq -r ".$p" <<<"$spec")"; ra_capture ip -4 rule show priority "$value" || { ra_die "could not inspect fixture rule priority $value"; return 1; }; [[ -z "$RA_CAPTURE" ]] || { ra_die "fixture rule priority already occupied: $value"; return 1; }; done
  value="$(jq -r '.table' <<<"$spec")"; ra_capture ip -4 route show table "$value" || { ra_die "could not inspect fixture routing table $value"; return 1; }; [[ -z "$RA_CAPTURE" ]] || { ra_die "fixture routing table already occupied: $value"; return 1; }
}
ra_fixture_acquire() {
  local name="$1" spec identity tun cidr table route rule_a rule_b pa pb nft dns server domain
  spec="$(ra_fixture_spec "$name")" || return 1; ra_fixture_assert_free "$spec" || return 1
  identity="$(jq -c '.+{current_route:.route}' <<<"$spec")"; ra_mut_begin_acquire "$name" network_fixture "$identity" || return 1
  tun="$(jq -r '.tun' <<<"$spec")"; cidr="$(jq -r '.cidr' <<<"$spec")"; table="$(jq -r '.table' <<<"$spec")"; route="$(jq -r '.route' <<<"$spec")"; rule_a="$(jq -r '.rule_a' <<<"$spec")"; rule_b="$(jq -r '.rule_b' <<<"$spec")"; pa="$(jq -r '.priority_a' <<<"$spec")"; pb="$(jq -r '.priority_b' <<<"$spec")"; nft="$(jq -r '.nft_table' <<<"$spec")"; dns="$(jq -r '.dns_link' <<<"$spec")"; server="$(jq -r '.dns_server' <<<"$spec")"; domain="$(jq -r '.dns_domain' <<<"$spec")"
  ra_capture ip tuntap add dev "$tun" mode tun || return 1; ra_capture ip link set dev "$tun" up || return 1; ra_capture ip -4 address add "$cidr" dev "$tun" || return 1; ra_capture ip -4 route add blackhole "$route" table "$table" || return 1; ra_capture ip -4 rule add priority "$pa" to "$rule_a" lookup "$table" || return 1; ra_capture ip -4 rule add priority "$pb" to "$rule_b" lookup "$table" || return 1; ra_capture nft add table inet "$nft" || return 1; ra_capture ip link add "$dns" type dummy || return 1; ra_capture ip link set dev "$dns" up || return 1; ra_capture resolvectl dns "$dns" "$server" || return 1; ra_capture resolvectl domain "$dns" "$domain" || return 1; ra_capture resolvectl default-route "$dns" no || return 1
  ra_fixture_verify "$name" || return 1; ra_mut_mark_acquired "$name"
}
ra_fixture_verify() {
  local name="$1" spec tun cidr table route nft dns server
  spec="$(jq -ce --arg n "$name" '.mutations[$n].identity' "$RA_CHECKPOINT")" || return 1
  tun="$(jq -r '.tun' <<<"$spec")"; cidr="$(jq -r '.cidr' <<<"$spec")"; table="$(jq -r '.table' <<<"$spec")"; route="$(jq -r '.current_route' <<<"$spec")"; nft="$(jq -r '.nft_table' <<<"$spec")"; dns="$(jq -r '.dns_link' <<<"$spec")"; server="$(jq -r '.dns_server' <<<"$spec")"
  ra_capture ip link show dev "$tun" || return 1; grep -Fq "$tun" <<<"$RA_CAPTURE" || { ra_die "fixture $name TUN drifted"; return 1; }
  ra_capture ip -4 address show dev "$tun" || return 1; grep -Fq "$cidr" <<<"$RA_CAPTURE" || { ra_die "fixture $name address drifted"; return 1; }
  ra_capture ip -4 route show table "$table" || return 1; grep -Fq "${route%/*}" <<<"$RA_CAPTURE" || { ra_die "fixture $name route drifted"; return 1; }
  ra_capture nft list table inet "$nft" || return 1; grep -Fq "$nft" <<<"$RA_CAPTURE" || { ra_die "fixture $name nft drifted"; return 1; }
  ra_capture resolvectl status "$dns" --no-pager || return 1; grep -Fq "$server" <<<"$RA_CAPTURE" || { ra_die "fixture $name DNS drifted"; return 1; }
}
ra_fixture_churn() {
  local name="$1" spec table current alternate
  spec="$(jq -ce --arg n "$name" '.mutations[$n].identity' "$RA_CHECKPOINT")" || return 1; table="$(jq -r '.table' <<<"$spec")"; current="$(jq -r '.current_route' <<<"$spec")"
  [[ "$name" == fixture_b ]] && alternate="203.0.113.64/32" || alternate="198.51.100.252/32"
  ra_capture ip -4 route replace blackhole "$alternate" table "$table" || return 1; ra_capture ip -4 route del blackhole "$current" table "$table" || return 1
  ra_state_jq '.mutations[$n].identity.current_route=$route' --arg n "$name" --arg route "$alternate"
}
ra_observe_link_exact() {
  local name="$1" kind="$2" count observed ifname
  if ! ra_capture ip -j -d link show dev "$name"; then printf absent; return 0; fi
  count="$(jq 'length' <<<"$RA_CAPTURE" 2>/dev/null)" || { ra_die "invalid link evidence for $name"; return 1; }
  [[ "$count" == 1 ]] || { ra_die "ambiguous link evidence for $name"; return 1; }
  ifname="$(jq -r '.[0].ifname//""' <<<"$RA_CAPTURE")"; observed="$(jq -r '.[0].linkinfo.info_kind//""' <<<"$RA_CAPTURE")"
  [[ "$ifname" == "$name" && "$observed" == "$kind" ]] || { ra_die "foreign link occupies $name"; return 1; }
  printf present
}
ra_fixture_release_partial() {
  local name="$1" spec state tun dns table route pa pb rule_a rule_b nft tun_state dns_state route_state=absent rule_a_state=absent rule_b_state=absent nft_state=absent n line
  spec="$(jq -ce --arg n "$name" '.mutations[$n].identity' "$RA_CHECKPOINT")" || { ra_die "fixture $name has no persisted authority"; return 1; }
  state="$(jq -r --arg n "$name" '.mutations[$n].state' "$RA_CHECKPOINT")"; [[ "$state" == acquiring || "$state" == releasing ]] || { ra_die "fixture $name partial cleanup invalid from $state"; return 1; }
  tun="$(jq -r '.tun' <<<"$spec")"; dns="$(jq -r '.dns_link' <<<"$spec")"; table="$(jq -r '.table' <<<"$spec")"; route="$(jq -r '.current_route' <<<"$spec")"; pa="$(jq -r '.priority_a' <<<"$spec")"; pb="$(jq -r '.priority_b' <<<"$spec")"; rule_a="$(jq -r '.rule_a' <<<"$spec")"; rule_b="$(jq -r '.rule_b' <<<"$spec")"; nft="$(jq -r '.nft_table' <<<"$spec")"
  tun_state="$(ra_observe_link_exact "$tun" tun)" || return 1; dns_state="$(ra_observe_link_exact "$dns" dummy)" || return 1
  ra_capture ip -j -4 route show table "$table" || { ra_die "could not inspect fixture routing table $table"; return 1; }
  n="$(jq 'length' <<<"$RA_CAPTURE" 2>/dev/null)" || { ra_die "invalid route evidence"; return 1; }
  if [[ "$n" != 0 ]]; then [[ "$n" == 1 ]] || { ra_die "fixture routing table $table contains foreign state"; return 1; }; jq -e --arg dst "$route" --arg t "$table" '.[0].type=="blackhole" and .[0].dst==$dst and (.[0].table|tostring)==$t' <<<"$RA_CAPTURE" >/dev/null || { ra_die "fixture routing table $table contains foreign state"; return 1; }; route_state=present; fi
  ra_capture ip -4 rule show priority "$pa" || { ra_die "could not inspect fixture rule $pa"; return 1; }; if [[ -n "$RA_CAPTURE" ]]; then [[ "$(grep -c . <<<"$RA_CAPTURE")" == 1 ]] || { ra_die "fixture rule priority $pa contains foreign state"; return 1; }; line="$RA_CAPTURE"; grep -Eq "^${pa}: .*to ${rule_a//./\.} .*lookup $table" <<<"$line" || { ra_die "fixture rule priority $pa contains foreign state"; return 1; }; rule_a_state=present; fi
  ra_capture ip -4 rule show priority "$pb" || { ra_die "could not inspect fixture rule $pb"; return 1; }; if [[ -n "$RA_CAPTURE" ]]; then [[ "$(grep -c . <<<"$RA_CAPTURE")" == 1 ]] || { ra_die "fixture rule priority $pb contains foreign state"; return 1; }; line="$RA_CAPTURE"; grep -Eq "^${pb}: .*to ${rule_b//./\.} .*lookup $table" <<<"$line" || { ra_die "fixture rule priority $pb contains foreign state"; return 1; }; rule_b_state=present; fi
  if ra_capture nft -j list table inet "$nft"; then jq -e --arg n "$nft" '[.nftables[]? | select(has("metainfo")|not)] as $x | ($x|length)==1 and ($x[0].table.family=="inet") and ($x[0].table.name==$n)' <<<"$RA_CAPTURE" >/dev/null || { ra_die "fixture nft table $nft contains foreign state"; return 1; }; nft_state=present; fi
  if [[ "$dns_state" == present ]]; then ra_capture resolvectl revert "$dns" || true; ra_capture ip link del dev "$dns" || return 1; fi
  [[ "$nft_state" == absent ]] || { ra_capture nft delete table inet "$nft" || return 1; }; [[ "$rule_b_state" == absent ]] || { ra_capture ip -4 rule del priority "$pb" to "$rule_b" lookup "$table" || return 1; }; [[ "$rule_a_state" == absent ]] || { ra_capture ip -4 rule del priority "$pa" to "$rule_a" lookup "$table" || return 1; }; [[ "$route_state" == absent ]] || { ra_capture ip -4 route del blackhole "$route" table "$table" || return 1; }; [[ "$tun_state" == absent ]] || { ra_capture ip link del dev "$tun" || return 1; }
  ra_mut_mark_released "$name"
}
ra_fixture_release() {
  local name="$1" state spec tun dns table route pa pb rule_a rule_b nft
  state="$(jq -r --arg n "$name" '.mutations[$n].state//"released"' "$RA_CHECKPOINT")"; [[ "$state" != released ]] || return 0
  if [[ "$state" == acquiring || "$state" == releasing ]]; then ra_fixture_release_partial "$name"; return; fi
  [[ "$state" == acquired ]] || { ra_die "fixture $name has unsupported state: $state"; return 1; }
  ra_fixture_verify "$name" || return 1; ra_mut_begin_release "$name" || return 1; spec="$(jq -ce --arg n "$name" '.mutations[$n].identity' "$RA_CHECKPOINT")"
  tun="$(jq -r '.tun' <<<"$spec")"; dns="$(jq -r '.dns_link' <<<"$spec")"; table="$(jq -r '.table' <<<"$spec")"; route="$(jq -r '.current_route' <<<"$spec")"; pa="$(jq -r '.priority_a' <<<"$spec")"; pb="$(jq -r '.priority_b' <<<"$spec")"; rule_a="$(jq -r '.rule_a' <<<"$spec")"; rule_b="$(jq -r '.rule_b' <<<"$spec")"; nft="$(jq -r '.nft_table' <<<"$spec")"
  ra_capture resolvectl revert "$dns" || return 1; ra_capture ip link del dev "$dns" || return 1; ra_capture nft delete table inet "$nft" || return 1; ra_capture ip -4 rule del priority "$pb" to "$rule_b" lookup "$table" || return 1; ra_capture ip -4 rule del priority "$pa" to "$rule_a" lookup "$table" || return 1; ra_capture ip -4 route del blackhole "$route" table "$table" || return 1; ra_capture ip link del dev "$tun" || return 1; ra_mut_mark_released "$name"
}

ra_systemd_hook_cleanup() {
  local name="$1" state path hook_dir child
  state="$(jq -r --arg n "$name" '.mutations[$n].state' "$RA_CHECKPOINT")"; path="$(jq -r --arg n "$name" '.mutations[$n].identity.path' "$RA_CHECKPOINT")"; hook_dir="$(jq -r --arg n "$name" '.mutations[$n].identity.hook_dir' "$RA_CHECKPOINT")"
  [[ "$state" != acquired ]] || ra_mut_begin_release "$name" || return 1
  rm -f -- "$path"; ra_capture systemctl daemon-reload || return 1
  if [[ -d "$hook_dir" ]]; then while IFS= read -r -d '' child; do [[ -f "$child" && ! -L "$child" ]] || { ra_die "ambiguous hook cleanup entry: $child"; return 1; }; rm -f -- "$child"; done < <(find "$hook_dir" -mindepth 1 -maxdepth 1 -print0); rmdir -- "$hook_dir"; fi
  state="$(jq -r --arg n "$name" '.mutations[$n].state' "$RA_CHECKPOINT")"; if [[ "$state" == acquiring || "$state" == releasing ]]; then ra_mut_mark_released "$name"; fi
}
ra_nm_reconcile() {
  local name="$1" state connection
  state="$(jq -r --arg n "$name" '.mutations[$n].state' "$RA_CHECKPOINT")"; connection="$(jq -r --arg n "$name" '.mutations[$n].identity.connection//""' "$RA_CHECKPOINT")"; [[ -n "$connection" ]] || { ra_die "NetworkManager mutation lost exact connection identity"; return 1; }
  [[ "$state" != acquired ]] || ra_mut_begin_release "$name" || return 1; ra_capture nmcli connection up "$connection" || return 1; state="$(jq -r --arg n "$name" '.mutations[$n].state' "$RA_CHECKPOINT")"; if [[ "$state" == acquiring || "$state" == releasing ]]; then ra_mut_mark_released "$name"; fi
}
ra_terminal_profile_is_exact() {
  ra_product profile show "$1" --json || return 1
  jq -e --arg name "$RA_TERMINAL_NAME" '.schema_version=="v1" and .status=="ok" and .profile.name==$name and .profile.server=="vpn.invalid" and (.profile.port|tonumber)==443 and (.profile.protocol|ascii_downcase)=="vless"' <<<"$RA_CAPTURE" >/dev/null
}
ra_terminal_profile_create() {
  local baseline after additions id imported
  baseline="$(ra_profile_ids_json)" || return 1; ra_state_jq '.private.terminal_profile_acquisition={state:"acquiring",baseline_ids:$ids}' --argjson ids "$baseline" || return 1
  ra_product profile import "$RA_TERMINAL_URI" || return 1; imported="$(sed -n 's/^Imported profile:[[:space:]]*//p' <<<"$RA_CAPTURE" | head -n1)"; after="$(ra_profile_ids_json)" || return 1; additions="$(jq -cn --argjson a "$after" --argjson b "$baseline" '$a-$b')"; [[ "$(jq 'length' <<<"$additions")" == 1 ]] || { ra_die "could not acquire exact terminal profile identity"; return 1; }; id="$(jq -r '.[0]' <<<"$additions")"; [[ -z "$imported" || "$imported" == "$id" ]] || { ra_die "terminal profile import identity disagrees with profile set"; return 1; }; ra_terminal_profile_is_exact "$id" || { ra_die "created terminal profile does not match acceptance fixture"; return 1; }; ra_profile_validate "$id" || return 1
  ra_state_jq '.private.terminal_profile=$id | .private.terminal_profile_acquisition={state:"acquired",baseline_ids:.private.terminal_profile_acquisition.baseline_ids,profile_id:$id}' --arg id "$id" || return 1; printf '%s' "$id"
}
ra_terminal_profile_reconcile() {
  local acq state baseline current additions id legacy
  acq="$(jq -ce '.private.terminal_profile_acquisition//empty' "$RA_CHECKPOINT" 2>/dev/null || true)"; legacy="$(jq -r '.private.terminal_profile//""' "$RA_CHECKPOINT")"
  if [[ -z "$acq" ]]; then [[ -z "$legacy" ]] && return 0; ra_terminal_profile_is_exact "$legacy" || { ra_die "recorded terminal profile is no longer exact"; return 1; }; ra_product profile delete "$legacy" --yes || return 1; ra_state_jq 'del(.private.terminal_profile)'; return; fi
  state="$(jq -r '.state' <<<"$acq")"; baseline="$(jq -c '.baseline_ids' <<<"$acq")"; current="$(ra_profile_ids_json)" || return 1
  if [[ "$state" == acquiring ]]; then additions="$(jq -cn --argjson a "$current" --argjson b "$baseline" '$a-$b')"; [[ "$(jq 'length' <<<"$additions")" == 1 ]] || { if [[ "$(jq 'length' <<<"$additions")" == 0 ]]; then ra_state_jq 'del(.private.terminal_profile_acquisition)'; return; fi; ra_die "terminal profile acquisition is ambiguous"; return 1; }; id="$(jq -r '.[0]' <<<"$additions")"; ra_terminal_profile_is_exact "$id" || { ra_die "new profile after terminal acquisition checkpoint is foreign"; return 1; }; ra_state_jq '.private.terminal_profile=$id | .private.terminal_profile_acquisition.state="acquired" | .private.terminal_profile_acquisition.profile_id=$id' --arg id "$id" || return 1; else id="$(jq -r '.profile_id//""' <<<"$acq")"; [[ -n "$id" && ( -z "$legacy" || "$legacy" == "$id" ) ]] || { ra_die "terminal profile authority is inconsistent"; return 1; }; fi
  if jq -e --arg id "$id" 'index($id)!=null' <<<"$current" >/dev/null; then ra_terminal_profile_is_exact "$id" || { ra_die "recorded terminal profile is foreign"; return 1; }; ra_state_jq '.private.terminal_profile_acquisition.state="releasing"' || return 1; ra_product profile delete "$id" --yes || return 1; fi
  ra_state_jq 'del(.private.terminal_profile,.private.terminal_profile_acquisition)'
}
ra_cleanup_owned_mutations() {
  local entries name kind state
  entries="$(jq -r '.mutations|to_entries|reverse[]|[.key,.value.kind,.value.state]|@tsv' "$RA_CHECKPOINT")"
  while IFS=$'\t' read -r name kind state; do [[ -n "$name" && "$state" != released ]] || continue; case "$kind" in previous_package) ;; network_fixture) ra_fixture_release "$name" || return 1 ;; systemd_dropin) ra_systemd_hook_cleanup "$name" || return 1 ;; networkmanager_connection) ra_nm_reconcile "$name" || return 1 ;; *) ra_die "unsupported owned mutation kind during cleanup: $kind"; return 1 ;; esac; done <<<"$entries"
}
ra_require_mutations_released() {
  local pending; pending="$(jq -r '[.mutations|to_entries[]|select(.value.state!="released")|select(.value.kind!="previous_package")|.key]|join(",")' "$RA_CHECKPOINT")"; [[ -z "$pending" ]] || { ra_die "owned mutation cleanup is incomplete: $pending"; return 1; }
}

ra_lifecycle_graceful_restart() { local before; before="$(ra_main_pid)" || return 1; ra_capture systemctl restart "$RA_SERVICE" || return 1; ra_wait_new_pid "$before" >/dev/null || return 1; ra_wait_active || return 1; ra_privacy_require_protected; }
ra_lifecycle_unexpected_death() { local before; before="$(ra_main_pid)" || return 1; ra_capture systemctl kill --kill-who=main --signal=SIGKILL "$RA_SERVICE" || return 1; ra_wait_new_pid "$before" >/dev/null || return 1; ra_wait_active || return 1; ra_privacy_require_protected; }
ra_lifecycle_stop_start() { ra_capture systemctl stop "$RA_SERVICE" || return 1; ra_capture systemctl start "$RA_SERVICE" || return 1; ra_wait_inactive; }
ra_lifecycle_reinstall() { local before; before="$(ra_main_pid)" || return 1; ra_pkg_install_exact "$1" || return 1; ra_wait_new_pid "$before" >/dev/null || return 1; ra_wait_active || return 1; ra_privacy_require_protected; }
ra_wait_file() { local path="$1" deadline=$((SECONDS+$2)); while ((SECONDS<deadline)); do [[ -f "$path" && ! -L "$path" ]] && return 0; sleep 1; done; ra_die "expected marker did not appear: $(basename "$path")"; return 1; }
ra_exact_rolling_back_count() { [[ -d "$RA_TRANSACTIONS" ]] || { printf 0; return; }; local count=0 path; while IFS= read -r -d '' path; do if jq -e '.owner=="podlaz" and .state=="rolling_back" and (.rollback|type)=="object" and ([.rollback.tun_addresses[]?,.rollback.routes[]?,.rollback.policy_rules[]?,.rollback.dns[]?,.rollback.nftables[]?,.rollback.generated_configs[]?,.rollback.child_processes[]?]|any(.owner=="podlaz"))' "$path" >/dev/null 2>&1; then ((count+=1)); fi; done < <(find "$RA_TRANSACTIONS" -maxdepth 1 -type f -name '*.json' -print0); printf '%s' "$count"; }
ra_lifecycle_rollback_interruption() {
  local hook_dir="/run/podlaz/release-acceptance-rollback" override="/etc/systemd/system/podlazd.service.d/99-release-acceptance-rollback.conf" identity old restart_pid
  identity="$(jq -cn --arg path "$override" --arg hook "$hook_dir" '{path:$path,hook_dir:$hook}')"; ra_mut_begin_acquire rollback_hook systemd_dropin "$identity" || return 1; mkdir -p "$hook_dir" "$(dirname "$override")"; chmod 0700 "$hook_dir"; cat >"$override" <<EOF
[Service]
Environment=PODLAZ_E2E_TUN_ROLLBACK_PAUSE=true
Environment=PODLAZ_E2E_TUN_ROLLBACK_PAUSE_DIR=$hook_dir
Environment=PODLAZ_E2E_TUN_ROLLBACK_PAUSE_TIMEOUT_SECONDS=180
EOF
  ra_capture systemctl daemon-reload || return 1; ra_mut_mark_acquired rollback_hook || return 1; ra_capture systemctl restart "$RA_SERVICE" || return 1; ra_wait_active || return 1; rm -f "$hook_dir/rollback-pause.ready" "$hook_dir/rollback-pause.continue"; printf 'armed\n' >"$hook_dir/rollback-pause.arm"; old="$(ra_main_pid)" || return 1; systemctl restart "$RA_SERVICE" >>"$RA_TRANSCRIPT" 2>&1 & restart_pid=$!; ra_wait_file "$hook_dir/rollback-pause.ready" 60 || return 1; [[ "$(ra_exact_rolling_back_count)" == 1 ]] || { ra_die "expected one exact rolling_back authority"; return 1; }; ra_capture kill -KILL "$old" || return 1; wait "$restart_pid" || true; ra_wait_new_pid "$old" 120 >/dev/null || return 1; ra_wait_active 180 || return 1; ra_privacy_require_protected || return 1; ra_systemd_hook_cleanup rollback_hook
}
ra_runtime_terminal_failure() {
  local hook_dir="/run/podlaz/release-acceptance-terminal" override="/etc/systemd/system/podlazd.service.d/99-release-acceptance-terminal.conf" identity
  identity="$(jq -cn --arg path "$override" --arg hook "$hook_dir" '{path:$path,hook_dir:$hook}')"; ra_mut_begin_acquire terminal_hook systemd_dropin "$identity" || return 1; mkdir -p "$hook_dir" "$(dirname "$override")"; chmod 0700 "$hook_dir"; cat >"$override" <<EOF
[Service]
Environment=PODLAZ_E2E_TUN_TERMINAL_FAILURE=true
Environment=PODLAZ_E2E_TUN_TERMINAL_FAILURE_DIR=$hook_dir
Environment=PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE=true
Environment=PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE_DIR=$hook_dir
Environment=PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE_TIMEOUT_SECONDS=180
EOF
  ra_capture systemctl daemon-reload || return 1; ra_mut_mark_acquired terminal_hook || return 1; ra_capture systemctl restart "$RA_SERVICE" || return 1; ra_wait_active || return 1; printf 'trigger\n' >"$hook_dir/terminal-failure.trigger"; ra_wait_file "$hook_dir/terminal-data-plane-clean.ready" 90 || return 1; ra_privacy_require_protected || return 1; printf 'continue\n' >"$hook_dir/terminal-data-plane-clean.continue"; ra_wait_inactive 120 || return 1; ra_systemd_hook_cleanup terminal_hook || return 1; ra_privacy_require_ordinary
}

ra_wifi_reconnect_once() {
  ((RA_ALLOW_WIFI==1)) || { ra_record wifi_reconnect SKIP_USER_REQUEST; return; }; command -v nmcli >/dev/null 2>&1 || { ra_record wifi_reconnect SKIP_HOST_CAPABILITY "nmcli unavailable"; return; }; ra_capture nmcli -t -f NAME,TYPE,DEVICE connection show --active || { ra_record wifi_reconnect SKIP_HOST_CAPABILITY "NetworkManager query failed"; return; }; local line connection device identity; line="$(awk -F: '$2=="802-11-wireless" && $3!="" {print;exit}' <<<"$RA_CAPTURE")"; [[ -n "$line" ]] || { ra_record wifi_reconnect SKIP_HOST_CAPABILITY "no active Wi-Fi connection"; return; }; IFS=: read -r connection _ device <<<"$line"; identity="$(jq -cn --arg connection "$connection" --arg device "$device" '{connection:$connection,device:$device}')"; ra_mut_begin_acquire wifi_reconnect networkmanager_connection "$identity" || return 1; ra_capture nmcli connection down "$connection" || return 1; ra_mut_mark_acquired wifi_reconnect || return 1; ra_capture nmcli connection up "$connection" || return 1; ra_mut_begin_release wifi_reconnect || return 1; ra_mut_mark_released wifi_reconnect || return 1; ra_wait_active 120 || return 1; ra_privacy_require_protected || return 1; ra_record wifi_reconnect PASS
}
ra_suspend_once() { ((RA_ALLOW_SUSPEND==1)) || { ra_record suspend_resume SKIP_USER_REQUEST; return; }; command -v rtcwake >/dev/null 2>&1 || { ra_record suspend_resume SKIP_HOST_CAPABILITY "rtcwake unavailable"; return; }; if ! ra_capture rtcwake -m mem -s 15; then ra_record suspend_resume SKIP_HOST_CAPABILITY "bounded rtcwake suspend unavailable"; return; fi; ra_wait_active 180 || return 1; ra_privacy_require_protected || return 1; ra_record suspend_resume PASS; }
ra_soak_run() {
  local seconds interval start next elapsed wifi_done=0 suspend_done=0 pid
  if [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" == 1 ]]; then seconds="${RELEASE_ACCEPTANCE_TEST_SOAK_SECONDS:-2}"; interval=1; else seconds=$((RA_SOAK_MINUTES*60)); interval=60; fi; start=$SECONDS; next=$SECONDS
  while ((SECONDS-start<seconds)); do elapsed=$((SECONDS-start)); if ((SECONDS>=next)); then ra_wait_active 30 || return 1; ra_privacy_require_protected || return 1; ra_product doctor --tun --json >/dev/null || return 1; pid="$(ra_main_pid)" || return 1; ra_capture ps -o pid=,rss=,etimes= -p "$pid" || return 1; next=$((SECONDS+interval)); fi; if ((wifi_done==0 && elapsed>=seconds/3)); then ra_wifi_reconnect_once || return 1; wifi_done=1; fi; if ((suspend_done==0 && elapsed>=(seconds*2)/3)); then ra_suspend_once || return 1; suspend_done=1; fi; sleep 1; done
  ((wifi_done==1)) || ra_wifi_reconnect_once || return 1; ((suspend_done==1)) || ra_suspend_once || return 1; ra_record resource_soak PASS "" "$(jq -cn --argjson measured "$((SECONDS-start))" '{measured_seconds:$measured}')"
}

ra_package_setup_prepare() {
  local candidate="$1" previous="$2" installed="$3" cv pv identity
  cv="$(jq -r '.version' <<<"$candidate")"; if [[ -n "$installed" && "$installed" != "$cv" ]] && ra_pkg_lt "$installed" "$cv"; then return 0; fi; [[ -n "$previous" ]] || { ra_die "full lower-release qualification requires an installed lower release or --previous-deb"; return 1; }; pv="$(jq -r '.version' <<<"$previous")"; ra_pkg_lt "$pv" "$cv" || { ra_die "--previous-deb is not strictly lower than candidate"; return 1; }; identity="$(jq -cn --argjson p "$previous" --argjson c "$candidate" '{previous:$p,candidate:$c}')"; ra_mut_begin_acquire package_setup previous_package "$identity" || return 1; ra_pkg_install_exact "$previous" || return 1; ra_mut_mark_acquired package_setup
}
ra_package_setup_reconcile_abort() {
  local state identity candidate previous installed cv pv
  state="$(jq -r '.mutations.package_setup.state//"released"' "$RA_CHECKPOINT")"; [[ "$state" != released ]] || return 0; identity="$(jq -ce '.mutations.package_setup.identity' "$RA_CHECKPOINT")" || return 1; candidate="$(jq -c '.candidate' <<<"$identity")"; previous="$(jq -c '.previous' <<<"$identity")"; ra_pkg_assert_identity "$candidate" || return 1; ra_pkg_assert_identity "$previous" || return 1; installed="$(ra_pkg_installed_version)" || return 1; cv="$(jq -r '.version' <<<"$candidate")"; pv="$(jq -r '.version' <<<"$previous")"
  if [[ "$installed" == "$cv" ]]; then [[ "$state" != acquired ]] || ra_mut_begin_release package_setup || return 1; state="$(jq -r '.mutations.package_setup.state' "$RA_CHECKPOINT")"; if [[ "$state" == acquiring || "$state" == releasing ]]; then ra_mut_mark_released package_setup; fi; return; fi
  [[ "$installed" == "$pv" ]] || { ra_die "package_setup installed version is neither exact previous nor candidate"; return 1; }; if [[ "$state" == acquiring ]]; then ra_mut_mark_acquired package_setup || return 1; state=acquired; fi; if [[ "$state" == acquired ]]; then ra_mut_begin_release package_setup || return 1; state=releasing; fi; [[ "$state" == releasing ]] || return 1; ra_pkg_install_exact "$candidate" || return 1; ra_mut_mark_released package_setup
}
ra_package_setup_release_after_candidate() { local state; state="$(jq -r '.mutations.package_setup.state//"released"' "$RA_CHECKPOINT")"; case "$state" in released) ;; acquired) ra_mut_begin_release package_setup || return 1; ra_mut_mark_released package_setup ;; acquiring|releasing) ra_mut_mark_released package_setup ;; *) ra_die "unsupported package_setup state: $state"; return 1 ;; esac; }

ra_report_write() {
  local qualification="$1" summary="$RA_PUBLIC_DIR/summary.txt" report="$RA_PUBLIC_DIR/report.json"; mkdir -p "$RA_PUBLIC_DIR"; chmod 0700 "$RA_PUBLIC_DIR"; { printf 'Podlaz release laptop acceptance\nResult: %s\n' "$qualification"; jq -r '.scenarios|to_entries|sort_by(.key)[]|"\(.key): \(.value.outcome)\(if .value.reason!="" then " - "+.value.reason else "" end)"' "$RA_CHECKPOINT"; } >"$summary"; jq --arg q "$qualification" '{schema_version:"podlaz.release-acceptance-report.v1",qualification:$q,scenarios:.scenarios}' "$RA_CHECKPOINT" >"$report"; chmod 0600 "$summary" "$report"; if ((EUID==0)) && [[ "${RELEASE_ACCEPTANCE_TEST_MODE:-0}" != 1 ]]; then chown "$RA_UID:$RA_GID" "$summary" "$report"; fi
}
ra_qualification() {
  jq -e '.scenarios[]?|select(.outcome=="FAIL")' "$RA_CHECKPOINT" >/dev/null && { printf FAIL; return; }; local required=(lower_release_upgrade privacy_active graceful_restart daemon_kill reinstall rollback_interruption preconnect_coexistence active_coexistence disconnect_cleanup runtime_terminal_convergence final_restoration) r; if ((RA_REBOOT_PHASES==1)); then required+=(reboot_autostart_off reboot_autostart_on explicit_disconnect_no_same_boot_retry reboot_terminal_autostart terminal_no_same_boot_retry); fi; for r in "${required[@]}"; do [[ "$(jq -r --arg r "$r" '.scenarios[$r].outcome//""' "$RA_CHECKPOINT")" == PASS ]] || { printf FAIL; return; }; done; if ((RA_SOAK_MINUTES<60 || RA_REBOOT_PHASES==0 || RA_ALLOW_WIFI==0 || RA_ALLOW_SUSPEND==0)); then printf PARTIAL_PASS; return; fi; [[ "$(jq -r '.scenarios.wifi_reconnect.outcome//""' "$RA_CHECKPOINT")" == PASS && "$(jq -r '.scenarios.suspend_resume.outcome//""' "$RA_CHECKPOINT")" == PASS ]] || { printf PARTIAL_PASS; return; }; printf QUALIFIED_PASS
}
ra_restore_original_policy() { ra_terminal_profile_reconcile || return 1; ra_product autostart disable >/dev/null || return 1; local manifest; manifest="$(jq -ce '.private.boot_manifest' "$RA_CHECKPOINT")" || return 1; ra_boot_manifest_restore "$manifest"; }
ra_prepare_reboot_off() { local boot; ra_product autostart disable >/dev/null || return 1; boot="$(ra_boot_id)" || return 1; ra_state_jq '.previous_boot_id=$boot|.phase="await-reboot-autostart-off"' --arg boot "$boot" || return 1; printf 'Release acceptance checkpoint saved.\nReboot the laptop, then run: sudo ./release-laptop.sh --resume\n'; }
ra_finalize() { ra_record final_restoration PASS || return 1; local q; q="$(ra_qualification)"; ra_report_write "$q" || return 1; ra_set_phase complete || return 1; [[ "$q" == FAIL ]] || ra_state_remove || return 1; printf '%s\n' "$q"; [[ "$q" != FAIL ]]; }

ra_run_pre_reboot() {
  local candidate profile cv installed before
  candidate="$(jq -ce '.candidate' "$RA_CHECKPOINT")" || return 1; profile="$(jq -r '.private.selected_profile' "$RA_CHECKPOINT")"; cv="$(jq -r '.version' <<<"$candidate")"
  ra_fixture_acquire fixture_a || return 1; ra_record preconnect_coexistence PASS || return 1; ra_connect "$profile" || return 1; ra_wait_active || return 1; installed="$(ra_pkg_installed_version)" || return 1
  if [[ "$installed" != "$cv" ]]; then before="$(ra_main_pid)" || return 1; ra_pkg_install_exact "$candidate" || return 1; ra_wait_new_pid "$before" >/dev/null || return 1; ra_wait_active || return 1; ra_privacy_require_protected || return 1; fi
  ra_package_setup_release_after_candidate || return 1; ra_record lower_release_upgrade PASS || return 1; ra_privacy_require_protected || return 1; ra_record privacy_active PASS || return 1; ra_fixture_verify fixture_a || return 1; ra_lifecycle_graceful_restart || return 1; ra_record graceful_restart PASS || return 1; ra_lifecycle_unexpected_death || return 1; ra_record daemon_kill PASS || return 1; ra_lifecycle_reinstall "$candidate" || return 1; ra_record reinstall PASS || return 1; ra_lifecycle_rollback_interruption || return 1; ra_record rollback_interruption PASS || return 1; ra_fixture_acquire fixture_b || return 1; ra_fixture_churn fixture_b || return 1; ra_fixture_verify fixture_b || return 1; ra_fixture_release fixture_b || return 1; ra_record active_coexistence PASS || return 1; ra_lifecycle_stop_start || return 1; ra_record stop_start_no_reconnect PASS || return 1; ra_connect "$profile" || return 1; ra_wait_active || return 1; ra_privacy_require_protected || return 1; ra_soak_run || return 1; ra_disconnect || return 1; ra_wait_inactive || return 1; ra_privacy_require_ordinary || return 1; ra_fixture_verify fixture_a || return 1; ra_fixture_release fixture_a || return 1; ra_record disconnect_cleanup PASS || return 1; ra_connect "$profile" || return 1; ra_wait_active || return 1; ra_runtime_terminal_failure || return 1; ra_record runtime_terminal_convergence PASS || return 1
  if ((RA_REBOOT_PHASES==0)); then ra_record reboot_autostart_off SKIP_USER_REQUEST; ra_record reboot_autostart_on SKIP_USER_REQUEST; ra_record explicit_disconnect_no_same_boot_retry SKIP_USER_REQUEST; ra_record reboot_terminal_autostart SKIP_USER_REQUEST; ra_record terminal_no_same_boot_retry SKIP_USER_REQUEST; ra_restore_original_policy || return 1; ra_finalize; else ra_prepare_reboot_off; fi
}
ra_run_new() {
  if ra_checkpoint_exists; then ra_preflight_die "an acceptance checkpoint already exists; use --resume or --abort"; return 2; fi
  local candidate previous="" installed manifest run_id baseline profile
  candidate="$(ra_pkg_inspect "$RA_CANDIDATE")" || return $?; [[ -z "$RA_PREVIOUS_DEB" ]] || previous="$(ra_pkg_inspect "$RA_PREVIOUS_DEB")" || return $?; installed="$(ra_pkg_installed_version)" || return 1; manifest="$(ra_boot_manifest_capture)" || return 1; run_id="$(date -u +%Y%m%dT%H%M%SZ)-$$"; ra_artifacts_init_new "$run_id"; baseline="$(ra_privacy_baseline)" || return 1; profile="$(ra_profile_select "$RA_PROFILE")" || return 1; ra_state_init "$run_id" "$candidate" "$installed" "$manifest" "$profile" || return 1; ra_state_jq '.private.privacy_baseline=$baseline|.private.selected_profile=$profile|.private.run_config.candidate=$candidate|.private.run_config.previous=$previous' --argjson baseline "$baseline" --arg profile "$profile" --argjson candidate "$candidate" --argjson previous "${previous:-null}" || return 1; ra_package_setup_prepare "$candidate" "$previous" "$installed" || return 1; ra_set_phase running-pre-reboot || return 1; ra_run_pre_reboot
}
ra_resume_require_new_boot() { local old current; old="$(jq -r '.previous_boot_id' "$RA_CHECKPOINT")"; current="$(ra_boot_id)" || return 1; [[ -n "$old" && "$current" != "$old" ]] || { ra_die "--resume requires a real reboot with a new boot_id"; return 1; }; }
ra_run_resume() {
  ra_checkpoint_exists || { ra_preflight_die "no acceptance checkpoint exists"; return 2; }; ra_state_require_schema || return 1; ra_artifacts_from_state || return 1; local phase profile current terminal_id attempt candidate previous installed; phase="$(jq -r '.phase' "$RA_CHECKPOINT")"; profile="$(jq -r '.private.selected_profile//""' "$RA_CHECKPOINT")"; RA_SOAK_MINUTES="$(jq -r '.private.run_config.soak_minutes//60' "$RA_CHECKPOINT")"; RA_ALLOW_WIFI="$(jq -r 'if .private.run_config.allow_wifi_reconnect then 1 else 0 end' "$RA_CHECKPOINT")"; RA_ALLOW_SUSPEND="$(jq -r 'if .private.run_config.allow_suspend then 1 else 0 end' "$RA_CHECKPOINT")"; RA_REBOOT_PHASES="$(jq -r 'if .private.run_config.reboot_phases then 1 else 0 end' "$RA_CHECKPOINT")"
  case "$phase" in
    preparing-lower-release) candidate="$(jq -ce '.candidate' "$RA_CHECKPOINT")" || return 1; previous="$(jq -ce '.private.run_config.previous//empty' "$RA_CHECKPOINT" 2>/dev/null || true)"; ra_package_setup_reconcile_abort || return 1; installed="$(ra_pkg_installed_version)" || return 1; ra_package_setup_prepare "$candidate" "$previous" "$installed" || return 1; ra_set_phase running-pre-reboot || return 1; ra_run_pre_reboot ;;
    running-pre-reboot) ra_die "pre-reboot scenario execution was interrupted at a non-replayable boundary; use --abort, then start a new run"; return 1 ;;
    await-reboot-autostart-off) ra_resume_require_new_boot || return 1; ra_wait_inactive 120 || return 1; ra_record reboot_autostart_off PASS || return 1; ra_product autostart enable --mode tun "$profile" >/dev/null || return 1; current="$(ra_boot_id)" || return 1; ra_state_jq '.previous_boot_id=$boot|.phase="await-reboot-autostart-on"' --arg boot "$current" || return 1; printf 'Release acceptance checkpoint advanced.\nReboot the laptop again, then run: sudo ./release-laptop.sh --resume\n' ;;
    await-reboot-autostart-on) ra_resume_require_new_boot || return 1; ra_wait_active 180 || return 1; ra_privacy_require_protected || return 1; ra_record reboot_autostart_on PASS || return 1; ra_disconnect || return 1; ra_wait_inactive || return 1; sleep 5; ra_wait_inactive 5 || return 1; ra_record explicit_disconnect_no_same_boot_retry PASS || return 1; terminal_id="$(ra_terminal_profile_create)" || return 1; ra_product autostart enable --mode tun "$terminal_id" >/dev/null || return 1; current="$(ra_boot_id)" || return 1; ra_state_jq '.previous_boot_id=$boot|.phase="await-reboot-terminal"' --arg boot "$current" || return 1; printf 'Release acceptance checkpoint advanced.\nReboot the laptop again, then run: sudo ./release-laptop.sh --resume\n' ;;
    await-reboot-terminal) ra_resume_require_new_boot || return 1; ra_wait_inactive 180 || return 1; [[ -f "$RA_BOOT_ATTEMPT" && ! -L "$RA_BOOT_ATTEMPT" ]] || { ra_die "boot autostart attempt evidence missing"; return 1; }; attempt="$(jq -ce . "$RA_BOOT_ATTEMPT")" || return 1; [[ "$(jq -r '.state//""' <<<"$attempt")" == terminal ]] || { ra_die "terminal autostart attempt did not converge to terminal"; return 1; }; ra_record reboot_terminal_autostart PASS "" "$(jq -c '{state,terminal_reason:(.terminal_reason//"")}' <<<"$attempt")" || return 1; sleep 5; ra_wait_inactive 5 || return 1; ra_record terminal_no_same_boot_retry PASS || return 1; ra_restore_original_policy || return 1; ra_finalize ;;
    *) ra_die "unsupported persisted phase: $phase"; return 1 ;;
  esac
}
ra_run_abort() {
  ra_checkpoint_exists || { ra_preflight_die "no acceptance checkpoint exists"; return 2; }; ra_state_require_schema || return 1; ra_artifacts_from_state || return 1; local candidate; candidate="$(jq -ce '.candidate' "$RA_CHECKPOINT")" || return 1; ra_pkg_assert_identity "$candidate" || return 1; if jq -e '.mutations.package_setup and .mutations.package_setup.state!="released"' "$RA_CHECKPOINT" >/dev/null; then ra_package_setup_reconcile_abort || return 1; elif [[ "$(ra_pkg_installed_version)" != "$(jq -r '.version' <<<"$candidate")" ]]; then ra_pkg_install_exact "$candidate" || return 1; fi; ra_disconnect >/dev/null 2>&1 || true; ra_cleanup_owned_mutations || return 1; ra_require_mutations_released || return 1; ra_restore_original_policy || return 1; ra_record final_restoration PASS || return 1; ra_set_phase aborted-clean || return 1; ra_report_write FAIL || return 1; ra_state_remove || return 1; printf 'ABORTED_CLEAN\n'
}
ra_main() { ra_cli_parse "$@" || { ra_usage >&2; return 2; }; ra_require_root_and_user || return $?; ra_init_paths; ra_require_tools || return $?; case "$RA_MODE" in new) ra_run_new ;; resume) ra_run_resume ;; abort) ra_run_abort ;; *) ra_die "internal mode error"; return 1 ;; esac; }

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then ra_main "$@"; fi
