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

docker run --rm --network bridge \
  -e EXPECTED_FINGERPRINT="${fingerprint}" \
  -e EXPECTED_VERSION="${PODLAZ_APT_EXPECTED_VERSION}" \
  -e APT_URL="${PODLAZ_APT_URL}" \
  ubuntu:24.04 bash -euo pipefail -c '
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y -qq --no-install-recommends ca-certificates curl gnupg
    install -d -m 0755 /etc/apt/keyrings
    curl --fail --silent --show-error --location --proto "=https" --tlsv1.2 \
      "${APT_URL}/podlaz-archive-keyring.gpg" -o /tmp/podlaz-key.gpg
    actual="$(gpg --batch --show-keys --with-colons /tmp/podlaz-key.gpg 2>/dev/null |
      awk -F: '\''$1 == "fpr" { print toupper($10); exit }'\'')"
    test "${actual}" = "${EXPECTED_FINGERPRINT}" || {
      echo "public-apt: published key differs from independently configured fingerprint" >&2
      exit 1
    }
    install -m 0644 /tmp/podlaz-key.gpg /etc/apt/keyrings/podlaz.gpg
    printf "Types: deb\nURIs: %s\nSuites: stable\nComponents: main\nArchitectures: amd64\nSigned-By: /etc/apt/keyrings/podlaz.gpg\n" "${APT_URL}" \
      >/etc/apt/sources.list.d/podlaz.sources
    apt-get update -o APT::Get::List-Cleanup=1
    apt-cache policy podlaz
    candidate="$(apt-cache policy podlaz | awk '\''/Candidate:/ {print $2}'\'')"
    test "${candidate}" = "${EXPECTED_VERSION}" || {
      echo "public-apt: indexed candidate does not match deployed release" >&2
      exit 1
    }
    apt-cache policy podlaz | grep -F "${APT_URL}" >/dev/null || {
      echo "public-apt: candidate lacks expected HTTPS repository origin" >&2
      exit 1
    }
    apt-get install -y --no-install-recommends "podlaz=${EXPECTED_VERSION}"
    test "$(dpkg-query -W -f="\''${Version}'\''" podlaz)" = "${EXPECTED_VERSION}"
    podlaz version | grep -Fx "podlaz version ${EXPECTED_VERSION}"
    test -f /usr/lib/systemd/system/podlazd.service
    test -x /usr/bin/podlazd
    echo "public-apt: HTTPS signed index, scoped key, install, version, and packaged service unit verified"
    echo "public-apt: service runtime is qualified separately in the systemd guest"
  '
