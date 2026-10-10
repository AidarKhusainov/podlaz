#!/usr/bin/env bash
set -euo pipefail

: "${PODLAZ_APT_SIGNING_FINGERPRINT:?Set independently verified production signing fingerprint}"
: "${PODLAZ_APT_EXPECTED_VERSION:?Set expected published Podlaz version}"
: "${PODLAZ_APT_URL:=https://aidarkhusainov.github.io/podlaz/apt}"

fingerprint="$(printf '%s' "${PODLAZ_APT_SIGNING_FINGERPRINT}" | tr -d '[:space:]' | tr '[:lower:]' '[:upper:]')"
[[ "${fingerprint}" =~ ^[0-9A-F]{40}$ ]] || {
  echo "public-apt: expected production fingerprint is invalid" >&2
  exit 2
}
[[ "${PODLAZ_APT_EXPECTED_VERSION}" =~ ^[0-9]+[.][0-9]+[.][0-9]+$ ]] || {
  echo "public-apt: expected version must be semver" >&2
  exit 2
}
[[ "${PODLAZ_APT_URL}" == https://aidarkhusainov.github.io/podlaz/apt ]] || {
  echo "public-apt: unexpected public repository origin" >&2
  exit 2
}

docker run --rm --network bridge -i \
  -e EXPECTED_FINGERPRINT="${fingerprint}" \
  -e EXPECTED_VERSION="${PODLAZ_APT_EXPECTED_VERSION}" \
  -e APT_URL="${PODLAZ_APT_URL}" \
  ubuntu:24.04 bash -euo pipefail -s <<'CONTAINER_SCRIPT'
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq --no-install-recommends ca-certificates curl gnupg
install -d -m 0755 /etc/apt/keyrings
curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
  "${APT_URL}/podlaz-archive-keyring.gpg" -o /tmp/podlaz-key.gpg
actual="$(gpg --batch --show-keys --with-colons /tmp/podlaz-key.gpg 2>/dev/null |
  awk -F: '$1 == "fpr" { print toupper($10); exit }')"
test "${actual}" = "${EXPECTED_FINGERPRINT}" || {
  echo "public-apt: published key differs from independently configured fingerprint" >&2
  exit 1
}
install -m 0644 /tmp/podlaz-key.gpg /etc/apt/keyrings/podlaz.gpg
printf 'Types: deb\nURIs: %s\nSuites: stable\nComponents: main\nArchitectures: amd64\nSigned-By: /etc/apt/keyrings/podlaz.gpg\n' "${APT_URL}" \
  >/etc/apt/sources.list.d/podlaz.sources
apt-get update -o APT::Get::List-Cleanup=1
apt-cache policy podlaz
candidate="$(apt-cache policy podlaz | awk '/Candidate:/ {print $2}')"
test "${candidate}" = "${EXPECTED_VERSION}" || {
  echo "public-apt: indexed candidate does not match deployed release" >&2
  exit 1
}
apt-cache policy podlaz | grep -F "${APT_URL}" >/dev/null || {
  echo "public-apt: candidate lacks expected HTTPS repository origin" >&2
  exit 1
}
mkdir -p /tmp/podlaz-public-apt
cd /tmp/podlaz-public-apt
apt-get download "podlaz=${EXPECTED_VERSION}"
package="podlaz_${EXPECTED_VERSION}_amd64.deb"
test -f "${package}"
test "$(dpkg-deb --field "${package}" Version)" = "${EXPECTED_VERSION}"
test "$(dpkg-deb --field "${package}" Architecture)" = amd64
dpkg-deb -x "${package}" package-root
test -x package-root/usr/bin/podlaz
test -x package-root/usr/bin/podlazd
test -x package-root/usr/lib/podlaz/xray
test -f package-root/usr/lib/systemd/system/podlazd.service
package-root/usr/bin/podlaz version | grep -Fx "podlaz version ${EXPECTED_VERSION}"
echo "public-apt: signed HTTPS index and exact package acquisition verified"
echo "public-apt: actual package install and daemon lifecycle are tested in the booted systemd guest"
CONTAINER_SCRIPT
