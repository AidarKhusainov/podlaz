#!/usr/bin/env bash
set -Eeuo pipefail

# Public HTTPS APT acceptance in a booted, disposable systemd guest.
# The guest shares only the runner's outbound network stack; NET_ADMIN is dropped.
ROOT="${RUNNER_TEMP:-/tmp}/podlaz-public-apt-guest"
GUEST="${ROOT}/rootfs"
MACHINE="podlaz-public-apt"
APT_URL="https://aidarkhusainov.github.io/podlaz/apt"
EXPECTED_FINGERPRINT="7C6F4EF9CDE73501E79CCC63E2F6F280766864C2"
PREVIOUS_VERSION="0.2.42"
CURRENT_VERSION="0.2.47"
PREVIOUS_COMMIT="6e933a9f1b471c83ba884ee1f0569286f1dafdce"
PREVIOUS_SHA256="62acca173c0618ef3c13c7bdd810a128d42fd8c9df38d951dfb314167e2659da"
: "${PODLAZ_APT_CURRENT_COMMIT:?Exact published release commit required}"
: "${PODLAZ_APT_CURRENT_SHA256:?Exact release package SHA256 required}"
: "${PODLAZ_APT_SIGNING_FINGERPRINT:?Independent signing authority required}"

fail() { printf 'public-apt-guest: %s\n' "$*" >&2; exit 1; }
[[ "$(printf %s "${PODLAZ_APT_SIGNING_FINGERPRINT}" | tr "[:lower:]" "[:upper:]")" == "${EXPECTED_FINGERPRINT}" ]] || fail "unexpected production signing fingerprint"
[[ "${PODLAZ_APT_CURRENT_COMMIT}" =~ ^[a-f0-9]{40}$ ]] || fail "invalid release commit"
[[ "${PODLAZ_APT_CURRENT_SHA256}" =~ ^[a-f0-9]{64}$ ]] || fail "invalid release package digest"

cleanup() {
  sudo -n machinectl terminate "${MACHINE}" >/dev/null 2>&1 || true
  if [[ -n "${NSPAWN_PID:-}" ]]; then
    wait "${NSPAWN_PID}" >/dev/null 2>&1 || true
  fi
  sudo -n rm -rf -- "${ROOT}"
}
trap cleanup EXIT
guest() { sudo -n systemd-run --machine="${MACHINE}" --expand-environment=no --wait --pipe --collect --quiet -- "$@"; }

sudo -n rm -rf -- "${ROOT}"
mkdir -p "${ROOT}"
sudo -n debootstrap --variant=minbase noble "${GUEST}" http://archive.ubuntu.com/ubuntu >"${ROOT}/debootstrap.log" 2>&1
printf '#!/bin/sh\nexit 101\n' | sudo -n tee "${GUEST}/usr/sbin/policy-rc.d" >/dev/null
sudo -n chmod 0755 "${GUEST}/usr/sbin/policy-rc.d"
sudo -n chroot "${GUEST}" /usr/bin/env DEBIAN_FRONTEND=noninteractive apt-get update >"${ROOT}/bootstrap-apt.log" 2>&1
sudo -n chroot "${GUEST}" /usr/bin/env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
  systemd systemd-sysv dbus ca-certificates curl gnupg sudo iproute2 nftables polkitd >>"${ROOT}/bootstrap-apt.log" 2>&1
sudo -n rm -f "${GUEST}/usr/sbin/policy-rc.d"
sudo -n install -d -m 0755 "${GUEST}/etc/apt/keyrings"
sudo -n install -m 0644 "${PWD}/scripts/e2e/lib/package_provenance.sh" "${GUEST}/tmp/provenance.sh"

sudo -n systemd-nspawn --quiet --boot --directory="${GUEST}" --machine="${MACHINE}" \
  --settings=no --drop-capability=CAP_NET_ADMIN,CAP_NET_RAW \
  --link-journal=no >"${ROOT}/nspawn.log" 2>&1 &
