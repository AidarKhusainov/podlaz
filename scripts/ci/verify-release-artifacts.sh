#!/usr/bin/env bash
set -euo pipefail

: "${VERSION:?VERSION is required}"
: "${EXPECTED_RELEASE_MANIFEST_SHA256:?EXPECTED_RELEASE_MANIFEST_SHA256 is required}"

release_dir="${1:-dist/release}"
manifest="${release_dir}/SHA256SUMS"
expected_assets=(
  "podlaz_${VERSION}_linux_amd64.tar.gz"
  "podlaz_${VERSION}_linux_arm64.tar.gz"
  "podlaz_${VERSION}_linux_amd64.deb"
  "podlaz_${VERSION}_linux_arm64.deb"
)

[[ -d "${release_dir}" && ! -L "${release_dir}" ]] || {
  echo "release artifact directory is missing or invalid: ${release_dir}" >&2
  exit 1
}
[[ -f "${manifest}" && ! -L "${manifest}" ]] || {
  echo "release checksum manifest is missing or invalid: ${manifest}" >&2
  exit 1
}

actual_manifest_sha256="$(sha256sum "${manifest}" | awk '{print $1}')"
if [[ "${actual_manifest_sha256}" != "${EXPECTED_RELEASE_MANIFEST_SHA256}" ]]; then
  echo "release checksum manifest digest mismatch" >&2
  echo "expected: ${EXPECTED_RELEASE_MANIFEST_SHA256}" >&2
  echo "actual:   ${actual_manifest_sha256}" >&2
  exit 1
fi

for asset in "${expected_assets[@]}"; do
  path="${release_dir}/${asset}"
  [[ -f "${path}" && ! -L "${path}" ]] || {
    echo "release artifact is missing or invalid: ${path}" >&2
    exit 1
  }
done

mapfile -t manifest_assets < <(awk 'NF == 2 {print $2}' "${manifest}" | LC_ALL=C sort)
mapfile -t expected_sorted < <(printf '%s\n' "${expected_assets[@]}" | LC_ALL=C sort)
if [[ "${#manifest_assets[@]}" -ne "${#expected_sorted[@]}" ]]   || [[ "$(printf '%s\n' "${manifest_assets[@]}")" != "$(printf '%s\n' "${expected_sorted[@]}")" ]]; then
  echo "release checksum manifest does not describe exactly the expected immutable assets" >&2
  exit 1
fi

(
  cd "${release_dir}"
  sha256sum -c SHA256SUMS
)

printf 'verified release artifact manifest sha256=%s\n' "${actual_manifest_sha256}"
