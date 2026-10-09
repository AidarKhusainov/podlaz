#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf 'usage: %s OUTPUT_DIR PACKAGE.deb [PACKAGE.deb ...]\n' "${0##*/}" >&2
  exit 2
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'build-apt-repository: required command is missing: %s\n' "$1" >&2
    exit 2
  }
}

normalize_fingerprint() {
  tr -d '[:space:]' <<<"$1" | tr '[:lower:]' '[:upper:]'
}

(($# >= 2)) || usage

: "${PODLAZ_APT_SIGNING_KEY_FILE:?PODLAZ_APT_SIGNING_KEY_FILE is required}"
: "${PODLAZ_APT_SIGNING_FINGERPRINT:?PODLAZ_APT_SIGNING_FINGERPRINT is required}"

for command in apt-ftparchive awk cmp dpkg-deb find gpg gpgv gzip install mktemp mv sha256sum; do
  require_command "${command}"
done

output_arg="$1"
shift
output_parent="$(dirname -- "${output_arg}")"
output_base="$(basename -- "${output_arg}")"
mkdir -p -- "${output_parent}"
output_parent="$(cd -- "${output_parent}" && pwd)"
output_dir="${output_parent}/${output_base}"
[[ ! -e "${output_dir}" ]] || {
  printf 'build-apt-repository: output already exists: %s\n' "${output_dir}" >&2
  exit 2
}

signing_key_file="$(readlink -f -- "${PODLAZ_APT_SIGNING_KEY_FILE}")"
[[ -f "${signing_key_file}" && ! -L "${PODLAZ_APT_SIGNING_KEY_FILE}" ]] || {
  printf 'build-apt-repository: signing key is unavailable or is not a regular file\n' >&2
  exit 2
}
expected_fingerprint="$(normalize_fingerprint "${PODLAZ_APT_SIGNING_FINGERPRINT}")"
[[ "${expected_fingerprint}" =~ ^[0-9A-F]{40,64}$ ]] || {
  printf 'build-apt-repository: signing fingerprint must be 40-64 hexadecimal characters\n' >&2
  exit 2
}

signing_passphrase_file=""
if [[ -n "${PODLAZ_APT_SIGNING_PASSPHRASE_FILE:-}" ]]; then
  signing_passphrase_file="$(readlink -f -- "${PODLAZ_APT_SIGNING_PASSPHRASE_FILE}")"
  [[ -f "${signing_passphrase_file}" && ! -L "${PODLAZ_APT_SIGNING_PASSPHRASE_FILE}" ]] || {
    printf 'build-apt-repository: signing passphrase file is unavailable or is not a regular file\n' >&2
    exit 2
  }
fi

case "${PODLAZ_APT_TEST_FAIL_STAGE:-}" in
  ""|after-metadata|after-signing) ;;
  *)
    printf 'build-apt-repository: unsupported test failure stage\n' >&2
    exit 2
    ;;
esac

stage="$(mktemp -d "${output_parent}/.podlaz-apt-repository.XXXXXX")"
gnupg_home="$(mktemp -d)"
cleanup() {
  rm -rf -- "${stage:-}" "${gnupg_home:-}"
}
trap cleanup EXIT
chmod 0700 "${gnupg_home}"

repo_root="${stage}/apt"
pool_dir="${repo_root}/pool/main/p/podlaz"
install -d -m 0755 "${pool_dir}"

