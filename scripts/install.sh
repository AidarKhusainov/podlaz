#!/usr/bin/env bash
set -euo pipefail

readonly PODLAZ_REPOSITORY="AidarKhusainov/podlaz"
readonly PODLAZ_RELEASES_URL="https://github.com/${PODLAZ_REPOSITORY}/releases"

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'podlaz installer: required command is missing: %s\n' "$1" >&2
    exit 1
  }
}

require_command curl
require_command sha256sum
require_command dpkg
require_command apt-get
require_command mktemp

arch="$(dpkg --print-architecture)"
case "${arch}" in
  amd64|arm64)
    ;;
  *)
    printf 'podlaz installer: unsupported Debian architecture: %s (supported: amd64, arm64)\n' "${arch}" >&2
    exit 1
    ;;
esac

latest_url="$(curl -fsSL -o /dev/null -w '%{url_effective}' "${PODLAZ_RELEASES_URL}/latest")"
tag="${latest_url##*/}"
if [[ ! "${tag}" =~ ^v[0-9]+[.][0-9]+[.][0-9]+$ ]]; then
  printf 'podlaz installer: could not resolve a valid latest release tag from %s\n' "${latest_url}" >&2
  exit 1
fi

version="${tag#v}"
asset="podlaz_${version}_linux_${arch}.deb"
download_base="${PODLAZ_RELEASES_URL}/download/${tag}"

tmp_dir="$(mktemp -d)"
cleanup() {
  rm -rf -- "${tmp_dir}"
}
trap cleanup EXIT

manifest="${tmp_dir}/SHA256SUMS"
package="${tmp_dir}/${asset}"

printf 'Downloading Podlaz %s for %s...\n' "${version}" "${arch}"
curl -fsSL "${download_base}/SHA256SUMS" -o "${manifest}"
curl -fsSL "${download_base}/${asset}" -o "${package}"

expected_sha256="$(
  awk -v asset="${asset}" '
    $2 == asset || $2 == "*" asset {
      count++
      digest=$1
    }
    END {
      if (count == 1) {
        print digest
      }
    }
  ' "${manifest}"
)"

if [[ ! "${expected_sha256}" =~ ^[[:xdigit:]]{64}$ ]]; then
  printf 'podlaz installer: SHA256SUMS does not contain exactly one valid checksum for %s\n' "${asset}" >&2
  exit 1
fi

actual_sha256="$(sha256sum -- "${package}" | awk '{print $1}')"
if [[ "${actual_sha256,,}" != "${expected_sha256,,}" ]]; then
  printf 'podlaz installer: checksum mismatch for %s\n' "${asset}" >&2
  printf 'expected: %s\n' "${expected_sha256}" >&2
  printf 'actual:   %s\n' "${actual_sha256}" >&2
  exit 1
fi

printf 'Checksum verified. Installing Podlaz %s...\n' "${version}"
(
  cd "${tmp_dir}"
  if (( EUID == 0 )); then
    apt-get install -y -- "./${asset}"
  else
    require_command sudo
    sudo apt-get install -y -- "./${asset}"
  fi
)

printf 'Podlaz %s installed successfully.\n' "${version}"
