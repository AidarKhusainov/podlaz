#!/usr/bin/env bash

# Shared helpers for self-hosted podlaz e2e scripts.
# Scripts sourcing this file are expected to run with `set -Eeuo pipefail`.

E2E_ARTIFACT_DIR="${E2E_ARTIFACT_DIR:-${RUNNER_TEMP:-/tmp}/podlaz-e2e-artifacts}"
E2E_TMP_ROOT="${E2E_TMP_ROOT:-${RUNNER_TEMP:-/tmp}/podlaz-e2e-tmp}"
mkdir -p "${E2E_ARTIFACT_DIR}" "${E2E_TMP_ROOT}"

E2E_STEP=0
LAST_STDOUT=""
LAST_STDERR=""

log() {
  printf '\n>>> %s\n' "$*"
}

fail() {
  printf 'ERROR: %s\n' "$*" >&2
  if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
    printf '::error::%s\n' "$*" >&2
  fi
  exit 1
}

require_cmd() {
  local cmd
  for cmd in "$@"; do
    command -v "${cmd}" >/dev/null 2>&1 || fail "required command not found: ${cmd}"
  done
}

safe_name() {
  printf '%s' "$1" | tr -c 'A-Za-z0-9._-' '_'
}

run_capture() {
  local name="$1"
  shift
  local safe
  safe="$(safe_name "${name}")"
  E2E_STEP=$((E2E_STEP + 1))
  LAST_STDOUT="${E2E_ARTIFACT_DIR}/$(printf '%03d' "${E2E_STEP}")-${safe}.stdout"
  LAST_STDERR="${E2E_ARTIFACT_DIR}/$(printf '%03d' "${E2E_STEP}")-${safe}.stderr"

  log "${name}: $*"
  set +e
  "$@" >"${LAST_STDOUT}" 2>"${LAST_STDERR}"
  local code=$?
  set -e

  if [[ -s "${LAST_STDOUT}" ]]; then
    sed -e 's/^/stdout: /' "${LAST_STDOUT}"
  fi
  if [[ -s "${LAST_STDERR}" ]]; then
    sed -e 's/^/stderr: /' "${LAST_STDERR}" >&2
  fi
  return "${code}"
}

expect_success() {
  local name="$1"
  shift
  if ! run_capture "${name}" "$@"; then
    local code=$?
    fail "${name} failed with exit code ${code}"
  fi
}

expect_exit() {
  local want="$1"
  local name="$2"
  shift 2
  set +e
  run_capture "${name}" "$@"
  local got=$?
  set -e
  if [[ "${got}" != "${want}" ]]; then
    fail "${name}: expected exit ${want}, got ${got}"
  fi
}

expect_exit_in() {
  local allowed="$1"
  local name="$2"
  shift 2
  set +e
  run_capture "${name}" "$@"
  local got=$?
  set -e
  local code
  for code in ${allowed}; do
    if [[ "${got}" == "${code}" ]]; then
      return 0
    fi
  done
  fail "${name}: expected one of [${allowed}], got ${got}"
}

assert_contains() {
  local file="$1"
  local needle="$2"
  grep -F -- "${needle}" "${file}" >/dev/null || fail "expected ${file} to contain: ${needle}"
}

assert_not_contains() {
  local file="$1"
  local needle="$2"
  if grep -F -- "${needle}" "${file}" >/dev/null; then
    fail "expected ${file} not to contain: ${needle}"
  fi
}

assert_nonempty() {
  local value="$1"
  local description="$2"
  [[ -n "${value}" ]] || fail "empty value: ${description}"
}

assert_json_file() {
  local file="$1"
  require_cmd python3
  python3 -m json.tool "${file}" >/dev/null || fail "invalid JSON: ${file}"
}

build_podlaz_binary() {
  require_cmd go
  local out_dir="${E2E_ARTIFACT_DIR}/bin"
  mkdir -p "${out_dir}"
  PODLAZ_BIN="${out_dir}/podlaz"
  log "build podlaz test binary"
  go build -o "${PODLAZ_BIN}" ./cmd/podlaz
  export PODLAZ_BIN
}

setup_isolated_xdg() {
  local suite="$1"
  E2E_HOME="$(mktemp -d "${E2E_TMP_ROOT}/${suite}.XXXXXX")"
  export E2E_HOME
  export XDG_CONFIG_HOME="${E2E_HOME}/config"
  export XDG_STATE_HOME="${E2E_HOME}/state"
  export XDG_CACHE_HOME="${E2E_HOME}/cache"
  mkdir -p "${XDG_CONFIG_HOME}" "${XDG_STATE_HOME}" "${XDG_CACHE_HOME}"
  log "isolated XDG state: ${E2E_HOME}"
}

mask_value() {
  local value="${1:-}"
  if [[ -n "${value}" && "${GITHUB_ACTIONS:-}" == "true" ]]; then
    printf '::add-mask::%s\n' "${value}"
  fi
}

write_vless_fixtures() {
  local dir="$1"
  mkdir -p "${dir}"
  cat >"${dir}/xray-vless.json" <<'JSON'
{
  "outbounds": [
    {
      "tag": "json-cli",
      "protocol": "vless",
      "settings": {
        "vnext": [
          {
            "address": "example.com",
            "port": 443,
            "users": [
              {"id": "00000000-0000-0000-0000-000000000001", "encryption": "none", "flow": "xtls-rprx-vision"}
            ]
          }
        ]
      },
      "streamSettings": {
        "network": "tcp",
        "security": "reality",
        "realitySettings": {
          "serverName": "example.com",
          "fingerprint": "chrome",
          "publicKey": "public-key",
          "shortId": "abcd",
          "spiderX": "/"
        }
      }
    }
  ]
}
JSON
}

vless_uri() {
  local name="$1"
  printf 'vless://00000000-0000-0000-0000-000000000001@example.com:443?type=tcp&security=tls&encryption=none#%s' "${name}"
}

vless_reality_uri() {
  local name="$1"
  printf 'vless://00000000-0000-0000-0000-000000000001@example.com:443?type=tcp&security=reality&encryption=none&flow=xtls-rprx-vision&sni=www.example.com&fp=chrome&pbk=public-key&sid=abcd&spx=%%2F#%s' "${name}"
}
