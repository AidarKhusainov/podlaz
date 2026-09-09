#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib/package_provenance.sh
source "${SCRIPT_DIR}/../lib/package_provenance.sh"

TEST_ROOT="$(mktemp -d)"
trap 'rm -rf -- "${TEST_ROOT}"' EXIT
INSTALLED_ROOT="${TEST_ROOT}/installed"
mkdir -p "${INSTALLED_ROOT}/usr/bin" "${INSTALLED_ROOT}/usr/lib/podlaz"

EXPECTED_VERSION="0.2.41"
INSTALLED_VERSION="${EXPECTED_VERSION}"
INSTALLED_STATUS="installed"
EXPECTED_COMMIT="0123456789abcdef0123456789abcdef01234567"
CLI_COMMIT="${EXPECTED_COMMIT}"

write_cli() {
  local target="$1" commit="$2"
  cat >"${target}" <<EOF
#!/bin/sh
if [ "\${1:-}" = version ]; then
  printf 'podlaz version 0.2.41\ncommit: %s\nbuilt: Sep 09 2026\n' '${commit}'
  exit 0
fi
exit 2
EOF
  chmod 0755 "${target}"
}

write_cli "${INSTALLED_ROOT}/usr/bin/podlaz" "${CLI_COMMIT}"
printf 'candidate-daemon\n' >"${INSTALLED_ROOT}/usr/bin/podlazd"
printf 'candidate-xray\n' >"${INSTALLED_ROOT}/usr/lib/podlaz/xray"

PODLAZ_PROVENANCE_CLI_PATH="${INSTALLED_ROOT}/usr/bin/podlaz"
PODLAZ_PROVENANCE_DAEMON_PATH="${INSTALLED_ROOT}/usr/bin/podlazd"
PODLAZ_PROVENANCE_XRAY_PATH="${INSTALLED_ROOT}/usr/lib/podlaz/xray"

fail() {
  printf 'test failure: %s\n' "$*" >&2
  return 1
}

dpkg-deb() {
  case "$1" in
    --field)
      printf '%s\n' "${EXPECTED_VERSION}"
      ;;
    -x)
      local dest="$3"
      mkdir -p "${dest}/usr/bin" "${dest}/usr/lib/podlaz"
      write_cli "${dest}/usr/bin/podlaz" "${EXPECTED_COMMIT}"
      printf 'candidate-daemon\n' >"${dest}/usr/bin/podlazd"
      printf 'candidate-xray\n' >"${dest}/usr/lib/podlaz/xray"
      ;;
    *) return 2 ;;
  esac
}

dpkg-query() {
  case "$1" in
    -W)
      case "$2" in
        *Status-Status*) printf '%s\n' "${INSTALLED_STATUS}" ;;
        *Version*) printf '%s\n' "${INSTALLED_VERSION}" ;;
        *) return 2 ;;
      esac
      ;;
    -L)
      printf '/usr/bin/podlaz\n/usr/bin/podlazd\n/usr/lib/podlaz/xray\n'
      ;;
    *) return 2 ;;
  esac
}

systemctl() {
  if [[ "$1" == is-active && "$2" == --quiet && "$3" == podlazd.service ]]; then
    return 0
  fi
  if [[ "$1" == show && "$2" == -p && "$3" == MainPID && "$4" == --value && "$5" == podlazd.service ]]; then
    printf '4242\n'
    return 0
  fi
  return 2
}

