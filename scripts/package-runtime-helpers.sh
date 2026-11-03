#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "usage: $0 <package-root> <deb-arch> <go-arch>" >&2
  exit 2
fi

package_root="$1"
deb_arch="$2"
go_arch="$3"
manifest="packaging/runtime-helpers.env"

# shellcheck disable=SC2034
: "${go_arch}"

# shellcheck disable=SC1090
. "${manifest}"

require_cmd() {
  local cmd="$1"
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    echo "${cmd} is required to package podlaz runtime helpers" >&2
    exit 2
  fi
}

runtime_cache_dir="${PODLAZ_RUNTIME_CACHE_DIR:-dist/runtime-cache}"
runtime_lib_dir="${package_root}/usr/lib/podlaz"
third_party_doc_dir="${package_root}/usr/share/doc/podlaz/third-party"

require_cmd curl
require_cmd sha256sum
require_cmd unzip

mkdir -p "${runtime_cache_dir}" "${runtime_lib_dir}" "${third_party_doc_dir}"

xray_asset=""
xray_sha256=""
case "${deb_arch}" in
  amd64)
    xray_asset="${XRAY_AMD64_ASSET}"
    xray_sha256="${XRAY_AMD64_SHA256}"
    ;;
  arm64)
    xray_asset="${XRAY_ARM64_ASSET}"
    xray_sha256="${XRAY_ARM64_SHA256}"
    ;;
  *)
    echo "unsupported Debian architecture for runtime helpers: ${deb_arch}" >&2
    exit 2
    ;;
esac

xray_url="https://github.com/XTLS/Xray-core/releases/download/${XRAY_VERSION}/${xray_asset}"
xray_archive="${runtime_cache_dir}/${xray_asset}"
if [ ! -f "${xray_archive}" ]; then
  curl --fail --location --proto '=https' --tlsv1.2 --show-error --output "${xray_archive}" "${xray_url}"
fi
printf '%s  %s\n' "${xray_sha256}" "${xray_archive}" | sha256sum --check --status

xray_tmp="$(mktemp -d)"
cleanup() {
  chmod -R u+w "${xray_tmp}" 2>/dev/null || true
  rm -rf "${xray_tmp}"
}
trap cleanup EXIT

unzip -q "${xray_archive}" -d "${xray_tmp}"
xray_binary="$(find "${xray_tmp}" -maxdepth 2 -type f -name xray | head -n 1)"
if [ -z "${xray_binary}" ]; then
  echo "xray binary not found in ${xray_asset}" >&2
  exit 1
fi
install -m 0755 "${xray_binary}" "${runtime_lib_dir}/xray"

xray_license="$(find "${xray_tmp}" -maxdepth 2 -type f \( -name LICENSE -o -name LICENSE.txt -o -name COPYING \) | head -n 1)"
if [ -z "${xray_license}" ]; then
  echo "xray license not found in ${xray_asset}" >&2
  exit 1
fi
install -m 0644 "${xray_license}" "${third_party_doc_dir}/xray-LICENSE"

test -x "${runtime_lib_dir}/xray"
test -s "${third_party_doc_dir}/xray-LICENSE"

# Stage the upstream inherited-FD binary separately; podlazd never selects it.
nm_xray_asset=""
nm_xray_sha256=""
case "${deb_arch}" in
  amd64)
    nm_xray_asset="${XRAY_NM_AMD64_ASSET}"
    nm_xray_sha256="${XRAY_NM_AMD64_SHA256}"
    ;;
  arm64)
    nm_xray_asset="${XRAY_NM_ARM64_ASSET}"
    nm_xray_sha256="${XRAY_NM_ARM64_SHA256}"
    ;;
esac
# Different releases reuse asset names. Cache by version to prevent aliasing.
nm_cache_dir="${runtime_cache_dir}/nm-owned/${XRAY_NM_VERSION}"
nm_archive="${nm_cache_dir}/${nm_xray_asset}"
mkdir -p "${nm_cache_dir}"
if [ ! -f "${nm_archive}" ]; then
  curl --fail --location --proto '=https' --tlsv1.2 --show-error \
    --output "${nm_archive}" \
    "https://github.com/XTLS/Xray-core/releases/download/${XRAY_NM_VERSION}/${nm_xray_asset}"
fi
printf '%s  %s\n' "${nm_xray_sha256}" "${nm_archive}" | sha256sum --check --status

nm_extract_dir="${xray_tmp}/nm-owned"
mkdir -p "${nm_extract_dir}"
unzip -q "${nm_archive}" -d "${nm_extract_dir}"
nm_xray_binary="$(find "${nm_extract_dir}" -maxdepth 2 -type f -name xray | head -n 1)"
if [ -z "${nm_xray_binary}" ]; then
  echo "NM Xray binary missing from verified archive" >&2
  exit 1
fi
install -m 0755 "${nm_xray_binary}" "${runtime_lib_dir}/xray-nm-owned"

nm_xray_license="$(find "${nm_extract_dir}" -maxdepth 2 -type f \( -name LICENSE -o -name LICENSE.txt -o -name COPYING \) | head -n 1)"
if [ -z "${nm_xray_license}" ]; then
  echo "NM Xray license missing from verified archive" >&2
  exit 1
fi
install -m 0644 "${nm_xray_license}" "${third_party_doc_dir}/xray-nm-owned-LICENSE"
test -x "${runtime_lib_dir}/xray-nm-owned"
test -s "${third_party_doc_dir}/xray-nm-owned-LICENSE"
