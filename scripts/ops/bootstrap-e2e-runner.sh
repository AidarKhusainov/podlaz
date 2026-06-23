#!/usr/bin/env bash
set -Eeuo pipefail

log() {
  printf '\n>>> %s\n' "$*"
}

fail() {
  printf 'ERROR: %s\n' "$*" >&2
  exit 1
}

require_root() {
  [[ "$(id -u)" == "0" ]] || fail "bootstrap must run as root"
}

require_no_newline() {
  local name="$1"
  local value="$2"
  case "${value}" in
    *$'\n'*|*$'\r'*) fail "${name} must not contain newlines" ;;
  esac
}

validate_runner_user() {
  [[ "${RUNNER_USER}" =~ ^[a-z_][a-z0-9_-]*[$]?$ ]] || fail "RUNNER_USER has unsupported characters: ${RUNNER_USER}"
}

validate_runner_home() {
  [[ "${RUNNER_HOME}" == /* ]] || fail "RUNNER_HOME must be an absolute path"
  case "${RUNNER_HOME}" in
    *[!A-Za-z0-9._/-]*) fail "RUNNER_HOME contains unsupported characters: ${RUNNER_HOME}" ;;
  esac
}

validate_host_wrapper_dir() {
  [[ "${HOST_WRAPPER_DIR}" == /* ]] || fail "HOST_WRAPPER_DIR must be an absolute path"
  case "${HOST_WRAPPER_DIR}" in
    *[!A-Za-z0-9._/-]*) fail "HOST_WRAPPER_DIR contains unsupported characters: ${HOST_WRAPPER_DIR}" ;;
  esac
}

install_packages() {
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    coreutils \
    curl \
    dnsutils \
    dpkg-dev \
    file \
    gcc \
    git \
    gzip \
    iproute2 \
    jq \
    make \
    nftables \
    procps \
    psmisc \
    python3 \
    sudo \
    systemd \
    systemd-resolved \
    tar
}

ensure_supported_platform() {
  # shellcheck disable=SC1091
  . /etc/os-release

  case "${RUNNER_PLATFORM_LABEL}" in
    ubuntu-24.04)
      [[ "${ID:-}" == "ubuntu" && "${VERSION_ID:-}" == "24.04" ]] || fail "expected ubuntu-24.04, got ${PRETTY_NAME:-unknown OS}"
      ;;
    ubuntu-22.04)
      [[ "${ID:-}" == "ubuntu" && "${VERSION_ID:-}" == "22.04" ]] || fail "expected ubuntu-22.04, got ${PRETTY_NAME:-unknown OS}"
      ;;
    debian-12)
      [[ "${ID:-}" == "debian" && "${VERSION_ID:-}" == "12" ]] || fail "expected debian-12, got ${PRETTY_NAME:-unknown OS}"
      ;;
    debian-13)
      [[ "${ID:-}" == "debian" && "${VERSION_ID:-}" == "13" ]] || fail "expected debian-13, got ${PRETTY_NAME:-unknown OS}"
      ;;
    *)
      fail "unsupported RUNNER_PLATFORM_LABEL=${RUNNER_PLATFORM_LABEL}"
      ;;
  esac
}

actual_runner_arch() {
  case "$(uname -m)" in
    x86_64) printf 'x64' ;;
    aarch64|arm64) printf 'arm64' ;;
    *) fail "unsupported runner architecture: $(uname -m)" ;;
  esac
}

ensure_supported_arch() {
  local actual
  actual="$(actual_runner_arch)"
  [[ "${RUNNER_ARCH_LABEL}" == "${actual}" ]] || fail "expected ${RUNNER_ARCH_LABEL} host, got ${actual} ($(uname -m))"
}

ensure_system_capabilities() {
  command -v systemctl >/dev/null 2>&1 || fail "systemctl is required"
  command -v journalctl >/dev/null 2>&1 || fail "journalctl is required"
  [[ -d /run/systemd/system ]] || fail "systemd must be PID 1 on the E2E host"
  [[ -c /dev/net/tun ]] || fail "/dev/net/tun is required for VPN/TUN E2E suites"
  systemctl enable --now systemd-resolved.service >/dev/null 2>&1 || true
}

ensure_runner_identity() {
  if ! id -u "${RUNNER_USER}" >/dev/null 2>&1; then
    useradd --create-home --shell /bin/bash "${RUNNER_USER}"
  fi

  if ! getent group podlaz >/dev/null 2>&1; then
    groupadd --system podlaz
  fi

  usermod -aG podlaz "${RUNNER_USER}"
}

install_sudoers_policy() {
  local sudoers_file="/etc/sudoers.d/podlaz-e2e-runner"
  cat >"${sudoers_file}" <<EOF
Defaults:${RUNNER_USER} !requiretty
Cmnd_Alias PODLAZ_E2E_ROOT = /usr/bin/true, /usr/bin/apt, /usr/bin/systemctl, /usr/bin/journalctl, /usr/sbin/ip, /usr/sbin/nft, /usr/bin/kill, /usr/bin/env, /usr/bin/timeout
Cmnd_Alias PODLAZ_E2E_WRAPPERS = ${HOST_WRAPPER_DIR}/suspend-resume, ${HOST_WRAPPER_DIR}/network-reconnect, ${HOST_WRAPPER_DIR}/dhcp-renew, ${HOST_WRAPPER_DIR}/dns-change, ${HOST_WRAPPER_DIR}/polkit-gui-auth, ${HOST_WRAPPER_DIR}/polkit-tty-auth
${RUNNER_USER} ALL=(root) NOPASSWD: PODLAZ_E2E_ROOT, PODLAZ_E2E_WRAPPERS
${RUNNER_USER} ALL=(${RUNNER_USER}:podlaz) NOPASSWD: /usr/bin/env
EOF
  chmod 0440 "${sudoers_file}"
  visudo -cf "${sudoers_file}" >/dev/null
}

runner_default_name() {
  local short_host
  short_host="$(hostname -s 2>/dev/null || hostname)"
  printf 'podlaz-%s-vpn-e2e' "${short_host}"
}

stop_existing_runner_service() {
  if [[ -x "${RUNNER_HOME}/svc.sh" ]]; then
    log "stop existing actions runner service in ${RUNNER_HOME}"
    (
      cd "${RUNNER_HOME}"
      ./svc.sh stop || true
      ./svc.sh uninstall || true
    )
  fi
}

detect_nested_runner_installations() {
  [[ -d "${RUNNER_HOME}" ]] || return 0
  if [[ -f "${RUNNER_HOME}/.runner" || -x "${RUNNER_HOME}/svc.sh" ]]; then
    return 0
  fi

  mapfile -t nested_runner_dirs < <(
    find "${RUNNER_HOME}" -mindepth 2 -maxdepth 2 \( -name .runner -o -name svc.sh \) -printf '%h\n' 2>/dev/null | sort -u
  )

  if [[ "${#nested_runner_dirs[@]}" -gt 0 ]]; then
    printf 'ERROR: found existing runner installation below RUNNER_HOME=%s:\n' "${RUNNER_HOME}" >&2
    printf '  %s\n' "${nested_runner_dirs[@]}" >&2
    fail "set runner_home to the exact existing runner directory, or use a clean server"
  fi
}

prepare_runner_directory() {
  detect_nested_runner_installations
  install -d -o "${RUNNER_USER}" -g "${RUNNER_USER}" -m 0755 "${RUNNER_HOME}"
  if [[ "${RESET_RUNNER}" == "true" ]]; then
    stop_existing_runner_service
    find "${RUNNER_HOME}" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
  elif [[ -f "${RUNNER_HOME}/.runner" ]]; then
    fail "runner is already configured in ${RUNNER_HOME}; rerun with reset_runner=true to replace it"
  fi
}

install_actions_runner() {
  local arch tarball url runner_home
  arch="$(actual_runner_arch)"
  tarball="actions-runner-linux-${arch}-${RUNNER_VERSION}.tar.gz"
  url="https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/${tarball}"
  runner_home="$(getent passwd "${RUNNER_USER}" | cut -d: -f6)"

  log "download actions runner ${RUNNER_VERSION} (${arch})"
  curl -fsSL "${url}" -o "/tmp/${tarball}"
  tar -xzf "/tmp/${tarball}" -C "${RUNNER_HOME}"
  rm -f "/tmp/${tarball}"
  chown -R "${RUNNER_USER}:${RUNNER_USER}" "${RUNNER_HOME}"

  log "configure actions runner"
  (
    cd "${RUNNER_HOME}"
    sudo -u "${RUNNER_USER}" env \
      HOME="${runner_home}" \
      ./config.sh \
        --url "${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}" \
        --token "${RUNNER_REGISTRATION_TOKEN}" \
        --name "${RUNNER_NAME}" \
        --labels "${RUNNER_LABELS}" \
        --work "_work" \
        --unattended \
        --replace

    ./svc.sh install "${RUNNER_USER}"
    ./svc.sh start
    ./svc.sh status
  )
}

main() {
  require_root

  : "${GITHUB_SERVER_URL:=https://github.com}"
  : "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
  : "${RUNNER_REGISTRATION_TOKEN:?RUNNER_REGISTRATION_TOKEN is required}"
  : "${RUNNER_VERSION:?RUNNER_VERSION is required}"
  : "${RUNNER_USER:=gha-runner}"
  : "${RUNNER_PLATFORM_LABEL:=ubuntu-24.04}"
  : "${RUNNER_ARCH_LABEL:=x64}"
  : "${RUNNER_LABELS:=self-hosted,linux,${RUNNER_ARCH_LABEL},vpn-e2e,${RUNNER_PLATFORM_LABEL}}"
  : "${RESET_RUNNER:=true}"
  : "${RUNNER_HOME:=/opt/actions-runner}"
  : "${RUNNER_NAME:=}"
  : "${HOST_WRAPPER_DIR:=/usr/local/libexec/podlaz-e2e}"

  if [[ -z "${RUNNER_NAME}" ]]; then
    RUNNER_NAME="$(runner_default_name)"
  fi

  require_no_newline GITHUB_SERVER_URL "${GITHUB_SERVER_URL}"
  require_no_newline GITHUB_REPOSITORY "${GITHUB_REPOSITORY}"
  require_no_newline RUNNER_VERSION "${RUNNER_VERSION}"
  require_no_newline RUNNER_USER "${RUNNER_USER}"
  require_no_newline RUNNER_PLATFORM_LABEL "${RUNNER_PLATFORM_LABEL}"
  require_no_newline RUNNER_ARCH_LABEL "${RUNNER_ARCH_LABEL}"
  require_no_newline RUNNER_LABELS "${RUNNER_LABELS}"
  require_no_newline RUNNER_HOME "${RUNNER_HOME}"
  require_no_newline RUNNER_NAME "${RUNNER_NAME}"
  require_no_newline HOST_WRAPPER_DIR "${HOST_WRAPPER_DIR}"
  validate_runner_user
  validate_runner_home
  validate_host_wrapper_dir

  case "${RESET_RUNNER}" in
    true|false) ;;
    *) fail "RESET_RUNNER must be true or false" ;;
  esac

  log "validate host contract"
  ensure_supported_platform
  ensure_supported_arch
  ensure_system_capabilities

  log "install host dependencies"
  install_packages

  log "configure runner identity and sudo policy"
  ensure_runner_identity
  install_sudoers_policy

  log "install GitHub Actions runner"
  prepare_runner_directory
  install_actions_runner

  log "bootstrap completed for ${RUNNER_NAME} with labels ${RUNNER_LABELS}"
}

main "$@"
