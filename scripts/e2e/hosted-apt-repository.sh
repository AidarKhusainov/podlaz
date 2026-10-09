#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "${SCRIPT_DIR}/../.." && pwd)"
BUILDER="${REPO_ROOT}/scripts/ci/build-apt-repository.sh"

: "${E2E_TMP_ROOT:=${RUNNER_TEMP:-/tmp}/podlaz-apt-repository-private}"
: "${E2E_ARTIFACT_DIR:=${RUNNER_TEMP:-/tmp}/podlaz-apt-repository-public}"
: "${PODLAZ_APT_OUTPUT_DIR:=${E2E_TMP_ROOT}/site}"

REPORT="${E2E_ARTIFACT_DIR}/hosted-apt-repository.txt"
PRIVATE_ROOT="${E2E_TMP_ROOT}/private"
GUEST_ROOT="${E2E_TMP_ROOT}/guest"
MACHINE="podlaz-apt-repository"
NSPAWN_PID=""

PREVIOUS_VERSION_EXPECTED="${PODLAZ_E2E_PREVIOUS_VERSION:-0.2.42}"
PREVIOUS_COMMIT="${PODLAZ_E2E_PREVIOUS_COMMIT:-6e933a9f1b471c83ba884ee1f0569286f1dafdce}"
PREVIOUS_SHA256="${PODLAZ_E2E_PREVIOUS_SHA256:-62acca173c0618ef3c13c7bdd810a128d42fd8c9df38d951dfb314167e2659da}"
CANDIDATE_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-}"

EVIDENCE_KEYS=(
  repository.signature
  repository.fingerprint
  repository.checksum_provenance
  repository.rerun
  repository.failure_atomicity
  repository.key_failure
  repository.rotation_boundary
  apt.update
  apt.install
  apt.version
  apt.service
  apt.upgrade
  apt.runtime_provenance
  runner.network_isolation
)

fail() {
  printf 'hosted-apt-repository: %s\n' "$*" >&2
  return 1
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || fail "required command is missing: $1"
}

record_report() {
  install -d -m 0700 "${E2E_ARTIFACT_DIR}"
  : >"${REPORT}"
  for key in "${EVIDENCE_KEYS[@]}"; do
    printf '%s=pass\n' "${key}" >>"${REPORT}"
  done
  chmod 0600 "${REPORT}"
}

validate_report() {
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || fail "public report is missing"
  [[ "$(wc -l <"${REPORT}")" -eq "${#EVIDENCE_KEYS[@]}" ]] || fail "public report line count mismatch"
  for key in "${EVIDENCE_KEYS[@]}"; do
    [[ "$(grep -Fxc "${key}=pass" "${REPORT}")" -eq 1 ]] || fail "public report is missing successful evidence: ${key}"
  done
  if grep -Ev '^[a-z0-9_.-]+=pass$' "${REPORT}" >/dev/null; then
    fail "public report contains non-normalized data"
  fi
}

stop_guest() {
  set +e
  sudo -n machinectl terminate "${MACHINE}" >/dev/null 2>&1 || true
  for _ in $(seq 1 100); do
    if ! machinectl show "${MACHINE}" >/dev/null 2>&1; then
      break
    fi
    sleep 0.1
  done
  if [[ -n "${NSPAWN_PID}" ]]; then
    kill "${NSPAWN_PID}" >/dev/null 2>&1 || true
    wait "${NSPAWN_PID}" >/dev/null 2>&1 || true
    NSPAWN_PID=""
  fi
  set -e
}

cleanup() {
  stop_guest
}
trap cleanup EXIT

guest_exec() {
  sudo -n systemd-run --machine="${MACHINE}" --expand-environment=no --wait --pipe --collect --quiet -- "$@"
}

normalize_fingerprint() {
  tr -d '[:space:]' <<<"$1" | tr '[:lower:]' '[:upper:]'
}