declare -A seen_package_versions=()
declare -A seen_arches=()
source_packages=()
pool_packages=()
package_arches=()
for package_arg in "$@"; do
  [[ -f "${package_arg}" && ! -L "${package_arg}" ]] || {
    printf 'build-apt-repository: package is unavailable or is not a regular file: %s\n' "${package_arg}" >&2
    exit 2
  }
  package="$(readlink -f -- "${package_arg}")"
  [[ "$(dpkg-deb --field "${package}" Package)" == podlaz ]] || {
    printf 'build-apt-repository: package is not podlaz: %s\n' "${package}" >&2
    exit 1
  }
  architecture="$(dpkg-deb --field "${package}" Architecture)"
  case "${architecture}" in
    amd64|arm64) ;;
    *)
      printf 'build-apt-repository: unsupported Debian architecture: %s\n' "${architecture}" >&2
      exit 1
      ;;
  esac
  seen_arches["${architecture}"]=1
  version="$(dpkg-deb --field "${package}" Version)"
  [[ -n "${version}" && "${version}" != */* ]] || {
    printf 'build-apt-repository: invalid Debian version in %s\n' "${package}" >&2
    exit 1
  }
  package_identity="${architecture}|${version}"
  [[ -z "${seen_package_versions[${package_identity}]:-}" ]] || {
    printf 'build-apt-repository: duplicate Podlaz version for %s: %s\n' "${architecture}" "${version}" >&2
    exit 1
  }
  seen_package_versions["${package_identity}"]=1

  filename="podlaz_${version}_linux_${architecture}.deb"
  target="${pool_dir}/${filename}"
  install -m 0644 -- "${package}" "${target}"
  cmp -s -- "${package}" "${target}" || {
    printf 'build-apt-repository: copied package bytes changed: %s\n' "${filename}" >&2
    exit 1
  }
  source_packages+=("${package}")
  pool_packages+=("${target}")
  package_arches+=("${architecture}")
done

architectures=()
for architecture in amd64 arm64; do
  [[ -n "${seen_arches[${architecture}]:-}" ]] || continue
  architectures+=("${architecture}")
  index_dir="${repo_root}/dists/stable/main/binary-${architecture}"
  install -d -m 0755 "${index_dir}"
  (
    cd -- "${repo_root}"
    apt-ftparchive --arch "${architecture}" packages pool/main/p/podlaz >"dists/stable/main/binary-${architecture}/Packages"
    gzip -9n -c "dists/stable/main/binary-${architecture}/Packages" >"dists/stable/main/binary-${architecture}/Packages.gz"
  )
  expected_count=0
  for package_architecture in "${package_arches[@]}"; do
    if [[ "${package_architecture}" == "${architecture}" ]]; then
      expected_count=$((expected_count + 1))
    fi
  done
  package_count="$(grep -c '^Package: podlaz

if [[ "${PODLAZ_APT_TEST_FAIL_STAGE:-}" == after-metadata ]]; then
  printf 'build-apt-repository: intentional failure after metadata generation\n' >&2
  exit 97
fi

gpg --batch --homedir "${gnupg_home}" --import "${signing_key_file}" >/dev/null 2>&1
mapfile -t secret_fingerprints < <(
  gpg --batch --homedir "${gnupg_home}" --with-colons --list-secret-keys 2>/dev/null |
    awk -F: '
      $1 == "sec" { want = 1; next }
      want && $1 == "fpr" { print toupper($10); want = 0 }
    '
)
[[ "${#secret_fingerprints[@]}" -eq 1 ]] || {
  printf 'build-apt-repository: signing key file must contain exactly one primary secret key\n' >&2
  exit 1
}
[[ "${secret_fingerprints[0]}" == "${expected_fingerprint}" ]] || {
  printf 'build-apt-repository: signing key fingerprint does not match configured fingerprint\n' >&2
  exit 1
}

keyring="${repo_root}/podlaz-archive-keyring.gpg"
fingerprint_file="${repo_root}/podlaz-archive-keyring.fingerprint"
gpg --batch --homedir "${gnupg_home}" --export-options export-minimal --export "${expected_fingerprint}" >"${keyring}"
[[ -s "${keyring}" ]] || {
  printf 'build-apt-repository: public signing key export is empty\n' >&2
  exit 1
}
exported_fingerprint="$(
  gpg --batch --show-keys --with-colons "${keyring}" 2>/dev/null |
    awk -F: '$1 == "fpr" { print toupper($10); exit }'
)"
[[ "${exported_fingerprint}" == "${expected_fingerprint}" ]] || {
  printf 'build-apt-repository: exported public key fingerprint mismatch\n' >&2
  exit 1
}
printf '%s\n' "${expected_fingerprint}" >"${fingerprint_file}"

sign_options=(
  --batch
  --yes
  --homedir "${gnupg_home}"
  --local-user "${expected_fingerprint}"
  --pinentry-mode loopback
)
if [[ -n "${signing_passphrase_file}" ]]; then
  sign_options+=(--passphrase-file "${signing_passphrase_file}")
else
  sign_options+=(--passphrase "")
fi

gpg "${sign_options[@]}" \
  --digest-algo SHA256 \
  --clearsign \
  --output "${repo_root}/dists/stable/InRelease" \
  "${repo_root}/dists/stable/Release"
gpg "${sign_options[@]}" \
  --digest-algo SHA256 \
  --armor \
  --detach-sign \
  --output "${repo_root}/dists/stable/Release.gpg" \
  "${repo_root}/dists/stable/Release"

gpgv --keyring "${keyring}" "${repo_root}/dists/stable/InRelease" >/dev/null 2>&1
gpgv --keyring "${keyring}" "${repo_root}/dists/stable/Release.gpg" "${repo_root}/dists/stable/Release" >/dev/null 2>&1

if [[ "${PODLAZ_APT_TEST_FAIL_STAGE:-}" == after-signing ]]; then
  printf 'build-apt-repository: intentional failure after signing\n' >&2
  exit 98
fi

find "${stage}" -type d -exec chmod 0755 {} +
find "${stage}" -type f -exec chmod 0644 {} +
mv -- "${stage}" "${output_dir}"
stage=""

printf 'signed APT repository prepared for atomic publication: %s\n' "${output_dir}"
 "${index_dir}/Packages" || true)"
  [[ "${package_count}" -eq "${expected_count}" ]] || {
    printf 'build-apt-repository: %s package index count mismatch\n' "${architecture}" >&2
    exit 1
  }
done

architecture_list="${architectures[*]}"
(
  cd -- "${repo_root}"
  apt-ftparchive \
    -o APT::FTPArchive::Release::Origin=Podlaz \
    -o APT::FTPArchive::Release::Label=Podlaz \
    -o APT::FTPArchive::Release::Suite=stable \
    -o APT::FTPArchive::Release::Codename=stable \
    -o "APT::FTPArchive::Release::Architectures=${architecture_list}" \
    -o APT::FTPArchive::Release::Components=main \
    -o APT::FTPArchive::Release::Description='Podlaz signed stable APT repository' \
    release dists/stable >dists/stable/Release
)

for i in "${!pool_packages[@]}"; do
  pool_package="${pool_packages[${i}]}"
  source_package="${source_packages[${i}]}"
  architecture="${package_arches[${i}]}"
  index_dir="${repo_root}/dists/stable/main/binary-${architecture}"
  filename="pool/main/p/podlaz/$(basename -- "${pool_package}")"
  digest="$(sha256sum -- "${source_package}" | awk '{print $1}')"
  awk -v filename="Filename: ${filename}" -v digest="SHA256: ${digest}" '
    BEGIN { RS = ""; found = 0 }
    index($0, filename) && index($0, digest) { found++ }
    END { exit(found == 1 ? 0 : 1) }
  ' "${index_dir}/Packages" || {
    printf 'build-apt-repository: package index lost exact checksum provenance for %s\n' "${filename}" >&2
    exit 1
  }
done

if [[ "${PODLAZ_APT_TEST_FAIL_STAGE:-}" == after-metadata ]]; then
  printf 'build-apt-repository: intentional failure after metadata generation\n' >&2
  exit 97
fi

gpg --batch --homedir "${gnupg_home}" --import "${signing_key_file}" >/dev/null 2>&1
mapfile -t secret_fingerprints < <(
  gpg --batch --homedir "${gnupg_home}" --with-colons --list-secret-keys 2>/dev/null |
    awk -F: '
      $1 == "sec" { want = 1; next }
      want && $1 == "fpr" { print toupper($10); want = 0 }
    '
)
[[ "${#secret_fingerprints[@]}" -eq 1 ]] || {
  printf 'build-apt-repository: signing key file must contain exactly one primary secret key\n' >&2
  exit 1
}
[[ "${secret_fingerprints[0]}" == "${expected_fingerprint}" ]] || {
  printf 'build-apt-repository: signing key fingerprint does not match configured fingerprint\n' >&2
  exit 1
}

keyring="${repo_root}/podlaz-archive-keyring.gpg"
fingerprint_file="${repo_root}/podlaz-archive-keyring.fingerprint"
gpg --batch --homedir "${gnupg_home}" --export-options export-minimal --export "${expected_fingerprint}" >"${keyring}"
[[ -s "${keyring}" ]] || {
  printf 'build-apt-repository: public signing key export is empty\n' >&2
  exit 1
}
exported_fingerprint="$(
  gpg --batch --show-keys --with-colons "${keyring}" 2>/dev/null |
    awk -F: '$1 == "fpr" { print toupper($10); exit }'
)"
[[ "${exported_fingerprint}" == "${expected_fingerprint}" ]] || {
  printf 'build-apt-repository: exported public key fingerprint mismatch\n' >&2
  exit 1
}
printf '%s\n' "${expected_fingerprint}" >"${fingerprint_file}"

sign_options=(
  --batch
  --yes
  --homedir "${gnupg_home}"
  --local-user "${expected_fingerprint}"
  --pinentry-mode loopback
)
if [[ -n "${signing_passphrase_file}" ]]; then
  sign_options+=(--passphrase-file "${signing_passphrase_file}")
else
  sign_options+=(--passphrase "")
fi

gpg "${sign_options[@]}" \
  --digest-algo SHA256 \
  --clearsign \
  --output "${repo_root}/dists/stable/InRelease" \
  "${repo_root}/dists/stable/Release"
gpg "${sign_options[@]}" \
  --digest-algo SHA256 \
  --armor \
  --detach-sign \
  --output "${repo_root}/dists/stable/Release.gpg" \
  "${repo_root}/dists/stable/Release"

gpgv --keyring "${keyring}" "${repo_root}/dists/stable/InRelease" >/dev/null 2>&1
gpgv --keyring "${keyring}" "${repo_root}/dists/stable/Release.gpg" "${repo_root}/dists/stable/Release" >/dev/null 2>&1

if [[ "${PODLAZ_APT_TEST_FAIL_STAGE:-}" == after-signing ]]; then
  printf 'build-apt-repository: intentional failure after signing\n' >&2
  exit 98
fi

find "${stage}" -type d -exec chmod 0755 {} +
find "${stage}" -type f -exec chmod 0644 {} +
mv -- "${stage}" "${output_dir}"
stage=""

printf 'signed APT repository prepared for atomic publication: %s\n' "${output_dir}"
