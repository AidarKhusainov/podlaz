#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd awk git grep mktemp python3 resolvectl runuser sleep sudo systemctl timeout

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"

if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required"
fi

EVIDENCE_FILE="${E2E_ARTIFACT_DIR}/issue243-acceptance.txt"
PROFILE_ID=""
CONNECTED=false

write_evidence() {
  local key="$1" value="$2"
  case "${key}${value}" in
    *$'\n'*|*$'\r'*) fail "invalid normalized issue 243 evidence" ;;
  esac
  printf '%s=%s\n' "${key}" "${value}" >>"${EVIDENCE_FILE}"
}

run_installed_podlaz() {
  sudo -n runuser -u "$(id -un)" -g podlaz -- env \
    XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
    XDG_STATE_HOME="${XDG_STATE_HOME}" \
    XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
    /usr/bin/podlaz "$@"
}

cleanup() {
  local saved=$?
  set +e
  if [[ "${CONNECTED}" == "true" ]]; then
    run_installed_podlaz disconnect >/dev/null 2>&1
  fi
  set -e
  return "${saved}"
}
trap cleanup EXIT

first_profile_uri() {
  if [[ -n "${PODLAZ_E2E_PROFILE_URI}" ]]; then
    printf '%s\n' "${PODLAZ_E2E_PROFILE_URI}"
    return
  fi
  while IFS= read -r uri; do
    [[ -n "${uri}" ]] || continue
    printf '%s\n' "${uri}"
    return
  done <<<"${PODLAZ_E2E_PROFILE_URI_LIST}"
}

verify_package_provenance() {
  local build_commit version_output
  build_commit="${GITHUB_SHA:-$(git rev-parse HEAD)}"
  version_output="$(mktemp "${E2E_TMP_ROOT}/issue243-version.XXXXXX")"
  /usr/bin/podlaz version >"${version_output}" 2>/dev/null || fail "issue 243 installed CLI version failed"
  grep -F -- "${build_commit}" "${version_output}" >/dev/null || fail "issue 243 installed CLI does not identify the tested commit"
  rm -f -- "${version_output}"
  systemctl is-active --quiet podlazd.service || fail "issue 243 requires the packaged podlazd service"
  write_evidence package_provenance pass
}

import_profile_privately() {
  local uri="$1" output error_output
  output="$(mktemp "${E2E_TMP_ROOT}/issue243-profile-import.stdout.XXXXXX")"
  error_output="$(mktemp "${E2E_TMP_ROOT}/issue243-profile-import.stderr.XXXXXX")"
  if ! run_installed_podlaz profile import "${uri}" >"${output}" 2>"${error_output}"; then
    rm -f -- "${output}" "${error_output}"
    fail "issue 243 profile import failed"
  fi
  PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${output}")"
  rm -f -- "${output}" "${error_output}"
  assert_nonempty "${PROFILE_ID}" "issue 243 imported profile id"
  mask_value "${PROFILE_ID}"
  write_evidence profile_import pass
}

assert_active_status() {
  local phase="$1" read_name output
  for read_name in first second; do
    output="$(mktemp "${E2E_TMP_ROOT}/issue243-${phase}-${read_name}-active-status.XXXXXX")"
    if ! run_installed_podlaz status >"${output}" 2>&1; then
      rm -f -- "${output}"
      fail "${phase}/${read_name}: active status returned non-zero"
    fi
    grep -Fx "Connection: active" "${output}" >/dev/null || fail "${phase}/${read_name}: connection is not active"
    grep -Fx "Transaction: committed" "${output}" >/dev/null || fail "${phase}/${read_name}: TUN transaction is not committed"
    grep -Fx "Stale state: none" "${output}" >/dev/null || fail "${phase}/${read_name}: active status reports stale state"
    grep -Fx "Startup recovery scan: clean for active connection" "${output}" >/dev/null || fail "${phase}/${read_name}: active startup scan is not clean"
    if grep -F "Inspection warnings:" "${output}" >/dev/null; then
      fail "${phase}/${read_name}: active status contains inspection warnings"
    fi
    rm -f -- "${output}"
  done
  write_evidence "active_status_${phase}" pass
  write_evidence "active_status_stability_${phase}" pass
}