sudo() {
  [[ "$1" == -n ]] || return 2
  shift
  case "$1" in
    sha256sum)
      case "$2" in
        "${PODLAZ_PROVENANCE_CLI_PATH}") command sha256sum "${PODLAZ_PROVENANCE_CLI_PATH}" ;;
        "${PODLAZ_PROVENANCE_DAEMON_PATH}"|/proc/4242/exe) command sha256sum "${PODLAZ_PROVENANCE_DAEMON_PATH}" ;;
        "${PODLAZ_PROVENANCE_XRAY_PATH}") command sha256sum "${PODLAZ_PROVENANCE_XRAY_PATH}" ;;
        *) return 2 ;;
      esac
      ;;
    readlink)
      printf '%s\n' "${RUNNING_DAEMON_TARGET:-/usr/bin/podlazd}"
      ;;
    stat)
      case "${STAT_MODE:-match}:$4" in
        mismatch:/proc/4242/exe) printf '8:41\n' ;;
        *) printf '8:42\n' ;;
      esac
      ;;
    *) return 2 ;;
  esac
}

restore_installed_files() {
  write_cli "${PODLAZ_PROVENANCE_CLI_PATH}" "${CLI_COMMIT}"
  printf 'candidate-daemon\n' >"${PODLAZ_PROVENANCE_DAEMON_PATH}"
  printf 'candidate-xray\n' >"${PODLAZ_PROVENANCE_XRAY_PATH}"
}

run_success_case() (
  restore_installed_files
  assert_exact_podlaz_package_runtime_provenance /tmp/candidate.deb "${EXPECTED_COMMIT}"
)

run_status_mismatch_case() (
  INSTALLED_STATUS="config-files"
  if assert_exact_podlaz_package_runtime_provenance /tmp/candidate.deb "${EXPECTED_COMMIT}"; then
    printf 'package status mismatch was accepted\n' >&2
    exit 1
  fi
)

run_version_mismatch_case() (
  INSTALLED_VERSION="0.2.40"
  if assert_exact_podlaz_package_runtime_provenance /tmp/candidate.deb "${EXPECTED_COMMIT}"; then
    printf 'version mismatch was accepted\n' >&2
    exit 1
  fi
)

run_installed_binary_mismatch_case() (
  restore_installed_files
  printf 'foreign-cli\n' >"${PODLAZ_PROVENANCE_CLI_PATH}"
  if assert_exact_podlaz_package_runtime_provenance /tmp/candidate.deb "${EXPECTED_COMMIT}"; then
    printf 'installed CLI mismatch was accepted\n' >&2
    exit 1
  fi
)

run_xray_mismatch_case() (
  restore_installed_files
  printf 'foreign-xray\n' >"${PODLAZ_PROVENANCE_XRAY_PATH}"
  if assert_exact_podlaz_package_runtime_provenance /tmp/candidate.deb "${EXPECTED_COMMIT}"; then
    printf 'installed Xray mismatch was accepted\n' >&2
    exit 1
  fi
)

run_commit_mismatch_case() (
  restore_installed_files
  write_cli "${PODLAZ_PROVENANCE_CLI_PATH}" deadbeefdeadbeefdeadbeefdeadbeefdeadbeef
  if assert_exact_podlaz_package_runtime_provenance /tmp/candidate.deb "${EXPECTED_COMMIT}"; then
    printf 'source commit mismatch was accepted\n' >&2
    exit 1
  fi
)

run_stale_daemon_case() (
  restore_installed_files
  RUNNING_DAEMON_TARGET='/usr/bin/podlazd (deleted)'
  if assert_exact_podlaz_package_runtime_provenance /tmp/candidate.deb "${EXPECTED_COMMIT}"; then
    printf 'stale daemon executable was accepted\n' >&2
    exit 1
  fi
)

run_inode_mismatch_case() (
  restore_installed_files
  STAT_MODE=mismatch
  if assert_exact_podlaz_package_runtime_provenance /tmp/candidate.deb "${EXPECTED_COMMIT}"; then
    printf 'daemon inode mismatch was accepted\n' >&2
    exit 1
  fi
)

run_success_case
run_status_mismatch_case
run_version_mismatch_case
run_installed_binary_mismatch_case
run_xray_mismatch_case
run_commit_mismatch_case
run_stale_daemon_case
run_inode_mismatch_case

printf 'package runtime provenance tests passed\n'