primary_builder() {
  local output="$1"
  shift
  local env_args=(
    "PODLAZ_APT_SIGNING_KEY_FILE=${PODLAZ_APT_SIGNING_KEY_FILE}"
    "PODLAZ_APT_SIGNING_FINGERPRINT=${PODLAZ_APT_SIGNING_FINGERPRINT}"
  )
  if [[ -n "${PODLAZ_APT_SIGNING_PASSPHRASE_FILE:-}" ]]; then
    env_args+=("PODLAZ_APT_SIGNING_PASSPHRASE_FILE=${PODLAZ_APT_SIGNING_PASSPHRASE_FILE}")
  fi
  env "${env_args[@]}" "$@" bash "${BUILDER}" "${output}" "${CANDIDATE_DEB}" "${PREVIOUS_DEB}"
}

expect_builder_failure() {
  local output="$1"
  shift
  rm -rf -- "${output}"
  set +e
  "$@" >"${PRIVATE_ROOT}/expected-failure.$(basename -- "${output}").log" 2>&1
  local code=$?
  set -e
  ((code != 0)) || fail "expected repository build failure unexpectedly succeeded"
  [[ ! -e "${output}" ]] || fail "failed repository build exposed an output directory"
}

validate_inputs() {
  (($# == 2)) || fail "usage: $0 CANDIDATE.deb PREVIOUS.deb"
  CANDIDATE_DEB="$(readlink -f -- "$1")"
  PREVIOUS_DEB="$(readlink -f -- "$2")"
  [[ -f "${CANDIDATE_DEB}" && -f "${PREVIOUS_DEB}" ]] || fail "candidate and previous packages must exist"
  [[ "$(dpkg-deb --field "${CANDIDATE_DEB}" Package)" == podlaz ]] || fail "candidate package is not podlaz"
  [[ "$(dpkg-deb --field "${PREVIOUS_DEB}" Package)" == podlaz ]] || fail "previous package is not podlaz"
  [[ "$(dpkg-deb --field "${CANDIDATE_DEB}" Architecture)" == amd64 ]] || fail "candidate package must be amd64"
  [[ "$(dpkg-deb --field "${PREVIOUS_DEB}" Architecture)" == amd64 ]] || fail "previous package must be amd64"

  CANDIDATE_VERSION="$(dpkg-deb --field "${CANDIDATE_DEB}" Version)"
  PREVIOUS_VERSION="$(dpkg-deb --field "${PREVIOUS_DEB}" Version)"
  [[ "${CANDIDATE_VERSION}" =~ ^[0-9A-Za-z.+~:-]+$ ]] || fail "candidate version is not safe for repository paths"
  [[ "${PREVIOUS_VERSION}" =~ ^[0-9A-Za-z.+~:-]+$ ]] || fail "previous version is not safe for repository paths"
  [[ "${PREVIOUS_VERSION%%-*}" == "${PREVIOUS_VERSION_EXPECTED}" ]] || fail "previous package version mismatch"
  dpkg --compare-versions "${CANDIDATE_VERSION}" gt "${PREVIOUS_VERSION}" || fail "candidate package must be newer than previous package"

  [[ "${CANDIDATE_COMMIT}" =~ ^[0-9a-f]{40}$ ]] || fail "candidate commit identity is unavailable"
  [[ "${PREVIOUS_COMMIT}" =~ ^[0-9a-f]{40}$ ]] || fail "previous commit identity is unavailable"
  [[ "${PREVIOUS_SHA256}" =~ ^[0-9a-f]{64}$ ]] || fail "previous package digest is unavailable"
  [[ "$(sha256sum -- "${PREVIOUS_DEB}" | awk '{print $1}')" == "${PREVIOUS_SHA256}" ]] || fail "previous package checksum mismatch"

  : "${PODLAZ_APT_SIGNING_KEY_FILE:?PODLAZ_APT_SIGNING_KEY_FILE is required}"
  : "${PODLAZ_APT_SIGNING_FINGERPRINT:?PODLAZ_APT_SIGNING_FINGERPRINT is required}"
  PODLAZ_APT_SIGNING_FINGERPRINT="$(normalize_fingerprint "${PODLAZ_APT_SIGNING_FINGERPRINT}")"
  [[ "${PODLAZ_APT_SIGNING_FINGERPRINT}" =~ ^[0-9A-F]{40,64}$ ]] || fail "configured signing fingerprint is invalid"
}

generate_rotation_key() {
  WRONG_GNUPGHOME="${PRIVATE_ROOT}/rotation-gnupg"
  WRONG_KEY="${PRIVATE_ROOT}/rotation-secret.asc"
  rm -rf -- "${WRONG_GNUPGHOME}"
  install -d -m 0700 "${WRONG_GNUPGHOME}"
  GNUPGHOME="${WRONG_GNUPGHOME}" gpg --batch --pinentry-mode loopback --passphrase '' \
    --quick-generate-key 'Podlaz APT qualification rotation <apt-rotation@example.invalid>' rsa2048 sign 0 >/dev/null 2>&1
  WRONG_FINGERPRINT="$(
    GNUPGHOME="${WRONG_GNUPGHOME}" gpg --batch --with-colons --list-secret-keys 2>/dev/null |
      awk -F: '$1 == "fpr" { print toupper($10); exit }'
  )"
  [[ "${WRONG_FINGERPRINT}" =~ ^[0-9A-F]{40,64}$ ]] || fail "could not create rotation test key"
  GNUPGHOME="${WRONG_GNUPGHOME}" gpg --batch --pinentry-mode loopback --passphrase '' \
    --armor --export-secret-keys "${WRONG_FINGERPRINT}" >"${WRONG_KEY}"
  chmod 0600 "${WRONG_KEY}"
}

exercise_repository_build_failures() {
  local missing_output="${PRIVATE_ROOT}/missing-key-site"
  local wrong_output="${PRIVATE_ROOT}/wrong-key-site"
  local metadata_output="${PRIVATE_ROOT}/metadata-failure-site"
  local signing_output="${PRIVATE_ROOT}/signing-failure-site"

  expect_builder_failure "${missing_output}" \
    env PODLAZ_APT_SIGNING_KEY_FILE="${PRIVATE_ROOT}/does-not-exist.asc" \
      PODLAZ_APT_SIGNING_FINGERPRINT="${PODLAZ_APT_SIGNING_FINGERPRINT}" \
      bash "${BUILDER}" "${missing_output}" "${CANDIDATE_DEB}" "${PREVIOUS_DEB}"

  expect_builder_failure "${wrong_output}" \
    env -u PODLAZ_APT_SIGNING_PASSPHRASE_FILE \
      PODLAZ_APT_SIGNING_KEY_FILE="${WRONG_KEY}" \
      PODLAZ_APT_SIGNING_FINGERPRINT="${PODLAZ_APT_SIGNING_FINGERPRINT}" \
      bash "${BUILDER}" "${wrong_output}" "${CANDIDATE_DEB}" "${PREVIOUS_DEB}"

  expect_builder_failure "${metadata_output}" \
    primary_builder "${metadata_output}" PODLAZ_APT_TEST_FAIL_STAGE=after-metadata

  expect_builder_failure "${signing_output}" \
    primary_builder "${signing_output}" PODLAZ_APT_TEST_FAIL_STAGE=after-signing
}

validate_repository_bytes() {
  local site="$1"
  local candidate_pool="${site}/apt/pool/main/p/podlaz/podlaz_${CANDIDATE_VERSION}_linux_amd64.deb"
  local previous_pool="${site}/apt/pool/main/p/podlaz/podlaz_${PREVIOUS_VERSION}_linux_amd64.deb"
  cmp -s -- "${CANDIDATE_DEB}" "${candidate_pool}" || fail "candidate package changed in repository"
  cmp -s -- "${PREVIOUS_DEB}" "${previous_pool}" || fail "previous package changed in repository"
  [[ "$(cat "${site}/apt/podlaz-archive-keyring.fingerprint")" == "${PODLAZ_APT_SIGNING_FINGERPRINT}" ]] || fail "published fingerprint mismatch"
  gpgv --keyring "${site}/apt/podlaz-archive-keyring.gpg" "${site}/apt/dists/stable/InRelease" >/dev/null 2>&1 ||
    fail "InRelease signature verification failed"
}

exercise_idempotence_and_rotation() {
  local rerun_site="${PRIVATE_ROOT}/rerun-site"
  local rotation_site="${PRIVATE_ROOT}/rotation-site"
  rm -rf -- "${rerun_site}" "${rotation_site}"

  primary_builder "${rerun_site}"
  validate_repository_bytes "${rerun_site}"
  cmp -s \
    "${PODLAZ_APT_OUTPUT_DIR}/apt/dists/stable/main/binary-amd64/Packages" \
    "${rerun_site}/apt/dists/stable/main/binary-amd64/Packages" ||
    fail "idempotent repository rebuild changed package index"
  for version in "${PREVIOUS_VERSION}" "${CANDIDATE_VERSION}"; do
    cmp -s \
      "${PODLAZ_APT_OUTPUT_DIR}/apt/pool/main/p/podlaz/podlaz_${version}_linux_amd64.deb" \
      "${rerun_site}/apt/pool/main/p/podlaz/podlaz_${version}_linux_amd64.deb" ||
      fail "idempotent repository rebuild changed package bytes"
  done

  env -u PODLAZ_APT_SIGNING_PASSPHRASE_FILE \
    PODLAZ_APT_SIGNING_KEY_FILE="${WRONG_KEY}" \
    PODLAZ_APT_SIGNING_FINGERPRINT="${WRONG_FINGERPRINT}" \
    bash "${BUILDER}" "${rotation_site}" "${CANDIDATE_DEB}" "${PREVIOUS_DEB}" >/dev/null
  set +e
  gpgv --keyring "${PODLAZ_APT_OUTPUT_DIR}/apt/podlaz-archive-keyring.gpg" \
    "${rotation_site}/apt/dists/stable/InRelease" >/dev/null 2>&1
  local code=$?
  set -e
  ((code != 0)) || fail "old signing key unexpectedly accepted rotated repository metadata"
}

prepare_guest() {
  local policy_rc="${GUEST_ROOT}/usr/sbin/policy-rc.d"
  sudo -n rm -rf -- "${GUEST_ROOT}"
  sudo -n debootstrap --variant=minbase noble "${GUEST_ROOT}" http://archive.ubuntu.com/ubuntu >"${PRIVATE_ROOT}/debootstrap.log" 2>&1
  printf '#!/bin/sh\nexit 101\n' | sudo -n tee "${policy_rc}" >/dev/null
  sudo -n chmod 0755 "${policy_rc}"
  sudo -n chroot "${GUEST_ROOT}" /usr/bin/env DEBIAN_FRONTEND=noninteractive apt-get update >"${PRIVATE_ROOT}/guest-apt.log" 2>&1
  sudo -n chroot "${GUEST_ROOT}" /usr/bin/env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    systemd systemd-sysv dbus ca-certificates sudo iproute2 nftables polkitd gnupg >>"${PRIVATE_ROOT}/guest-apt.log" 2>&1
  sudo -n rm -f -- "${policy_rc}"
  sudo -n chroot "${GUEST_ROOT}" apt-get clean >/dev/null 2>&1
  sudo -n rm -f -- "${GUEST_ROOT}/etc/apt/sources.list"
  sudo -n find "${GUEST_ROOT}/etc/apt/sources.list.d" -type f -delete
  sudo -n install -d -m 0755 "${GUEST_ROOT}/etc/apt/keyrings"
}

start_guest() {
  sudo -n systemd-nspawn \
    --quiet --boot \
    --directory="${GUEST_ROOT}" \
    --machine="${MACHINE}" \
    --settings=no \
    --private-network \
    --bind-ro="${REPO_ROOT}:/workspace" \
    --bind-ro="${PODLAZ_APT_OUTPUT_DIR}:/opt/podlaz-apt-site" \
    --link-journal=no >"${PRIVATE_ROOT}/nspawn.log" 2>&1 &
  NSPAWN_PID=$!

  for _ in $(seq 1 200); do
    if machinectl show "${MACHINE}" >/dev/null 2>&1 && guest_exec /bin/true >/dev/null 2>&1; then
      break
    fi
    sleep 0.2
  done
  guest_exec /bin/true >/dev/null
  # Expansion is intentionally evaluated by guest bash.
  # shellcheck disable=SC2016
  guest_exec /bin/bash -lc 'state="$(timeout 30 systemctl is-system-running --wait 2>/dev/null || true)"; [[ "$state" == running || "$state" == degraded ]]'
}

configure_guest_repository() {
  guest_exec install -m 0644 \
    /opt/podlaz-apt-site/apt/podlaz-archive-keyring.gpg \
    /etc/apt/keyrings/podlaz-archive-keyring.gpg
  guest_exec /bin/bash -lc "fingerprint=\$(gpg --batch --show-keys --with-colons /etc/apt/keyrings/podlaz-archive-keyring.gpg 2>/dev/null | awk -F: '\$1 == \"fpr\" { print toupper(\$10); exit }'); [[ \"\${fingerprint}\" == '${PODLAZ_APT_SIGNING_FINGERPRINT}' ]]"
  guest_exec /bin/bash -lc "cat >/etc/apt/sources.list.d/podlaz.sources <<'EOF_SOURCES'
Types: deb
URIs: file:/opt/podlaz-apt-site/apt
Suites: stable
Components: main
Architectures: amd64
Signed-By: /etc/apt/keyrings/podlaz-archive-keyring.gpg
EOF_SOURCES
apt-get update"
}

assert_guest_provenance() {
  local version="$1" commit="$2"
  local package="/opt/podlaz-apt-site/apt/pool/main/p/podlaz/podlaz_${version}_linux_amd64.deb"
  guest_exec /bin/bash -lc "fail() { printf '%s\\n' \"\$*\" >&2; return 1; }; source /workspace/scripts/e2e/lib/package_provenance.sh; assert_exact_podlaz_package_runtime_provenance '${package}' '${commit}'"
}

run_apt_install_upgrade() {
  guest_exec apt-cache policy podlaz | grep -F "${CANDIDATE_VERSION}" >/dev/null
  guest_exec apt-cache policy podlaz | grep -F "${PREVIOUS_VERSION}" >/dev/null

  guest_exec /usr/bin/env DEBIAN_FRONTEND=noninteractive apt-get install -y "podlaz=${PREVIOUS_VERSION}"
  guest_exec podlaz version | grep -Fx "podlaz version ${PREVIOUS_VERSION}" >/dev/null
  guest_exec podlaz version | grep -Fx "commit: ${PREVIOUS_COMMIT}" >/dev/null
  guest_exec systemctl is-active --quiet podlazd.service
  assert_guest_provenance "${PREVIOUS_VERSION}" "${PREVIOUS_COMMIT}"

  guest_exec /usr/bin/env DEBIAN_FRONTEND=noninteractive apt-get upgrade -y
  [[ "$(guest_exec dpkg-query -W "-f=\${Version}" podlaz)" == "${CANDIDATE_VERSION}" ]] || fail "APT upgrade did not install candidate version"
  guest_exec podlaz version | grep -Fx "podlaz version ${CANDIDATE_VERSION}" >/dev/null
  guest_exec podlaz version | grep -Fx "commit: ${CANDIDATE_COMMIT}" >/dev/null
  guest_exec systemctl is-active --quiet podlazd.service
  assert_guest_provenance "${CANDIDATE_VERSION}" "${CANDIDATE_COMMIT}"
}

main() {
  for command in awk bash cmp debootstrap dpkg dpkg-deb find gpg gpgv grep install machinectl readlink seq sha256sum sudo systemd-nspawn systemd-run timeout; do
    require_command "${command}"
  done

  install -d -m 0700 "${E2E_TMP_ROOT}" "${PRIVATE_ROOT}" "${E2E_ARTIFACT_DIR}"
  rm -f -- "${REPORT}"
  validate_inputs "$@"
  generate_rotation_key
  exercise_repository_build_failures

  rm -rf -- "${PODLAZ_APT_OUTPUT_DIR}"
  primary_builder "${PODLAZ_APT_OUTPUT_DIR}"
  validate_repository_bytes "${PODLAZ_APT_OUTPUT_DIR}"
  exercise_idempotence_and_rotation

  prepare_guest
  start_guest
  configure_guest_repository
  run_apt_install_upgrade

  record_report
  validate_report
}

if [[ "${1:-}" == validate-report ]]; then
  validate_report
  exit 0
fi

main "$@"