NSPAWN_PID=$!
for _ in $(seq 1 200); do
  if machinectl show "${MACHINE}" >/dev/null 2>&1 && guest /bin/true >/dev/null 2>&1; then break; fi
  sleep 0.2
done
guest /bin/true || fail "guest boot failed"
guest /bin/bash -lc 'state="$(timeout 30 systemctl is-system-running --wait 2>/dev/null || true)"; [[ "$state" == running || "$state" == degraded ]]'

guest curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
  "${APT_URL}/podlaz-archive-keyring.gpg" -o /tmp/podlaz-keyring.gpg
published="$(guest /bin/bash -lc "gpg --batch --show-keys --with-colons /tmp/podlaz-keyring.gpg 2>/dev/null | awk -F: '\$1 == \"fpr\" {print toupper(\$10); exit}'")"
[[ "${published}" == "${EXPECTED_FINGERPRINT}" ]] || fail "public key fingerprint mismatch"
guest install -m 0644 /tmp/podlaz-keyring.gpg /etc/apt/keyrings/podlaz-archive-keyring.gpg
guest /bin/bash -lc "printf 'Types: deb\\nURIs: %s\\nSuites: stable\\nComponents: main\\nArchitectures: amd64\\nSigned-By: /etc/apt/keyrings/podlaz-archive-keyring.gpg\\n' '${APT_URL}' >/etc/apt/sources.list.d/podlaz.sources"
guest apt-get update
guest apt-cache policy podlaz | grep -F "${APT_URL}" >/dev/null || fail "HTTPS origin absent"
guest apt-cache policy podlaz | grep -F "${PREVIOUS_VERSION}" >/dev/null || fail "baseline missing from public index"
guest apt-cache policy podlaz | grep -F "${CURRENT_VERSION}" >/dev/null || fail "candidate missing from public index"

for version in "${PREVIOUS_VERSION}" "${CURRENT_VERSION}"; do
  guest /bin/bash -lc "mkdir -p /tmp/podlaz-apt-packages && cd /tmp/podlaz-apt-packages && apt-get download 'podlaz=${version}'"
done
for version in "${PREVIOUS_VERSION}" "${CURRENT_VERSION}"; do
  expected="${PODLAZ_APT_CURRENT_SHA256}"
  if [[ "${version}" == "${PREVIOUS_VERSION}" ]]; then expected="${PREVIOUS_SHA256}"; fi
  printf '%s  %s\n' "${expected}" "${GUEST}/tmp/podlaz-apt-packages/podlaz_${version}_amd64.deb" | sudo -n sha256sum -c -
done

guest /usr/bin/env DEBIAN_FRONTEND=noninteractive apt-get install -y "podlaz=${PREVIOUS_VERSION}"
guest podlaz version | grep -Fx "podlaz version ${PREVIOUS_VERSION}" >/dev/null
guest /bin/bash -lc "source /tmp/provenance.sh; fail() { echo \"\$*\" >&2; return 1; }; assert_exact_podlaz_package_runtime_provenance '/tmp/podlaz-apt-packages/podlaz_${PREVIOUS_VERSION}_amd64.deb' '${PREVIOUS_COMMIT}'"
guest /usr/bin/env DEBIAN_FRONTEND=noninteractive apt-get upgrade -y
[[ "$(guest dpkg-query -W '-f=${Version}' podlaz)" == "${CURRENT_VERSION}" ]] || fail "upgrade did not install expected release"
guest podlaz version | grep -Fx "podlaz version ${CURRENT_VERSION}" >/dev/null
guest /bin/bash -lc "source /tmp/provenance.sh; fail() { echo \"\$*\" >&2; return 1; }; assert_exact_podlaz_package_runtime_provenance '/tmp/podlaz-apt-packages/podlaz_${CURRENT_VERSION}_amd64.deb' '${PODLAZ_APT_CURRENT_COMMIT}'"
echo "public-apt-guest: signed HTTPS install, upgrade, service and exact runtime provenance passed"
