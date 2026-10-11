#!/usr/bin/env bash
set -euo pipefail

readonly PODLAZ_APT_URL="https://aidarkhusainov.github.io/podlaz/apt"
readonly PODLAZ_APT_FINGERPRINT="7C6F4EF9CDE73501E79CCC63E2F6F280766864C2"
readonly PODLAZ_APT_KEYRING="/etc/apt/keyrings/podlaz-archive-keyring.gpg"
readonly PODLAZ_APT_SOURCE="/etc/apt/sources.list.d/podlaz.sources"

fail() {
  printf 'podlaz installer: %s\n' "$*" >&2
  exit 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command is missing: $1"
}

as_root() {
  if (( EUID == 0 )); then
    "$@"
  else
    sudo "$@"
  fi
}

require_command curl
require_command dpkg
require_command apt-get
require_command apt-cache
require_command mktemp
require_command install

if (( EUID != 0 )); then
  require_command sudo
fi

arch="$(dpkg --print-architecture)"
case "${arch}" in
  amd64|arm64) ;;
  *) fail "unsupported Debian architecture: ${arch} (supported: amd64, arm64)" ;;
esac

# GnuPG is required for inspecting the downloaded signing key before trusting it.
if ! command -v gpg >/dev/null 2>&1; then
  printf 'Installing GnuPG for Podlaz repository key verification...\n'
  as_root apt-get update
  as_root apt-get install -y --no-install-recommends gnupg
  require_command gpg
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf -- "${tmp_dir}"' EXIT
key_file="${tmp_dir}/podlaz-archive-keyring.gpg"
source_file="${tmp_dir}/podlaz.sources"

printf 'Verifying the Podlaz APT signing key...\n'
curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
  "${PODLAZ_APT_URL}/podlaz-archive-keyring.gpg" -o "${key_file}"

# Reject extra primary keys; a matching fingerprint alone must not authorize
# arbitrary additional signing identities in the same keyring.
actual_fingerprint="$(
  gpg --batch --show-keys --with-colons "${key_file}" 2>/dev/null |
    awk -F: '
      $1 == "pub" { primary_keys++ }
      $1 == "fpr" && primary_keys == 1 && first == "" { first=toupper($10) }
      END { if (primary_keys == 1) print first }
    '
)"
if [[ "${actual_fingerprint}" != "${PODLAZ_APT_FINGERPRINT}" ]]; then
  fail 'APT signing key fingerprint mismatch or unexpected additional key; no repository was configured'
fi

printf 'Types: deb\nURIs: %s\nSuites: stable\nComponents: main\nArchitectures: %s\nSigned-By: %s\n' \
  "${PODLAZ_APT_URL}" "${arch}" "${PODLAZ_APT_KEYRING}" >"${source_file}"

# Do not overwrite preexisting, possibly administrator-managed repository state.
# Identical files are safe to reuse when running the installer again.
if [[ -e "${PODLAZ_APT_SOURCE}" || -L "${PODLAZ_APT_SOURCE}" ||
      -e "${PODLAZ_APT_KEYRING}" || -L "${PODLAZ_APT_KEYRING}" ]]; then
  if [[ ! -f "${PODLAZ_APT_SOURCE}" || ! -f "${PODLAZ_APT_KEYRING}" ]] ||
      ! cmp -s "${source_file}" "${PODLAZ_APT_SOURCE}" ||
      ! cmp -s "${key_file}" "${PODLAZ_APT_KEYRING}"; then
    fail 'existing Podlaz APT configuration differs; inspect it manually before installing'
  fi
else
  as_root install -d -m 0755 /etc/apt/keyrings
  as_root install -m 0644 "${key_file}" "${PODLAZ_APT_KEYRING}"
  as_root install -m 0644 "${source_file}" "${PODLAZ_APT_SOURCE}"
fi

# Refresh the Podlaz index strictly without unrelated third-party repositories
# breaking this operation or clearing their cached package indexes.
printf 'Refreshing the signed Podlaz APT index...\n'
as_root apt-get \
  -o "Dir::Etc::sourcelist=${PODLAZ_APT_SOURCE}" \
  -o 'Dir::Etc::sourceparts=-' \
  -o 'APT::Get::List-Cleanup=0' \
  -o 'APT::Update::Error-Mode=any' \
  update

policy="$(apt-cache policy podlaz)"
candidate="$(awk '/^[[:space:]]*Candidate:/ { print $2; exit }' <<<"${policy}")"
if [[ -z "${candidate}" || "${candidate}" == '(none)' ]] ||
    ! grep -Fq "${PODLAZ_APT_URL} stable/main ${arch} Packages" <<<"${policy}"; then
  fail 'no Podlaz candidate from the signed stable repository; refusing to install'
fi

printf 'Installing Podlaz %s through APT...\n' "${candidate}"
as_root apt-get install -y -- "podlaz=${candidate}"
printf 'Podlaz %s installed. Future updates use normal APT upgrades.\n' "${candidate}"
