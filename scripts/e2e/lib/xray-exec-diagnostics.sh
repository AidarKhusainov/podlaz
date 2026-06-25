#!/usr/bin/env bash

# Redaction-safe diagnostics for packaged Xray exec failures.
# This helper intentionally avoids collecting profile URIs, subscription URLs,
# generated Xray config contents, provider tokens, private keys, or credentials.

collect_command_output() {
  local output="$1"
  shift
  "$@" >"${output}" 2>&1 || true
}

collect_daemon_proc_status() {
  local dir="$1" pid
  pid="$(systemctl show -p MainPID --value podlazd.service 2>/dev/null || true)"
  printf '%s\n' "${pid}" >"${dir}/podlazd.mainpid"
  if [[ -n "${pid}" && "${pid}" != "0" && -r "/proc/${pid}/status" ]]; then
    sudo -n cat "/proc/${pid}/status" >"${dir}/podlazd.proc.status" 2>&1 || true
  fi
}

collect_xray_binary_diagnostics() {
  local dir="$1" path="$2" label
  [[ -n "${path}" ]] || return 0
  label="$(safe_name "${path}")"
  local path_dir="${dir}/xray-${label}"
  mkdir -p "${path_dir}"
  printf '%s\n' "${path}" >"${path_dir}/path.txt"
  if [[ ! -e "${path}" && ! -L "${path}" ]]; then
    printf 'missing: %s\n' "${path}" >"${path_dir}/missing.txt"
    return 0
  fi
  collect_command_output "${path_dir}/namei.txt" namei -l "${path}"
  collect_command_output "${path_dir}/ls.txt" ls -l "${path}"
  collect_command_output "${path_dir}/stat.txt" stat -c 'mode=%a uid=%u gid=%g type=%F path=%n' "${path}"
  if command -v getcap >/dev/null 2>&1; then
    collect_command_output "${path_dir}/getcap.txt" getcap -v "${path}"
  else
    printf 'getcap not available on runner\n' >"${path_dir}/getcap.txt"
  fi
  collect_command_output "${path_dir}/findmnt.txt" findmnt -T "${path}" -o TARGET,FSTYPE,OPTIONS
  collect_command_output "${path_dir}/podlaz-xray-version.txt" sudo -n -u podlaz-xray -g podlaz-xray "${path}" version
}

write_xray_exec_diagnostic_summary() {
  local dir="$1" summary="${dir}/summary.txt" file saw_noexec=0 saw_nosuid=0 saw_caps=0 saw_lsm=0
  for file in "${dir}"/xray-*/findmnt.txt; do
    [[ -f "${file}" ]] || continue
    grep -Eq '(^|[[:space:],])noexec([[:space:],]|$)' "${file}" && saw_noexec=1
    grep -Eq '(^|[[:space:],])nosuid([[:space:],]|$)' "${file}" && saw_nosuid=1
  done
  for file in "${dir}"/xray-*/getcap.txt; do
    [[ -f "${file}" ]] || continue
    grep -Eq 'cap_[[:alnum:]_]+.*=' "${file}" && saw_caps=1
  done
  if [[ -s "${dir}/kernel-lsm-denials.txt" ]]; then
    saw_lsm=1
  fi

  {
    printf 'packaged Xray exec diagnostics captured in %s\n' "${dir}"
    printf 'redaction note: profile URIs, subscription URLs, generated Xray configs, provider tokens, and credentials are intentionally not collected\n'
    if [[ "${saw_noexec}" == "1" ]]; then
      printf 'candidate cause: Xray filesystem appears to include noexec; execve can fail before Xray starts\n'
    fi
    if [[ "${saw_nosuid}" == "1" ]]; then
      printf 'candidate cause: Xray filesystem appears to include nosuid; validate interaction with SUID/SGID bits or file capabilities\n'
    fi
    if [[ "${saw_caps}" == "1" ]]; then
      printf 'candidate cause: Xray has file capabilities; compare them with NoNewPrivileges and the podlazd CapabilityBoundingSet\n'
    fi
    if [[ "${saw_lsm}" == "1" ]]; then
      printf 'candidate cause: kernel/AppArmor/audit denial lines mention podlaz or xray; inspect kernel-lsm-denials.txt\n'
    fi
    if [[ "${saw_noexec}${saw_nosuid}${saw_caps}${saw_lsm}" == "0000" ]]; then
      printf 'no common EPERM cause was detected automatically; inspect systemd, identity, path, mount, capability, LSM, and proc artifacts\n'
    fi
  } >"${summary}"
  sed -e 's/^/diagnostic: /' "${summary}"
}

collect_packaged_xray_exec_diagnostics() {
  local name="$1" safe dir resolved_xray=""
  safe="$(safe_name "${name}")"
  dir="${E2E_ARTIFACT_DIR}/xray-exec-${safe}"
  mkdir -p "${dir}"
  date -u '+%Y-%m-%dT%H:%M:%SZ' >"${dir}/timestamp.txt"
  sudo -n systemctl show podlazd.service \
    -p User -p Group \
    -p NoNewPrivileges \
    -p RestrictSUIDSGID \
    -p CapabilityBoundingSet \
    -p AmbientCapabilities \
    -p MemoryDenyWriteExecute \
    -p ProtectSystem \
    -p ProtectHome \
    -p PrivateTmp \
    -p ProtectControlGroups \
    >"${dir}/podlazd.service.show" 2>&1 || true
  sudo -n systemctl status podlazd.service --no-pager >"${dir}/podlazd.service.status" 2>&1 || true
  sudo -n journalctl -u podlazd.service -n 300 --no-pager >"${dir}/podlazd.service.journal" 2>&1 || true
  id podlaz-xray >"${dir}/id-podlaz-xray.txt" 2>&1 || true
  getent passwd podlaz-xray >"${dir}/passwd-podlaz-xray.txt" 2>&1 || true
  getent group podlaz-xray >"${dir}/group-podlaz-xray.txt" 2>&1 || true
  resolved_xray="$(command -v xray 2>/dev/null || true)"
  printf '%s\n' "${resolved_xray}" >"${dir}/command-v-xray.txt"
  collect_xray_binary_diagnostics "${dir}" /usr/local/bin/xray
  if [[ -n "${resolved_xray}" && "${resolved_xray}" != "/usr/local/bin/xray" ]]; then
    collect_xray_binary_diagnostics "${dir}" "${resolved_xray}"
  fi
  if command -v aa-status >/dev/null 2>&1; then
    aa-status >"${dir}/aa-status.txt" 2>&1 || true
  else
    printf 'aa-status not available on runner\n' >"${dir}/aa-status.txt"
  fi
  { sudo -n journalctl -k -n 300 --no-pager || true; } | grep -Ei 'apparmor|audit|denied|xray|podlaz' >"${dir}/kernel-lsm-denials.txt" 2>&1 || true
  collect_daemon_proc_status "${dir}"
  write_xray_exec_diagnostic_summary "${dir}"
}
