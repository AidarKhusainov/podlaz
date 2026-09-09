#!/usr/bin/env bash

# Prove that the package database, installed CLI/daemon bytes, and the running
# systemd MainPID all correspond to one exact .deb. Callers provide fail(),
# safe_name(), E2E_TMP_ROOT, and E2E_ARTIFACT_DIR from the E2E harness.
assert_exact_package_runtime_provenance() {
  local deb="$1" phase="$2"
  local package_record installed_status installed_version package_paths unpack_dir
  local package_cli package_daemon expected_cli_hash expected_daemon_hash
  local installed_cli_hash installed_daemon_hash service_state active_state sub_state main_pid
  local daemon_target running_daemon_hash installed_inode running_inode artifact

  package_record="$(dpkg-query -W -f='${db:Status-Status}\t${Version}\n' podlaz 2>/dev/null)" || {
    fail "${phase}: installed podlaz package could not be queried"
    return 1
  }
  IFS=$'\t' read -r installed_status installed_version <<<"${package_record}"
  [[ "${installed_status}" == "installed" ]] || {
    fail "${phase}: podlaz package state is ${installed_status:-unknown}, expected installed"
    return 1
  }

  local expected_version
  expected_version="$(dpkg-deb -f "${deb}" Version 2>/dev/null)" || {
    fail "${phase}: exact package version could not be read"
    return 1
  }
  [[ -n "${expected_version}" && "${installed_version}" == "${expected_version}" ]] || {
    fail "${phase}: installed package version does not match the exact .deb"
    return 1
  }

  package_paths="$(dpkg-query -L podlaz 2>/dev/null)" || {
    fail "${phase}: installed podlaz file ownership could not be queried"
    return 1
  }
  for path in /usr/bin/podlaz /usr/bin/podlazd; do
    grep -Fx -- "${path}" <<<"${package_paths}" >/dev/null || {
      fail "${phase}: installed package does not own ${path}"
      return 1
    }
  done

  unpack_dir="$(mktemp -d "${E2E_TMP_ROOT}/package-provenance.XXXXXX")" || {
    fail "${phase}: could not create private package provenance directory"
    return 1
  }
  chmod 0700 "${unpack_dir}"
  if ! dpkg-deb -x "${deb}" "${unpack_dir}" >/dev/null 2>&1; then
    fail "${phase}: exact package contents could not be extracted"
    return 1
  fi
  package_cli="${unpack_dir}/usr/bin/podlaz"
  package_daemon="${unpack_dir}/usr/bin/podlazd"
  [[ -f "${package_cli}" && -f "${package_daemon}" ]] || {
    fail "${phase}: exact package does not contain both installed binaries"
    return 1
  }

  expected_cli_hash="$(sha256sum "${package_cli}" | awk '{print $1}')" || return 1
  expected_daemon_hash="$(sha256sum "${package_daemon}" | awk '{print $1}')" || return 1
  installed_cli_hash="$(sudo -n sha256sum /usr/bin/podlaz | awk '{print $1}')" || {
    fail "${phase}: installed CLI bytes could not be hashed"
    return 1
  }
  installed_daemon_hash="$(sudo -n sha256sum /usr/bin/podlazd | awk '{print $1}')" || {
    fail "${phase}: installed daemon bytes could not be hashed"
    return 1
  }
  [[ "${installed_cli_hash}" == "${expected_cli_hash}" ]] || {
    fail "${phase}: installed CLI bytes do not match the exact .deb"
    return 1
  }
  [[ "${installed_daemon_hash}" == "${expected_daemon_hash}" ]] || {
    fail "${phase}: installed daemon bytes do not match the exact .deb"
    return 1
  }

  service_state="$(sudo -n systemctl show podlazd.service \
    --property=ActiveState --property=SubState --property=MainPID 2>/dev/null)" || {
    fail "${phase}: podlazd.service runtime identity could not be inspected"
    return 1
  }
  active_state=""
  sub_state=""
  main_pid=""
  while IFS='=' read -r key value; do
    case "${key}" in
      ActiveState) active_state="${value}" ;;
      SubState) sub_state="${value}" ;;
      MainPID) main_pid="${value}" ;;
    esac
  done <<<"${service_state}"
  [[ "${active_state}" == "active" && "${sub_state}" == "running" ]] || {
    fail "${phase}: podlazd.service is not active/running"
    return 1
  }
  [[ "${main_pid}" =~ ^[1-9][0-9]*$ ]] || {
    fail "${phase}: podlazd.service has no valid MainPID"
    return 1
  }

  daemon_target="$(sudo -n readlink "/proc/${main_pid}/exe" 2>/dev/null)" || {
    fail "${phase}: running daemon executable target could not be inspected"
    return 1
  }
  [[ "${daemon_target}" == "/usr/bin/podlazd" ]] || {
    fail "${phase}: MainPID is not executing the installed /usr/bin/podlazd"
    return 1
  }
  running_daemon_hash="$(sudo -n sha256sum "/proc/${main_pid}/exe" | awk '{print $1}')" || {
    fail "${phase}: running daemon executable bytes could not be hashed"
    return 1
  }
  [[ "${running_daemon_hash}" == "${expected_daemon_hash}" ]] || {
    fail "${phase}: running daemon bytes do not match the exact .deb"
    return 1
  }

  installed_inode="$(sudo -n stat -Lc '%d:%i' /usr/bin/podlazd 2>/dev/null)" || {
    fail "${phase}: installed daemon inode could not be inspected"
    return 1
  }
  running_inode="$(sudo -n stat -Lc '%d:%i' "/proc/${main_pid}/exe" 2>/dev/null)" || {
    fail "${phase}: running daemon inode could not be inspected"
    return 1
  }
  [[ "${running_inode}" == "${installed_inode}" ]] || {
    fail "${phase}: MainPID is executing a stale daemon inode"
    return 1
  }

  artifact="${E2E_ARTIFACT_DIR}/package-provenance-$(safe_name "${phase}").txt"
  {
    printf 'phase=%s\n' "${phase}"
    printf 'package_version=%s\n' "${expected_version}"
    printf 'cli_sha256=%s\n' "${expected_cli_hash}"
    printf 'daemon_sha256=%s\n' "${expected_daemon_hash}"
    printf 'main_pid=%s\n' "${main_pid}"
    printf 'daemon_executable=/usr/bin/podlazd\n'
  } >"${artifact}"

  rm -rf -- "${unpack_dir}"
}
