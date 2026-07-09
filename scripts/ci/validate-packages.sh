#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -lt 1 ]; then
  echo "usage: $0 <package.deb> [package.deb ...]" >&2
  exit 2
fi

if ! command -v gzip >/dev/null 2>&1; then
  echo "gzip is required to inspect compressed packaged documentation" >&2
  exit 2
fi

# shellcheck source=scripts/ci/package-obsolete-tun-gate.sh
source scripts/ci/package-obsolete-tun-gate.sh

assert_no_match() {
  local pattern="$1"
  local file="$2"
  if grep -E "${pattern}" "${file}"; then
    echo "unexpected match for pattern: ${pattern}" >&2
    exit 1
  fi
}

assert_no_fixed_match() {
  local pattern="$1"
  local file="$2"
  if grep -F "${pattern}" "${file}"; then
    echo "unexpected fixed-string match: ${pattern}" >&2
    exit 1
  fi
}

validate_elf_architecture() {
  local package="$1"
  local arch="$2"
  local path="$3"
  local label="$4"
  local extracted="/tmp/podlaz-${arch}-${label}"
  local file_output="/tmp/podlaz-${arch}-${label}.file.txt"

  dpkg-deb --fsys-tarfile "${package}" | tar -xOf - ".${path}" > "${extracted}"
  chmod 0755 "${extracted}"
  file "${extracted}" | tee "${file_output}"

  case "${arch}" in
    amd64)
      grep -E 'ELF 64-bit.*x86-64' "${file_output}"
      ;;
    arm64)
      grep -E 'ELF 64-bit.*(ARM aarch64|aarch64)' "${file_output}"
      ;;
    *)
      echo "unsupported package architecture: ${arch}" >&2
      exit 1
      ;;
  esac
}

validate_binary_architecture() {
  local package="$1"
  local arch="$2"
  local binary="$3"
  validate_elf_architecture "${package}" "${arch}" "/usr/bin/${binary}" "${binary}"
}

validate_executable_mode() {
  local package="$1"
  local path="$2"
  local listing
  listing="$(dpkg-deb --fsys-tarfile "${package}" | tar -tvf - ".${path}")"
  printf '%s\n' "${listing}"
  grep -E '^-rwxr-xr-x' <<<"${listing}"
}

validate_bash_completion_protocol_boundary() {
  local extracted_root="$1"
  local bash_completion="${extracted_root}/usr/share/bash-completion/completions/podlaz"
  local pattern

  # These are literal generated Bash snippets, not expressions evaluated by this
  # validation script.
  # shellcheck disable=SC2016
  local -a required_literal_patterns=(
    'value-only display fallback'
    'COMPREPLY=("${values[@]}")'
  )
  # shellcheck disable=SC2016
  local -a forbidden_literal_patterns=(
    'matches+=("$line")'
    'COMPREPLY+=("$line")'
    'COMPREPLY+=("${line}")'
    'COMPREPLY=("${matches[@]}")'
  )

  test -f "${bash_completion}"
  for pattern in "${required_literal_patterns[@]}"; do
    grep -F "${pattern}" "${bash_completion}"
  done
  for pattern in "${forbidden_literal_patterns[@]}"; do
    assert_no_fixed_match "${pattern}" "${bash_completion}"
  done
}

run_native_packaged_xray_schema_test() {
  local package="$1"
  local arch="$2"
  local host_arch
  host_arch="$(dpkg --print-architecture)"
  if [ "${arch}" != "${host_arch}" ]; then
    echo "Skipping packaged Xray config test for ${arch} on ${host_arch} host"
    return 0
  fi
  local xray="/tmp/podlaz-${arch}-packaged-xray-schema-test"
  dpkg-deb --fsys-tarfile "${package}" | tar -xOf - ./usr/lib/podlaz/xray > "${xray}"
  chmod 0755 "${xray}"
  PODLAZ_PACKAGED_XRAY_PATH="${xray}" go test ./internal/daemon -run '^TestPackagedXrayAcceptsPinnedTunConfigs$' -count=1
}

