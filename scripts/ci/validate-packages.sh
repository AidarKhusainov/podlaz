#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -lt 1 ]; then
  echo "usage: $0 <package.deb> [package.deb ...]" >&2
  exit 2
fi

for package in "$@"; do
  test -f "${package}"

  arch="$(dpkg-deb --field "${package}" Architecture)"
  version="$(dpkg-deb --field "${package}" Version)"
  contents="/tmp/podlaz-${arch}-package-contents.txt"
  service="/tmp/podlazd-${arch}.service"
  sysusers="/tmp/podlaz-${arch}.sysusers"
  control="/tmp/podlaz-${arch}-control"

  test "$(dpkg-deb --field "${package}" Package)" = podlaz
  test -n "${version}"
  case "${arch}" in
    amd64|arm64) ;;
    *) echo "unsupported package architecture: ${arch}" >&2; exit 1 ;;
  esac

  dpkg-deb --info "${package}"
  dpkg-deb --contents "${package}" | tee "${contents}"

  ! grep -E '(^| )\./usr/local(/|$)' "${contents}"
  ! grep -E '(^| )\./run(/|$)' "${contents}"
  ! grep -E '(^| )\./var/run(/|$)' "${contents}"
  ! grep -E '(^| )\./home(/|$)' "${contents}"
  ! grep -E '(^| )\./run/podlaz/generated(/|$)' "${contents}"
  grep -F './usr/bin/podlaz' "${contents}"
  grep -F './usr/bin/plz' "${contents}"
  grep -F './usr/bin/podlazd' "${contents}"
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
  ! grep -F './usr/share/metainfo/' "${contents}"
  ! grep -F './usr/share/applications/' "${contents}"
  ! grep -F './usr/share/icons/' "${contents}"

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
  grep -Fx 'RuntimeDirectory=podlaz' "${service}"
  grep -Fx 'RuntimeDirectoryMode=0711' "${service}"
  grep -Fx 'StateDirectory=podlaz' "${service}"
  grep -Fx 'StateDirectoryMode=0700' "${service}"
  grep -Fx 'NoNewPrivileges=yes' "${service}"
  grep -Fx 'CapabilityBoundingSet=CAP_CHOWN CAP_SETUID CAP_SETGID CAP_KILL CAP_NET_ADMIN' "${service}"
  grep -Fx 'AmbientCapabilities=CAP_SETUID CAP_KILL' "${service}"
  grep -Fx 'RestrictSUIDSGID=yes' "${service}"
  grep -Fx 'MemoryDenyWriteExecute=yes' "${service}"
  ! grep -E '^AmbientCapabilities=.*CAP_(NET_ADMIN|SETGID|SYS_ADMIN)' "${service}"

  grep -F 'unit=podlazd.service' "${control}/postinst"
  grep -F 'deb-systemd-helper enable "$unit"' "${control}/postinst"
  grep -F 'deb-systemd-helper update-state "$unit"' "${control}/postinst"
  grep -F 'deb-systemd-invoke start "$1"' "${control}/postinst"
  grep -F 'start_service "$unit"' "${control}/postinst"
  ! grep -E '(^|[^[:alnum:]_])systemctl[[:space:]]+(start|enable)([[:space:]]|$)' "${control}/postinst"
done

! grep -R -E '(^|[^[:alnum:]_])systemctl[[:space:]]+(start|enable)([[:space:]]|$)' packaging/debian

if [ -n "${PODLAZ_LINKAGE_ROOT:-}" ]; then
  file "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlaz" "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlazd"
  ldd "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlaz"
  ldd "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlazd"
  ldd "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlaz" | grep -F 'libc.so.6'
  ldd "${PODLAZ_LINKAGE_ROOT}/usr/bin/podlazd" | grep -F 'libc.so.6'
fi

for package in "$@"; do
  lintian --fail-on error "${package}"
done 2>&1 | tee /tmp/podlaz-lintian.txt
