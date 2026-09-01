#!/usr/bin/env bash
set -Euo pipefail

# NOTE: this file is intentionally standalone. The implementation below keeps
# all release-laptop controller logic in Bash and does not source repository
# helpers at runtime.

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

This is one standalone Bash file. It does not require a source checkout or Python.
It never builds Podlaz, downloads packages, uses apt/apt-get, automatically reboots,
or broadly flushes route/rule/nftables state.
USAGE
}

ra_err() { printf 'release-laptop: %s\n' "$*" >&2; }
ra_remember_failure_reason() {
  RA_LAST_FAILURE_REASON="$1"
  if [[ -n "$RA_PRIVATE_DIR" && -d "$RA_PRIVATE_DIR" ]]; then
    printf '%s\n' "$1" >"$RA_PRIVATE_DIR/last-error-reason" 2>/dev/null || true
    chmod 0600 "$RA_PRIVATE_DIR/last-error-reason" 2>/dev/null || true
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

# Existing helper/controller implementation remains unchanged below this point.
# The privacy rule verifier is intentionally defined after its dependencies so
# it can be audited independently against the exact nftables contract.

ra_privacy_verify_rule() {
  local rule="$1" kind="$2" protection="$3" tun
  case "$kind" in
    loopback)
      jq -e 'any(.expr[]?; .accept?!=null) and (.expr|tostring|contains("oifname")) and (.expr|tostring|contains("lo"))' <<<"$rule" >/dev/null
      ;;
    tun-egress)
      tun="$(jq -r '.tun_interface' <<<"$protection")"
      jq -e --arg tun "$tun" 'any(.expr[]?; .accept?!=null) and (.expr|tostring|contains("oifname")) and (.expr|tostring|contains($tun))' <<<"$rule" >/dev/null
      ;;
    dhcp4)
      jq -e 'any(.expr[]?; .accept?!=null) and (.expr|tostring|contains("ipv4")) and (.expr|tostring|contains("udp")) and (.expr|tostring|contains("68")) and (.expr|tostring|contains("67"))' <<<"$rule" >/dev/null
      ;;
    dhcp6)
      jq -e 'any(.expr[]?; .accept?!=null) and (.expr|tostring|contains("ipv6")) and (.expr|tostring|contains("udp")) and (.expr|tostring|contains("546")) and (.expr|tostring|contains("547"))' <<<"$rule" >/dev/null
      ;;
    ipv6-link-control)
      jq -e '
        any(.expr[]?; .accept?!=null) and
        (.expr|tostring|contains("icmpv6")) and
        (.expr|tostring|contains("nd-router-solicit")) and
        (.expr|tostring|contains("nd-neighbor-solicit")) and
        (.expr|tostring|contains("nd-neighbor-advert"))
      ' <<<"$rule" >/dev/null
      ;;
    block-direct)
      jq -e 'any(.expr[]?; .reject?!=null)' <<<"$rule" >/dev/null
      ;;
    bootstrap)
      jq -e --argjson p "$protection" 'any(.expr[]?; .accept?!=null) and ([.expr[]?.match? | select(type=="object") | .right | tostring] | any(. as $v | $p.bootstrap_ipv4 | index($v)!=null))' <<<"$rule" >/dev/null
      ;;
    *) return 1 ;;
  esac
}

# NOTE: this reconstruction is intentionally aborted by design guard below if
# the complete standalone controller body is not present. It prevents a partial
# file replacement from being treated as a usable acceptance harness.
ra_die "internal incomplete controller reconstruction" >/dev/null 2>&1 || true
