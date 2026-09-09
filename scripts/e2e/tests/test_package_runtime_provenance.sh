#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib/package_runtime_provenance.sh
source "${SCRIPT_DIR}/../lib/package_runtime_provenance.sh"

TEST_ROOT="$(mktemp -d)"
trap 'rm -rf -- "${TEST_ROOT}"' EXIT
E2E_TMP_ROOT="${TEST_ROOT}/private"
E2E_ARTIFACT_DIR="${TEST_ROOT}/public"
mkdir -p "${E2E_TMP_ROOT}" "${E2E_ARTIFACT_DIR}"
EXPECTED_VERSION="0.2.41"
INSTALLED_STATUS="installed"
INSTALLED_VERSION="${EXPECTED_VERSION}"
INSTALLED_ROOT="${TEST_ROOT}/installed"
mkdir -p "${INSTALLED_ROOT}/usr/bin"
printf 'candidate-cli\n' >"${INSTALLED_ROOT}/usr/bin/podlaz"
printf 'candidate-daemon\n' >"${INSTALLED_ROOT}/usr/bin/podlazd"

fail() {
  printf 'test failure: %s\n' "$*" >&2
  return 1
}

safe_name() {
  printf '%s' "$1" | tr -c 'A-Za-z0-9._-' '_'
}

dpkg-deb() {
  case "$1" in
    -f)
      printf '%s\n' "${EXPECTED_VERSION}"
      ;;
    -x)
      local dest="$3"
      mkdir -p "${dest}/usr/bin"
      printf 'candidate-cli\n' >"${dest}/usr/bin/podlaz"
      printf 'candidate-daemon\n' >"${dest}/usr/bin/podlazd"
      ;;
    *)
      return 2
      ;;
  esac
}

dpkg-query() {
  case "$1" in
    -W)
      printf '%s\t%s\n' "${INSTALLED_STATUS}" "${INSTALLED_VERSION}"
      ;;
    -L)
      printf '/usr/bin/podlaz\n/usr/bin/podlazd\n'
      ;;
    *)
      return 2
      ;;
  esac
}

sudo() {
  [[ "$1" == "-n" ]] || return 2
  shift
  case "$1" in
    sha256sum)
      case "$2" in
        /usr/bin/podlaz)
          command sha256sum "${INSTALLED_ROOT}/usr/bin/podlaz"
          ;;
        /usr/bin/podlazd|/proc/4242/exe)
          command sha256sum "${INSTALLED_ROOT}/usr/bin/podlazd"
          ;;
        *) return 2 ;;
      esac
      ;;
    systemctl)
      cat <<'EOF'
ActiveState=active
SubState=running
MainPID=4242
EOF
      ;;
    readlink)
      printf '/usr/bin/podlazd\n'
      ;;
    stat)
      printf '8:42\n'
      ;;
    *)
      return 2
      ;;
  esac
}

run_success_case() (
  assert_exact_package_runtime_provenance /tmp/candidate.deb candidate || exit 1
  [[ -s "${E2E_ARTIFACT_DIR}/package-provenance-candidate.txt" ]] || {
    printf 'missing provenance artifact\n' >&2
    exit 1
  }
)

run_package_state_mismatch_case() (
  INSTALLED_STATUS="config-files"
  if assert_exact_package_runtime_provenance /tmp/candidate.deb candidate; then
    printf 'non-installed package state was accepted\n' >&2
    exit 1
  fi
)

run_version_mismatch_case() (
  INSTALLED_VERSION="0.2.40"
  if assert_exact_package_runtime_provenance /tmp/candidate.deb candidate; then
    printf 'version mismatch was accepted\n' >&2
    exit 1
  fi
)

run_installed_binary_mismatch_case() (
  printf 'foreign-cli\n' >"${INSTALLED_ROOT}/usr/bin/podlaz"
  if assert_exact_package_runtime_provenance /tmp/candidate.deb candidate; then
    printf 'installed CLI mismatch was accepted\n' >&2
    exit 1
  fi
)

run_stale_daemon_case() (
  sudo() {
    [[ "$1" == "-n" ]] || return 2
    shift
    case "$1" in
      sha256sum)
        case "$2" in
          /usr/bin/podlaz) command sha256sum "${INSTALLED_ROOT}/usr/bin/podlaz" ;;
          /usr/bin/podlazd|/proc/4242/exe) command sha256sum "${INSTALLED_ROOT}/usr/bin/podlazd" ;;
          *) return 2 ;;
        esac
        ;;
      systemctl)
        printf 'ActiveState=active\nSubState=running\nMainPID=4242\n'
        ;;
      readlink)
        printf '/usr/bin/podlazd (deleted)\n'
        ;;
      stat)
        printf '8:42\n'
        ;;
      *) return 2 ;;
    esac
  }
  if assert_exact_package_runtime_provenance /tmp/candidate.deb candidate; then
    printf 'stale daemon executable was accepted\n' >&2
    exit 1
  fi
)

run_inode_mismatch_case() (
  sudo() {
    [[ "$1" == "-n" ]] || return 2
    shift
    case "$1" in
      sha256sum)
        case "$2" in
          /usr/bin/podlaz) command sha256sum "${INSTALLED_ROOT}/usr/bin/podlaz" ;;
          /usr/bin/podlazd|/proc/4242/exe) command sha256sum "${INSTALLED_ROOT}/usr/bin/podlazd" ;;
          *) return 2 ;;
        esac
        ;;
      systemctl)
        printf 'ActiveState=active\nSubState=running\nMainPID=4242\n'
        ;;
      readlink)
        printf '/usr/bin/podlazd\n'
        ;;
      stat)
        if [[ "$4" == "/proc/4242/exe" ]]; then printf '8:41\n'; else printf '8:42\n'; fi
        ;;
      *) return 2 ;;
    esac
  }
  if assert_exact_package_runtime_provenance /tmp/candidate.deb candidate; then
    printf 'daemon inode mismatch was accepted\n' >&2
    exit 1
  fi
)

run_success_case
run_package_state_mismatch_case
run_version_mismatch_case
run_installed_binary_mismatch_case
printf 'candidate-cli\n' >"${INSTALLED_ROOT}/usr/bin/podlaz"
run_stale_daemon_case
run_inode_mismatch_case

printf 'package runtime provenance tests passed\n'
