#!/usr/bin/env bash

# Composable installed-package provenance assertions for package E2E scenarios.
# Scenario-specific candidate/release authority and hash policy remain local.

assert_installed_podlaz_commit() {
  local expected_commit="$1" version_output
  if ! version_output="$(/usr/bin/podlaz version 2>/dev/null)"; then
    fail "installed podlaz version command failed"
  fi
  grep -F -- "${expected_commit}" <<<"${version_output}" >/dev/null || \
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
  local deb_path="$1" package_name="$2" expected_version installed_version
  expected_version="$(dpkg-deb --field "${deb_path}" Version)" || fail "cannot read package version: ${deb_path}"
  installed_version="$(dpkg-query -W -f='${Version}\n' "${package_name}" 2>/dev/null)" || \
    fail "package is not installed: ${package_name}"
  [[ "${installed_version}" == "${expected_version}" ]] || \
    fail "installed package version mismatch for ${package_name}"
}

assert_running_podlazd_matches_deb() {
  local deb_path="$1" extract_dir expected_hash main_pid running_exe running_hash
  extract_dir="$(mktemp -d)" || fail "cannot create package provenance temp directory"
  if ! dpkg-deb -x "${deb_path}" "${extract_dir}"; then
    rm -rf -- "${extract_dir}"
    fail "cannot extract package for daemon provenance"
  fi
  expected_hash="$(sha256sum "${extract_dir}/usr/bin/podlazd" | awk '{print $1}')" || {
    rm -rf -- "${extract_dir}"
    fail "cannot hash packaged podlazd"
  }
  rm -rf -- "${extract_dir}"

  main_pid="$(systemctl show -p MainPID --value podlazd.service)" || fail "cannot read podlazd.service MainPID"
  [[ "${main_pid}" =~ ^[1-9][0-9]*$ ]] || fail "podlazd.service has no running MainPID"
  running_exe="$(sudo -n readlink -f "/proc/${main_pid}/exe")" || fail "cannot resolve running podlazd executable"
  [[ "${running_exe}" == "/usr/bin/podlazd" ]] || fail "running daemon executable is not /usr/bin/podlazd"
  running_hash="$(sudo -n sha256sum "/proc/${main_pid}/exe" | awk '{print $1}')" || fail "cannot hash running podlazd"
  [[ "${running_hash}" == "${expected_hash}" ]] || fail "running podlazd does not match tested package"
}
