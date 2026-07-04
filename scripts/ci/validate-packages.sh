#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -lt 1 ]; then
  echo "usage: $0 <package.deb> [package.deb ...]" >&2
  exit 2
fi

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

for package in "$@"; do
  test -f "${package}"

  arch="$(dpkg-deb --field "${package}" Architecture)"
  version="$(dpkg-deb --field "${package}" Version)"
  contents="/tmp/podlaz-${arch}-package-contents.txt"
  service="/tmp/podlazd-${arch}.service"
  sysusers="/tmp/podlaz-${arch}.sysusers"
  control="/tmp/podlaz-${arch}-control"
  depends="/tmp/podlaz-${arch}-depends.txt"

  test "$(dpkg-deb --field "${package}" Package)" = podlaz
  test -n "${version}"
  case "${arch}" in
    amd64|arm64) ;;
    *) echo "unsupported package architecture: ${arch}" >&2; exit 1 ;;
  esac

  dpkg-deb --info "${package}"
  dpkg-deb --contents "${package}" | tee "${contents}"
  dpkg-deb --field "${package}" Depends | tee "${depends}"

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
  grep -F './usr/lib/podlaz/tun2socks' "${contents}"
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
  grep -F './usr/share/doc/podlaz/third-party/tun2socks-LICENSE' "${contents}"
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
  validate_elf_architecture "${package}" "${arch}" "/usr/lib/podlaz/tun2socks" "tun2socks"
  validate_executable_mode "${package}" "/usr/lib/podlaz/xray"
  validate_executable_mode "${package}" "/usr/lib/podlaz/tun2socks"

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
  grep -Fx 'Environment=PODLAZ_XRAY_PATH=/usr/lib/podlaz/xray' "${service}"
  grep -Fx 'Environment=PODLAZ_TUN2SOCKS_PATH=/usr/lib/podlaz/tun2socks' "${service}"
  grep -Fx 'RuntimeDirectory=podlaz' "${service}"
  grep -Fx 'RuntimeDirectoryMode=0711' "${service}"
  grep -Fx 'StateDirectory=podlaz' "${service}"
  grep -Fx 'StateDirectoryMode=0700' "${service}"
  grep -Fx 'NoNewPrivileges=yes' "${service}"
  grep -Fx 'CapabilityBoundingSet=CAP_CHOWN CAP_SETUID CAP_SETGID CAP_KILL CAP_NET_ADMIN' "${service}"
  grep -Fx 'AmbientCapabilities=CAP_SETUID CAP_KILL' "${service}"
  grep -Fx 'RestrictSUIDSGID=yes' "${service}"
  grep -Fx 'MemoryDenyWriteExecute=yes' "${service}"
  assert_no_match '^AmbientCapabilities=.*CAP_(NET_ADMIN|SETGID|SYS_ADMIN)' "${service}"

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
  file "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlaz" "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlazd" "${PODLAZ_LINKAGE_ROOT}/usr/lib/podlaz/xray" "${PODLAZ_LINKAGE_ROOT}/usr/lib/podlaz/tun2socks"
  ldd "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlaz"
  ldd "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlazd"
  ldd "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlaz" | grep -F 'libc.so.6'
  ldd "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlazd" | grep -F 'libc.so.6'
fi

for package in "$@"; do
  lintian --fail-on error "${package}"
done 2>&1 | tee /tmp/podlaz-lintian.txt
