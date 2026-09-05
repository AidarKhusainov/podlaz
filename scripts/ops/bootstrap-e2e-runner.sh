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

validate_inputs() {
  [[ "${GITHUB_REPOSITORY}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || \
    fail "GITHUB_REPOSITORY must use owner/name"
  [[ "${RUNNER_VERSION}" =~ ^[0-9]+[.][0-9]+[.][0-9]+$ ]] || \
    fail "RUNNER_VERSION must use MAJOR.MINOR.PATCH"
  [[ "${RUNNER_SHA256}" =~ ^[0-9A-Fa-f]{64}$ ]] || \
    fail "RUNNER_SHA256 must be a 64-character SHA-256 digest"
  [[ "${RUNNER_USER}" =~ ^[a-z_][a-z0-9_-]*[$]?$ ]] || \
    fail "RUNNER_USER has unsupported characters"
  [[ "${RUNNER_HOME}" == /opt/actions-runner/* ]] || \
    fail "RUNNER_HOME must stay below /opt/actions-runner"
}

validate_host() {
  # shellcheck disable=SC1091
  . /etc/os-release
  [[ "${ID:-}" == "ubuntu" && "${VERSION_ID:-}" == "24.04" ]] || \
    fail "release TUN runner requires Ubuntu 24.04"
  [[ "$(uname -m)" == "x86_64" ]] || fail "release TUN runner requires x86_64"
  [[ -d /run/systemd/system ]] || fail "systemd must be PID 1"
  [[ -c /dev/net/tun ]] || fail "/dev/net/tun is required"
}

install_host_packages() {
  export DEBIAN_FRONTEND=noninteractive
  apt-get update
  apt-get install -y --no-install-recommends \
    apt \
    bash \
    ca-certificates \
    coreutils \
    curl \
    dpkg \
    git \
    grep \
    gzip \
    init-system-helpers \
    iproute2 \
    libc-bin \
    mawk \
    nftables \
    passwd \
    procps \
    python3 \
    sed \
    sudo \
    systemd \
    systemd-resolved \
    tar
  systemctl enable --now systemd-resolved.service
}

assert_command_path() {
  local command_name="$1" expected="$2" actual
  actual="$(command -v "${command_name}" || true)"
  [[ "${actual}" == "${expected}" ]] || \
    fail "${command_name} resolved to ${actual:-missing}, want ${expected}"
}

validate_privileged_command_paths() {
  assert_command_path apt /usr/bin/apt
  assert_command_path systemctl /usr/bin/systemctl
  assert_command_path journalctl /usr/bin/journalctl
  assert_command_path ip /usr/sbin/ip
  assert_command_path nft /usr/sbin/nft
  assert_command_path resolvectl /usr/bin/resolvectl
  assert_command_path python3 /usr/bin/python3
  assert_command_path rm /usr/bin/rm
  assert_command_path sha256sum /usr/bin/sha256sum
  assert_command_path readlink /usr/bin/readlink
  assert_command_path pgrep /usr/bin/pgrep
  assert_command_path cat /usr/bin/cat
  assert_command_path kill /usr/bin/kill
  assert_command_path deb-systemd-helper /usr/bin/deb-systemd-helper
  assert_command_path env /usr/bin/env
  assert_command_path curl /usr/bin/curl
}

ensure_runner_identity() {
  if ! id -u "${RUNNER_USER}" >/dev/null 2>&1; then
    useradd --create-home --shell /bin/bash "${RUNNER_USER}"
  fi
  if ! getent group podlaz >/dev/null 2>&1; then
    groupadd --system podlaz
  fi
  if id -nG "${RUNNER_USER}" | tr ' ' '\n' | grep -Fx podlaz >/dev/null; then
    gpasswd -d "${RUNNER_USER}" podlaz >/dev/null
  fi
}

install_sudoers_policy() {
  local sudoers_file="/etc/sudoers.d/podlaz-release-tun-runner"
  cat >"${sudoers_file}" <<EOF
Defaults:${RUNNER_USER} !requiretty
Cmnd_Alias PODLAZ_RELEASE_TUN_ROOT = /usr/bin/apt, /usr/bin/systemctl, /usr/bin/journalctl, /usr/sbin/ip, /usr/sbin/nft, /usr/bin/resolvectl, /usr/bin/python3, /usr/bin/rm, /usr/bin/sha256sum, /usr/bin/readlink, /usr/bin/pgrep, /usr/bin/cat, /usr/bin/kill, /usr/bin/deb-systemd-helper
${RUNNER_USER} ALL=(root) NOPASSWD: PODLAZ_RELEASE_TUN_ROOT
${RUNNER_USER} ALL=(${RUNNER_USER}:podlaz) NOPASSWD: /usr/bin/env, /usr/bin/curl
EOF
  chmod 0440 "${sudoers_file}"
  visudo -cf "${sudoers_file}" >/dev/null
}

configure_needrestart() {
  if [[ -d /etc/needrestart/conf.d ]]; then
    cat > /etc/needrestart/conf.d/actions_runner_services.conf <<'EOF'
$nrconf{override_rc}{qr(^actions\.runner\..+\.service$)} = 0;
EOF
  fi
}

stop_existing_runner() {
  if [[ -x "${RUNNER_HOME}/svc.sh" ]]; then
    (
      cd "${RUNNER_HOME}"
      ./svc.sh stop >/dev/null 2>&1 || true
      ./svc.sh uninstall >/dev/null 2>&1 || true
    )
  fi
  rm -rf -- "${RUNNER_HOME}"
  install -d -o "${RUNNER_USER}" -g "${RUNNER_USER}" -m 0755 "${RUNNER_HOME}"
}

install_runner() {
  local archive archive_name url runner_user_home
  archive_name="actions-runner-linux-x64-${RUNNER_VERSION}.tar.gz"
  archive="$(mktemp "/tmp/${archive_name}.XXXXXX")"
  url="https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/${archive_name}"
  runner_user_home="$(getent passwd "${RUNNER_USER}" | cut -d: -f6)"

  log "download verified GitHub Actions runner ${RUNNER_VERSION}"
  curl --fail --location --silent --show-error --proto '=https' --tlsv1.2 \
    "${url}" -o "${archive}"
  printf '%s  %s\n' "${RUNNER_SHA256}" "${archive}" | sha256sum -c - >/dev/null
  tar -xzf "${archive}" -C "${RUNNER_HOME}"
  rm -f -- "${archive}"
  "${RUNNER_HOME}/bin/installdependencies.sh"
  chown -R "${RUNNER_USER}:${RUNNER_USER}" "${RUNNER_HOME}"

  log "register dedicated release TUN runner"
  (
    cd "${RUNNER_HOME}"
    sudo -u "${RUNNER_USER}" env HOME="${runner_user_home}" \
      ./config.sh \
        --url "${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}" \
        --token "${RUNNER_REGISTRATION_TOKEN}" \
        --name "${RUNNER_NAME}" \
        --labels "${RUNNER_LABELS}" \
        --work _work \
        --unattended \
        --replace
    ./svc.sh install "${RUNNER_USER}"
    ./svc.sh start
    ./svc.sh status
  )
}

main() {
  require_root
  : "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"
  : "${RUNNER_REGISTRATION_TOKEN:?RUNNER_REGISTRATION_TOKEN is required}"
  : "${RUNNER_VERSION:?RUNNER_VERSION is required}"
  : "${RUNNER_SHA256:?RUNNER_SHA256 is required}"
  : "${GITHUB_SERVER_URL:=https://github.com}"
  RUNNER_USER="gha-runner"
  RUNNER_HOME="/opt/actions-runner/podlaz-vpn-e2e"
  RUNNER_NAME="podlaz-vpn-e2e"
  RUNNER_LABELS="self-hosted,linux,x64,vpn-e2e,ubuntu-24.04"

  validate_inputs
  validate_host
  log "install release TUN runner dependencies"
  install_host_packages
  validate_privileged_command_paths
  ensure_runner_identity
  install_sudoers_policy
  configure_needrestart
  stop_existing_runner
  install_runner
  log "release TUN runner provisioning completed"
}

main "$@"
