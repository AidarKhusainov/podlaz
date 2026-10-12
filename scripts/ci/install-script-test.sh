#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
installer="${repo_root}/scripts/install.sh"

fail() {
  printf 'install-script-test: %s\n' "$*" >&2
  exit 1
}

make_fixture() {
  local root="$1"
  mkdir -p "${root}/bin" "${root}/fixtures" "${root}/writes"
  printf 'mock public key\n' >"${root}/fixtures/podlaz-archive-keyring.gpg"

  cat >"${root}/bin/dpkg" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == '--print-architecture' ]]
printf '%s\n' "${TEST_ARCH:?}"
MOCK

  cat >"${root}/bin/curl" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
[[ " $* " == *" --proto =https "* ]]
[[ " $* " == *" --tlsv1.2 "* ]]
[[ " $* " == *" --location "* ]]
out=""
url=""
while (($#)); do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
[[ "$url" == 'https://aidarkhusainov.github.io/podlaz/apt/podlaz-archive-keyring.gpg' ]]
cp "${TEST_FIXTURES:?}/podlaz-archive-keyring.gpg" "$out"
MOCK

  cat >"${root}/bin/gpg" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
[[ "$*" == *'--show-keys --with-colons'* ]]
case "${TEST_GPG_MODE:-valid}" in
  valid) printf 'pub:::::::::\nfpr:::::::::7C6F4EF9CDE73501E79CCC63E2F6F280766864C2:\n' ;;
  mismatch) printf 'pub:::::::::\nfpr:::::::::AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA:\n' ;;
  extra) printf 'pub:::::::::\nfpr:::::::::7C6F4EF9CDE73501E79CCC63E2F6F280766864C2:\npub:::::::::\nfpr:::::::::BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB:\n' ;;
  *) exit 99 ;;
esac
MOCK

  cat >"${root}/bin/apt-get" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${TEST_APT_LOG:?}"
if [[ "$*" == *' update' && "${TEST_APT_FAIL:-}" == update ]]; then
  exit 100
fi
MOCK

  cat >"${root}/bin/apt-cache" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
[[ "$*" == 'policy podlaz' ]]
case "${TEST_POLICY_MODE:-valid}" in
  valid)
    cat <<EOF_POLICY
podlaz:
  Installed: (none)
  Candidate: 1.2.3
  Version table:
     1.2.3 500
        500 https://aidarkhusainov.github.io/podlaz/apt stable/main ${TEST_ARCH} Packages
EOF_POLICY
    ;;
  absent)
    printf 'podlaz:\n  Installed: (none)\n  Candidate: (none)\n  Version table:\n'
    ;;
  foreign)
    printf 'podlaz:\n  Installed: (none)\n  Candidate: 1.2.3\n  Version table:\n     1.2.3 500\n        500 https://packages.example.org/repo stable/main amd64 Packages\n'
    ;;
  foreign-newer)
    cat <<EOF_POLICY
podlaz:
  Installed: (none)
  Candidate: 9.9.9
  Version table:
     9.9.9 600
        600 https://packages.example.org/repo stable/main amd64 Packages
     1.2.3 500
        500 https://aidarkhusainov.github.io/podlaz/apt stable/main ${TEST_ARCH} Packages
EOF_POLICY
    ;;
  *) exit 99 ;;
esac
MOCK

  cat >"${root}/bin/install" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${TEST_INSTALL_LOG:?}"
if [[ "$1" == '-d' ]]; then
  exit 0
fi
[[ "$1" == '-m' && "$2" == '0644' && "$#" == 4 ]]
dest="${TEST_WRITES:?}/${4#/}"
mkdir -p "$(dirname "$dest")"
cp "$3" "$dest"
MOCK

  cat >"${root}/bin/sudo" <<'MOCK'
#!/usr/bin/env bash
set -euo pipefail
exec "$@"
MOCK

  chmod +x "${root}/bin/"*
}