for package in "$@"; do
  test -f "${package}"

  arch="$(dpkg-deb --field "${package}" Architecture)"
  version="$(dpkg-deb --field "${package}" Version)"
  contents="/tmp/podlaz-${arch}-package-contents.txt"
  service="/tmp/podlazd-${arch}.service"
  sysusers="/tmp/podlaz-${arch}.sysusers"
  control="/tmp/podlaz-${arch}-control"
  depends="/tmp/podlaz-${arch}-depends.txt"
  extracted_root="/tmp/podlaz-${arch}-package-root"

  test "$(dpkg-deb --field "${package}" Package)" = podlaz
  test -n "${version}"
  case "${arch}" in
    amd64|arm64) ;;
    *) echo "unsupported package architecture: ${arch}" >&2; exit 1 ;;
  esac

  dpkg-deb --info "${package}"
  dpkg-deb --contents "${package}" | tee "${contents}"
  dpkg-deb --field "${package}" Depends | tee "${depends}"
  rm -rf "${extracted_root}"
  mkdir -p "${extracted_root}"
  dpkg-deb -x "${package}" "${extracted_root}"
  assert_no_obsolete_tun_artifacts "${extracted_root}" "extracted package root for ${package}"
  validate_bash_completion_protocol_boundary "${extracted_root}"

  assert_no_match '(^| )\./usr/local(/|$)' "${contents}"
  assert_no_match '(^| )\./run(/|$)' "${contents}"
  assert_no_match '(^| )\./var/run(/|$)' "${contents}"
  assert_no_match '(^| )\./home(/|$)' "${contents}"
  assert_no_match '(^| )\./run/podlaz/generated(/|$)' "${contents}"
  grep -F './usr/bin/podlaz' "${contents}"
  grep -F './usr/bin/plz' "${contents}"
  grep -F './usr/bin/podlazd' "${contents}"
  grep -F './usr/lib/podlaz/' "${contents}"
  grep -F './usr/lib/podlaz/xray' "${contents}"
  grep -F './usr/lib/systemd/system/podlazd.service' "${contents}"
  grep -F './usr/lib/sysusers.d/podlaz.conf' "${contents}"
  grep -F './usr/share/bash-completion/completions/podlaz' "${contents}"
  grep -F './usr/share/bash-completion/completions/plz' "${contents}"
  grep -F './usr/share/zsh/vendor-completions/_podlaz' "${contents}"
  grep -F './usr/share/zsh/vendor-completions/_plz' "${contents}"
  grep -F './usr/share/fish/vendor_completions.d/podlaz.fish' "${contents}"
  grep -F './usr/share/fish/vendor_completions.d/plz.fish' "${contents}"
  grep -F './usr/share/polkit-1/actions/io.github.aidarkhusainov.podlaz.policy' "${contents}"
  grep -F './usr/share/man/man1/podlaz.1.gz' "${contents}"
  grep -F './usr/share/man/man8/podlazd.8.gz' "${contents}"
  grep -F './usr/share/doc/podlaz/third-party/xray-LICENSE' "${contents}"
  assert_no_fixed_match './usr/share/metainfo/' "${contents}"
  assert_no_fixed_match './usr/share/applications/' "${contents}"
  assert_no_fixed_match './usr/share/icons/' "${contents}"

  grep -F 'libc6' "${depends}"
  grep -F 'systemd' "${depends}"
  grep -F 'ca-certificates' "${depends}"
  grep -F 'iproute2' "${depends}"
  grep -F 'nftables' "${depends}"
  grep -F 'systemd-resolved' "${depends}"
  grep -E 'polkitd|policykit-1' "${depends}"

  validate_binary_architecture "${package}" "${arch}" podlaz
  validate_binary_architecture "${package}" "${arch}" podlazd
  validate_elf_architecture "${package}" "${arch}" "/usr/lib/podlaz/xray" "xray"
  validate_executable_mode "${package}" "/usr/lib/podlaz/xray"
  run_native_packaged_xray_schema_test "${package}" "${arch}"

  dpkg-deb --fsys-tarfile "${package}" | tar -xOf - ./usr/lib/systemd/system/podlazd.service > "${service}"
  dpkg-deb --fsys-tarfile "${package}" | tar -xOf - ./usr/lib/sysusers.d/podlaz.conf > "${sysusers}"
  rm -rf "${control}"
  dpkg-deb --control "${package}" "${control}"

  test -f "${control}/postinst"
  grep -F 'u podlaz ' "${sysusers}"
  grep -F 'u podlaz-xray ' "${sysusers}"
  grep -Fx 'User=root' "${service}"
  grep -Fx 'Group=podlaz' "${service}"
  grep -Fx 'UMask=0077' "${service}"
  grep -Fx 'Environment=PODLAZ_SERVICE=systemd' "${service}"
  grep -Fx 'Environment=PODLAZ_POLKIT_AUTHORIZATION=required' "${service}"
  grep -Fx 'Environment=PODLAZ_XRAY_PATH=/usr/lib/podlaz/xray' "${service}"
  assert_no_fixed_match "PODLAZ_${legacy_tun_helper_upper}_PATH" "${service}"
  grep -Fx 'RuntimeDirectory=podlaz' "${service}"
  grep -Fx 'RuntimeDirectoryMode=0711' "${service}"
  grep -Fx 'StateDirectory=podlaz' "${service}"
  grep -Fx 'StateDirectoryMode=0700' "${service}"
  grep -Fx 'NoNewPrivileges=yes' "${service}"
  grep -Fx 'CapabilityBoundingSet=CAP_CHOWN CAP_SETUID CAP_SETGID CAP_KILL CAP_NET_ADMIN' "${service}"
  grep -Fx 'AmbientCapabilities=CAP_SETUID CAP_KILL CAP_NET_ADMIN' "${service}"
  grep -Fx 'RestrictSUIDSGID=yes' "${service}"
  grep -Fx 'MemoryDenyWriteExecute=yes' "${service}"
  assert_no_match '^AmbientCapabilities=.*CAP_(CHOWN|SETGID|SYS_ADMIN)' "${service}"

  grep -F 'unit=podlazd.service' "${control}/postinst"
  grep -F "deb-systemd-helper enable \"\$unit\"" "${control}/postinst"
  grep -F "deb-systemd-helper update-state \"\$unit\"" "${control}/postinst"
  grep -F "deb-systemd-invoke start \"\$1\"" "${control}/postinst"
  grep -F "start_service \"\$unit\"" "${control}/postinst"
  assert_no_match '(^|[^[:alnum:]_])systemctl[[:space:]]+(start|enable)([[:space:]]|$)' "${control}/postinst"
done

if grep -R -E '(^|[^[:alnum:]_])systemctl[[:space:]]+(start|enable)([[:space:]]|$)' packaging/debian; then
  echo "Debian maintainer scripts must not call systemctl start/enable directly" >&2
  exit 1
fi

if [ -n "${PODLAZ_LINKAGE_ROOT:-}" ]; then
  file "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlaz" "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlazd" "${PODLAZ_LINKAGE_ROOT}/usr/lib/podlaz/xray"
  ldd "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlaz"
  ldd "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlazd"
  ldd "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlaz" | grep -F 'libc.so.6'
  ldd "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlazd"
  ldd "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlazd" | grep -F 'libc.so.6'
fi

for package in "$@"; do
  lintian \
    --fail-on error \
    --suppress-tags statically-linked-binary,unstripped-binary-or-object \
    "${package}"
done 2>&1 | tee /tmp/podlaz-lintian.txt
