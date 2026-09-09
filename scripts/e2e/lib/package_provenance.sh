#!/usr/bin/env bash

# Composable installed-package provenance assertions for package E2E scenarios.
# Scenario-specific candidate/release authority remains local.

: "${PODLAZ_PROVENANCE_CLI_PATH:=/usr/bin/podlaz}"
: "${PODLAZ_PROVENANCE_DAEMON_PATH:=/usr/bin/podlazd}"
: "${PODLAZ_PROVENANCE_XRAY_PATH:=/usr/lib/podlaz/xray}"

assert_installed_podlaz_commit() {
  local expected_commit="$1" version_output
  [[ -n "${expected_commit}" ]] || fail "expected Podlaz source commit is empty"
  if ! version_output="$("${PODLAZ_PROVENANCE_CLI_PATH}" version 2>/dev/null)"; then
    fail "installed podlaz version command failed"
  fi
  grep -Fx -- "commit: ${expected_commit}" <<<"${version_output}" >/dev/null || \
    fail "installed podlaz does not identify the tested commit"
}

assert_package_service_active() {
  local service_name="$1"
  systemctl is-active --quiet "${service_name}" || fail "required packaged service is not active: ${service_name}"
}

assert_native_deb_arch() {
  local deb_path="$1" expected_arch="$2" package_arch
  package_arch="$(dpkg-deb --field "${deb_path}" Architecture)" || fail "cannot read package architecture: ${deb_path}"
  [[ "${package_arch}" == "${expected_arch}" ]] || \
    fail "package architecture mismatch: expected ${expected_arch}, got ${package_arch}"
}

assert_installed_package_version_matches_deb() {
  local deb_path="$1" package_name="$2" expected_version installed_version installed_status
  expected_version="$(dpkg-deb --field "${deb_path}" Version)" || fail "cannot read package version: ${deb_path}"
  installed_status="$(dpkg-query -W -f='${db:Status-Status}\n' "${package_name}" 2>/dev/null)" || \
    fail "package is not installed: ${package_name}"
  [[ "${installed_status}" == "installed" ]] || fail "package state is not installed: ${package_name}"
  installed_version="$(dpkg-query -W -f='${Version}\n' "${package_name}" 2>/dev/null)" || \
    fail "cannot read installed package version: ${package_name}"
  [[ "${installed_version}" == "${expected_version}" ]] || \
    fail "installed package version mismatch for ${package_name}"
}

assert_installed_podlaz_files_match_deb() {
  local deb_path="$1" extract_dir package_paths
  local expected_cli expected_daemon expected_xray installed_cli installed_daemon installed_xray

  package_paths="$(dpkg-query -L podlaz 2>/dev/null)" || fail "cannot read installed Podlaz file ownership"
  for path in /usr/bin/podlaz /usr/bin/podlazd /usr/lib/podlaz/xray; do
    grep -Fx -- "${path}" <<<"${package_paths}" >/dev/null || fail "installed Podlaz package does not own ${path}"
  done

  extract_dir="$(mktemp -d)" || fail "cannot create package provenance temp directory"
  if ! dpkg-deb -x "${deb_path}" "${extract_dir}" >/dev/null 2>&1; then
    rm -rf -- "${extract_dir}"
    fail "cannot extract package for installed-file provenance"
  fi
  for path in usr/bin/podlaz usr/bin/podlazd usr/lib/podlaz/xray; do
    [[ -f "${extract_dir}/${path}" ]] || {
      rm -rf -- "${extract_dir}"
      fail "tested package is missing ${path}"
    }
  done

  expected_cli="$(sha256sum "${extract_dir}/usr/bin/podlaz" | awk '{print $1}')" || {
    rm -rf -- "${extract_dir}"
    fail "cannot hash packaged podlaz"
  }
  expected_daemon="$(sha256sum "${extract_dir}/usr/bin/podlazd" | awk '{print $1}')" || {
    rm -rf -- "${extract_dir}"
    fail "cannot hash packaged podlazd"
  }
  expected_xray="$(sha256sum "${extract_dir}/usr/lib/podlaz/xray" | awk '{print $1}')" || {
    rm -rf -- "${extract_dir}"
    fail "cannot hash packaged Xray"
  }
  rm -rf -- "${extract_dir}"

  installed_cli="$(sudo -n sha256sum "${PODLAZ_PROVENANCE_CLI_PATH}" | awk '{print $1}')" || fail "cannot hash installed podlaz"
  installed_daemon="$(sudo -n sha256sum "${PODLAZ_PROVENANCE_DAEMON_PATH}" | awk '{print $1}')" || fail "cannot hash installed podlazd"
  installed_xray="$(sudo -n sha256sum "${PODLAZ_PROVENANCE_XRAY_PATH}" | awk '{print $1}')" || fail "cannot hash installed Xray"

  [[ "${installed_cli}" == "${expected_cli}" ]] || fail "installed podlaz does not match tested package"
  [[ "${installed_daemon}" == "${expected_daemon}" ]] || fail "installed podlazd does not match tested package"
  [[ "${installed_xray}" == "${expected_xray}" ]] || fail "installed Xray does not match tested package"
}

