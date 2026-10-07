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
  local arch="$2"
  local checksum_mode="${3:-valid}"
  local asset="podlaz_1.2.3_linux_${arch}.deb"

  mkdir -p "${root}/fixtures" "${root}/bin"
  printf 'package-%s\n' "${arch}" >"${root}/fixtures/${asset}"

  if [[ "${checksum_mode}" == valid ]]; then
    (
      cd "${root}/fixtures"
      sha256sum "${asset}" >SHA256SUMS
    )
  else
    printf '%064d  %s\n' 0 "${asset}" >"${root}/fixtures/SHA256SUMS"
  fi

  cat >"${root}/bin/dpkg" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == "--print-architecture" ]]
printf '%s\n' "${TEST_ARCH:?}"
EOF

  cat >"${root}/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

out=""
url=""
while (($# > 0)); do
  case "$1" in
    -o)
      out="$2"
      shift 2
      ;;
    -w)
      shift 2
      ;;
    -*)
      shift
      ;;
    *)
      url="$1"
      shift
      ;;
  esac
done

case "${url}" in
  https://github.com/AidarKhusainov/podlaz/releases/latest)
    printf 'https://github.com/AidarKhusainov/podlaz/releases/tag/v1.2.3'
    ;;
  */SHA256SUMS)
    cp "${TEST_FIXTURES:?}/SHA256SUMS" "${out}"
    ;;
  */podlaz_1.2.3_linux_*.deb)
    cp "${TEST_FIXTURES:?}/${url##*/}" "${out}"
    ;;
  *)
    printf 'unexpected curl URL: %s\n' "${url}" >&2
    exit 91
    ;;
esac
EOF

  cat >"${root}/bin/apt-get" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${TEST_APT_LOG:?}"
EOF

  cat >"${root}/bin/sudo" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
exec "$@"
EOF

  chmod +x "${root}/bin/dpkg" "${root}/bin/curl" "${root}/bin/apt-get" "${root}/bin/sudo"
}

run_success_case() (
  local arch="$1"
  local root
  root="$(mktemp -d)"
  trap 'rm -rf -- "${root}"' EXIT
  make_fixture "${root}" "${arch}"

  : >"${root}/apt.log"
  PATH="${root}/bin:${PATH}" \
    TEST_ARCH="${arch}" \
    TEST_FIXTURES="${root}/fixtures" \
    TEST_APT_LOG="${root}/apt.log" \
    bash "${installer}" >"${root}/stdout" 2>"${root}/stderr"

  grep -Fqx "install -y -- ./podlaz_1.2.3_linux_${arch}.deb" "${root}/apt.log" \
    || fail "installer did not install expected ${arch} package"
  grep -Fq "Checksum verified." "${root}/stdout" \
    || fail "installer did not report checksum verification"
)

run_checksum_failure_case() (
  local root
  root="$(mktemp -d)"
  trap 'rm -rf -- "${root}"' EXIT
  make_fixture "${root}" amd64 invalid

  : >"${root}/apt.log"
  if PATH="${root}/bin:${PATH}" \
    TEST_ARCH=amd64 \
    TEST_FIXTURES="${root}/fixtures" \
    TEST_APT_LOG="${root}/apt.log" \
    bash "${installer}" >"${root}/stdout" 2>"${root}/stderr"; then
    fail "installer accepted a package with a mismatched checksum"
  fi

  [[ ! -s "${root}/apt.log" ]] || fail "installer attempted installation after checksum failure"
  grep -Fq "checksum mismatch" "${root}/stderr" || fail "checksum failure was not classified clearly"
)

run_unsupported_arch_case() (
  local root
  root="$(mktemp -d)"
  trap 'rm -rf -- "${root}"' EXIT
  mkdir -p "${root}/bin"

  cat >"${root}/bin/dpkg" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'armhf\n'
EOF
  chmod +x "${root}/bin/dpkg"

  if PATH="${root}/bin:${PATH}" bash "${installer}" >"${root}/stdout" 2>"${root}/stderr"; then
    fail "installer accepted unsupported architecture"
  fi
  grep -Fq "unsupported Debian architecture: armhf" "${root}/stderr" \
    || fail "unsupported architecture failure was not classified clearly"
)

run_success_case amd64
run_success_case arm64
run_checksum_failure_case
run_unsupported_arch_case

printf 'install-script-test: ok\n'