wait_for_exact_exit_zero_missing_status() {
  local phase="$1" stdout_file stderr_file exit_code classification attempt
  stdout_file="$(mktemp "${E2E_TMP_ROOT}/issue243-${phase}-resolved.stdout.XXXXXX")"
  stderr_file="$(mktemp "${E2E_TMP_ROOT}/issue243-${phase}-resolved.stderr.XXXXXX")"

  for attempt in {1..100}; do
    set +e
    timeout --signal=TERM --kill-after=1s 3s \
      resolvectl status podlaz0 --no-pager >"${stdout_file}" 2>"${stderr_file}"
    exit_code=$?
    set -e

    classification="$(python3 - "${exit_code}" "${stdout_file}" "${stderr_file}" <<'PY'
import re
import sys

exit_code = int(sys.argv[1])
stdout_path = sys.argv[2]
stderr_path = sys.argv[3]
expected_missing = b'Failed to resolve interface "podlaz0", ignoring: No such device'

with open(stdout_path, "rb") as handle:
    stdout = handle.read()
with open(stderr_path, "rb") as handle:
    stderr = handle.read()

if exit_code == 0 and stdout == b"" and stderr in (
    expected_missing + b"\n",
    expected_missing + b"\r\n",
):
    print("exact")
    raise SystemExit(0)

if exit_code != 0 or stderr != b"":
    print("unexpected")
    raise SystemExit(0)

try:
    text = stdout.decode("utf-8")
except UnicodeDecodeError:
    print("unexpected")
    raise SystemExit(0)


def unique_tokens(value):
    out = []
    seen = set()
    for token in value.split():
        if token and token not in seen:
            seen.add(token)
            out.append(token)
    return out


def reject():
    print("unexpected")
    raise SystemExit(0)

seen_header = False
seen_fields = set()
last_field = ""
current_scopes = []
protocols = []
current_dns_server = ""
dns_servers = []
dns_domains = []

for raw_line in text.split("\n"):
    line = raw_line.strip()
    if not line:
        continue
    if line.startswith("Link "):
        if seen_header or re.fullmatch(r"Link [0-9]+ \(podlaz0\)", line) is None:
            reject()
        seen_header = True
        last_field = ""
        continue
    if not seen_header:
        reject()
    if ":" not in line:
        if last_field == "DNS Servers":
            for token in unique_tokens(line):
                if token not in dns_servers:
                    dns_servers.append(token)
            continue
        if last_field == "DNS Domain":
            for token in unique_tokens(line):
                if token not in dns_domains:
                    dns_domains.append(token)
            continue
        reject()

    key, value = (part.strip() for part in line.split(":", 1))
    if not key or key in seen_fields:
        reject()
    if key == "Current Scopes":
        if not value:
            reject()
        current_scopes = unique_tokens(value)
    elif key == "Protocols":
        if not value:
            reject()
        protocols = unique_tokens(value)
    elif key == "Current DNS Server":
        fields = value.split()
        if len(fields) != 1:
            reject()
        current_dns_server = fields[0]
    elif key == "DNS Servers":
        if not value:
            reject()
        dns_servers = unique_tokens(value)
    elif key == "DNS Domain":
        if not value:
            reject()
        dns_domains = unique_tokens(value)
    else:
        reject()
    seen_fields.add(key)
    last_field = key

if not seen_header or "Current Scopes" not in seen_fields or "Protocols" not in seen_fields:
    reject()

is_proven_empty = (
    current_scopes == ["none"]
    and current_dns_server == ""
    and not dns_servers
    and not dns_domains
    and "-DefaultRoute" in protocols
    and "+DefaultRoute" not in protocols
)
print("transient" if is_proven_empty else "unexpected")
PY
)"

    case "${classification}" in
      exact)
        rm -f -- "${stdout_file}" "${stderr_file}"
        write_evidence "resolved_exit0_missing_${phase}" pass
        return 0
        ;;
      transient)
        : # Supported semantic absence while resolved removes its transient Link record.
        ;;
      *)
        rm -f -- "${stdout_file}" "${stderr_file}"
        fail "${phase}: resolver state became unexpected while waiting for the exact exit-0 missing-device envelope"
        ;;
    esac

    sleep 0.1
  done

  rm -f -- "${stdout_file}" "${stderr_file}"
  fail "${phase}: resolver did not converge from the proven-empty transient record to the exact exit-0 missing-device envelope within the bounded wait"
}