run_installer() {
  local root="$1" arch="$2"
  TEST_ARCH="$arch" \
    TEST_FIXTURES="${root}/fixtures" \
    TEST_APT_LOG="${root}/apt.log" \
    TEST_INSTALL_LOG="${root}/install.log" \
    TEST_WRITES="${root}/writes" \
    PATH="${root}/bin:${PATH}" \
    bash "${installer}" >"${root}/stdout" 2>"${root}/stderr"
}

run_success_case() (
  local arch="$1" root
  root="$(mktemp -d)"
  trap 'rm -rf -- "$root"' EXIT
  make_fixture "$root"

  run_installer "$root" "$arch" || fail "installer rejected valid ${arch} repository"
  grep -Fxq 'install -y -- podlaz=1.2.3' "$root/apt.log" || fail 'package must install through signed APT'
  grep -Fq 'APT::Update::Error-Mode=any' "$root/apt.log" || fail 'APT update must fail on repository errors'
  grep -Fq 'Dir::Etc::sourceparts=-' "$root/apt.log" || fail 'APT update must be scoped to Podlaz'
  grep -Fq 'Dir::Etc::sourcelist=/etc/apt/sources.list.d/podlaz.sources' "$root/apt.log" \
    || fail 'APT update must use the Podlaz source'
  cmp "$root/fixtures/podlaz-archive-keyring.gpg" "$root/writes/etc/apt/keyrings/podlaz-archive-keyring.gpg" \
    || fail 'installer did not write verified keyring'
  grep -Fxq "Architectures: ${arch}" "$root/writes/etc/apt/sources.list.d/podlaz.sources" \
    || fail 'missing architecture restriction'
  grep -Fxq 'Signed-By: /etc/apt/keyrings/podlaz-archive-keyring.gpg' \
    "$root/writes/etc/apt/sources.list.d/podlaz.sources" || fail 'missing scoped signing key'
  ! grep -Eq 'apt-key|Trusted: yes' "$root/writes/etc/apt/sources.list.d/podlaz.sources" \
    || fail 'source weakens APT trust'
  grep -Fq 'Future updates use normal APT upgrades.' "$root/stdout" || fail 'missing success guidance'
)

run_failure_case() (
  local label="$1" arch="$2" root
  root="$(mktemp -d)"
  trap 'rm -rf -- "$root"' EXIT
  make_fixture "$root"
  case "$label" in
    fingerprint) export TEST_GPG_MODE=mismatch ;;
    extra-key) export TEST_GPG_MODE=extra ;;
    apt-update) export TEST_APT_FAIL=update ;;
    no-candidate) export TEST_POLICY_MODE=absent ;;
    foreign-source) export TEST_POLICY_MODE=foreign ;;
    foreign-newer) export TEST_POLICY_MODE=foreign-newer ;;
    unsupported-arch) : ;;
    *) fail "unknown failure test: $label" ;;
  esac

  if run_installer "$root" "$arch"; then
    fail "installer accepted invalid ${label}"
  fi
  ! grep -Fq 'install -y -- podlaz=' "$root/apt.log" 2>/dev/null \
    || fail "installer installed Podlaz despite ${label}"
  if [[ "$label" == fingerprint || "$label" == extra-key || "$label" == unsupported-arch ]]; then
    [[ ! -s "$root/install.log" ]] || fail "installer wrote APT configuration despite ${label}"
  fi
  if [[ "$label" == apt-update ]]; then
    grep -Fq 'APT::Update::Error-Mode=any' "$root/apt.log" || fail 'APT failure not exercised'
  fi
)

run_success_case amd64
run_success_case arm64
run_failure_case fingerprint amd64
run_failure_case extra-key amd64
run_failure_case apt-update amd64
run_failure_case no-candidate amd64
run_failure_case foreign-source amd64
run_failure_case foreign-newer amd64
run_failure_case unsupported-arch armhf

printf 'install-script-test: ok\n'