assert_running_podlazd_matches_deb() {
  local deb_path="$1" extract_dir expected_hash installed_hash main_pid running_exe running_hash installed_inode running_inode
  extract_dir="$(mktemp -d)" || fail "cannot create package provenance temp directory"
  if ! dpkg-deb -x "${deb_path}" "${extract_dir}" >/dev/null 2>&1; then
    rm -rf -- "${extract_dir}"
    fail "cannot extract package for daemon provenance"
  fi
  expected_hash="$(sha256sum "${extract_dir}/usr/bin/podlazd" | awk '{print $1}')" || {
    rm -rf -- "${extract_dir}"
    fail "cannot hash packaged podlazd"
  }
  rm -rf -- "${extract_dir}"

  installed_hash="$(sudo -n sha256sum "${PODLAZ_PROVENANCE_DAEMON_PATH}" | awk '{print $1}')" || fail "cannot hash installed podlazd"
  [[ "${installed_hash}" == "${expected_hash}" ]] || fail "installed podlazd does not match tested package"

  main_pid="$(systemctl show -p MainPID --value podlazd.service)" || fail "cannot read podlazd.service MainPID"
  [[ "${main_pid}" =~ ^[1-9][0-9]*$ ]] || fail "podlazd.service has no running MainPID"
  running_exe="$(sudo -n readlink "/proc/${main_pid}/exe")" || fail "cannot resolve running podlazd executable"
  [[ "${running_exe}" == "/usr/bin/podlazd" ]] || fail "running daemon executable is stale or is not /usr/bin/podlazd"
  running_hash="$(sudo -n sha256sum "/proc/${main_pid}/exe" | awk '{print $1}')" || fail "cannot hash running podlazd"
  [[ "${running_hash}" == "${expected_hash}" ]] || fail "running podlazd does not match tested package"

  installed_inode="$(sudo -n stat -Lc '%d:%i' "${PODLAZ_PROVENANCE_DAEMON_PATH}")" || fail "cannot inspect installed podlazd inode"
  running_inode="$(sudo -n stat -Lc '%d:%i' "/proc/${main_pid}/exe")" || fail "cannot inspect running podlazd inode"
  [[ "${running_inode}" == "${installed_inode}" ]] || fail "running podlazd uses a stale replacement inode"
}

assert_exact_podlaz_package_runtime_provenance() {
  local deb_path="$1" expected_commit="$2"
  assert_installed_package_version_matches_deb "${deb_path}" podlaz
  assert_installed_podlaz_files_match_deb "${deb_path}"
  assert_installed_podlaz_commit "${expected_commit}"
  assert_package_service_active podlazd.service
  assert_running_podlazd_matches_deb "${deb_path}"
}