assert_inactive_status() {
  local phase="$1" output
  output="$(mktemp "${E2E_TMP_ROOT}/issue243-${phase}-inactive-status.XXXXXX")"
  if ! run_installed_podlaz status >"${output}" 2>&1; then
    rm -f -- "${output}"
    fail "${phase}: inactive status returned non-zero"
  fi
  grep -Fx "Connection: inactive" "${output}" >/dev/null || fail "${phase}: status is not inactive"
  grep -Fx "Stale state: none" "${output}" >/dev/null || fail "${phase}: stale state is not clean"
  grep -Fx "Startup recovery scan: clean inactive state" "${output}" >/dev/null || fail "${phase}: startup recovery scan is not clean inactive"
  if grep -F "Recovery candidates:" "${output}" >/dev/null || grep -F "Inspection warnings:" "${output}" >/dev/null; then
    fail "${phase}: inactive status still publishes recovery or inspection evidence"
  fi
  rm -f -- "${output}"
  write_evidence "inactive_status_${phase}" pass
}

assert_recover_json_clean() {
  local phase="$1" output
  output="$(mktemp "${E2E_TMP_ROOT}/issue243-${phase}-recover.XXXXXX")"
  if ! run_installed_podlaz recover --json >"${output}" 2>/dev/null; then
    rm -f -- "${output}"
    fail "${phase}: recover --json returned non-zero"
  fi
  if ! python3 - "${output}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
if payload.get("status") != "ok":
    raise SystemExit("recover JSON status is not ok")
if payload.get("warnings"):
    raise SystemExit("top-level recover JSON warnings remain")
recovery = payload.get("recovery")
if not isinstance(recovery, dict):
    raise SystemExit("recovery payload is missing")
if recovery.get("candidates"):
    raise SystemExit("recovery candidates remain")
if recovery.get("warnings"):
    raise SystemExit("recovery inspection warnings remain")
PY
  then
    rm -f -- "${output}"
    fail "${phase}: recover --json is not clean"
  fi
  rm -f -- "${output}"
  write_evidence "recover_json_${phase}" pass
}

assert_recover_execute_clean() {
  local phase="$1" output
  output="$(mktemp "${E2E_TMP_ROOT}/issue243-${phase}-recover-execute.XXXXXX")"
  if ! run_installed_podlaz recover --execute --yes --json >"${output}" 2>/dev/null; then
    rm -f -- "${output}"
    fail "${phase}: recover --execute --yes --json returned non-zero"
  fi
  if ! python3 - "${output}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
if payload.get("status") != "ok":
    raise SystemExit("recover execute JSON status is not ok")
if payload.get("warnings"):
    raise SystemExit("recover execute JSON warnings remain")
if payload.get("errors"):
    raise SystemExit("recover execute JSON errors remain")
if payload.get("recovery"):
    raise SystemExit("recover execute JSON unexpectedly contains cleanup results")
PY
  then
    rm -f -- "${output}"
    fail "${phase}: recover execute result is not clean"
  fi
  rm -f -- "${output}"
  write_evidence "recover_execute_${phase}" pass
}

run_cycle() {
  local phase="$1"
  if ! run_installed_podlaz connect --mode tun "${PROFILE_ID}" >/dev/null 2>&1; then
    fail "${phase}: TUN connect failed"
  fi
  CONNECTED=true
  assert_active_status "${phase}"

  if ! run_installed_podlaz disconnect >/dev/null 2>&1; then
    fail "${phase}: disconnect failed"
  fi
  CONNECTED=false

  # The product absence contract is semantic here: immediately after disconnect
  # systemd-resolved may report either the exact missing-link envelope or the
  # already-supported proven-empty transient Link record. User-visible clean
  # status/recovery publication is therefore the authoritative convergence gate.
  assert_inactive_status "${phase}"
  assert_recover_json_clean "${phase}"
  write_evidence "cycle_${phase}" pass
}

: >"${EVIDENCE_FILE}"
setup_isolated_xdg issue243-package-acceptance
verify_package_provenance

PROFILE_URI="$(first_profile_uri)"
assert_nonempty "${PROFILE_URI}" "issue 243 profile URI"
mask_value "${PROFILE_URI}"
import_profile_privately "${PROFILE_URI}"
unset PROFILE_URI

# The preceding package acceptance may leave a short-lived proven-empty resolved
# Link record. First prove the product-level inactive state is already clean,
# then boundedly wait only through that supported transient representation until
# the real Ubuntu 24.04/systemd 255 byte-exact exit-0 envelope is observable.
assert_inactive_status initial
assert_recover_json_clean initial
wait_for_exact_exit_zero_missing_status initial
assert_recover_execute_clean initial
assert_inactive_status after-recover-execute
assert_recover_json_clean after-recover-execute
write_evidence initial_inactive pass

run_cycle first
run_cycle immediate-reconnect

write_evidence immediate_reconnect pass
write_evidence acceptance pass
