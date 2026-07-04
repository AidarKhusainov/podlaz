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
require_cmd go
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
tun_tmp="$(mktemp -d)"
cleanup() {
  chmod -R u+w "${xray_tmp}" "${tun_tmp}" 2>/dev/null || true
  rm -rf "${xray_tmp}" "${tun_tmp}"
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

tun_gopath="${tun_tmp}/gopath"
GOPATH="${tun_gopath}" \
GO111MODULE=on \
CGO_ENABLED=0 \
GOOS=linux \
GOARCH="${go_arch}" \
  go install "${TUN2SOCKS_MODULE}@${TUN2SOCKS_VERSION}"

tun2socks_binary="${tun_gopath}/bin/tun2socks"
if [ ! -x "${tun2socks_binary}" ]; then
  tun2socks_binary="${tun_gopath}/bin/linux_${go_arch}/tun2socks"
fi
if [ ! -x "${tun2socks_binary}" ]; then
  echo "tun2socks binary not found after Go module build for linux/${go_arch}" >&2
  find "${tun_gopath}/bin" -maxdepth 3 -type f -print >&2 2>/dev/null || true
  exit 1
fi
install -m 0755 "${tun2socks_binary}" "${runtime_lib_dir}/tun2socks"

module_cache="$(GOPATH="${tun_gopath}" go env GOMODCACHE)"
tun2socks_license="${module_cache}/${TUN2SOCKS_MODULE}@${TUN2SOCKS_VERSION}/LICENSE"
if [ ! -f "${tun2socks_license}" ]; then
  echo "tun2socks license not found in Go module cache: ${tun2socks_license}" >&2
  exit 1
fi
install -m 0644 "${tun2socks_license}" "${third_party_doc_dir}/tun2socks-LICENSE"

test -x "${runtime_lib_dir}/xray"
test -x "${runtime_lib_dir}/tun2socks"
test -s "${third_party_doc_dir}/xray-LICENSE"
test -s "${third_party_doc_dir}/tun2socks-LICENSE"
