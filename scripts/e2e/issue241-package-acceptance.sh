#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/tun_package_assertions.sh
source "${SCRIPT_DIR}/lib/tun_package_assertions.sh"

require_cmd awk bash curl dpkg dpkg-deb dpkg-query getent git grep head hostname ip journalctl mktemp python3 readlink resolvectl runuser sed seq sha256sum sleep sort sudo systemctl tail

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"
: "${PODLAZ_E2E_DNS_CHECK_HOST:=github.com}"
: "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:=https://api.ipify.org}"
: "${PODLAZ_DEB_ARCH:=$(dpkg --print-architecture)}"

if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required"
fi
if [[ "${PODLAZ_DEB_ARCH}" != "$(dpkg --print-architecture)" ]]; then
  fail "issue 241 package acceptance requires a native .deb"
fi

DEV_DEB="dist/podlaz_0.0.0~dev-1_linux_${PODLAZ_DEB_ARCH}.deb"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
FALLBACK_NETWORK_HELPER="${SCRIPT_DIR}/tun-package-fallback-network.py"
TRANSACTION_DIR="/run/podlaz/transactions"
EVIDENCE_FILE="${E2E_ARTIFACT_DIR}/issue241-acceptance.txt"
SENSITIVE_VALUES=""
PROFILE_ID=""
JOURNAL_CURSOR=""

write_evidence() {
  local key="$1" value="$2"
  case "${key}${value}" in
    *$'\n'*|*$'\r'*) fail "invalid normalized issue 241 evidence" ;;
  esac
  printf '%s=%s\n' "${key}" "${value}" >>"${EVIDENCE_FILE}"
}

mask_multiline_sensitive() {
  local value="${1:-}"
  [[ -n "${value}" ]] || return 0
  mask_value "${value}"
  while IFS= read -r line; do
    [[ -n "${line}" ]] && mask_value "${line}"
  done <<<"${value}"
}

append_sensitive_value() {
  local value="${1:-}"
  [[ -n "${value}" ]] || return 0
  SENSITIVE_VALUES+="${value}"$'\n'
  mask_multiline_sensitive "${value}"
}

for sensitive in "${PODLAZ_E2E_PROFILE_URI}" "${PODLAZ_E2E_PROFILE_URI_LIST}"; do
  append_sensitive_value "${sensitive}"
done

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

wait_for_daemon_socket() {
  local attempt
  for attempt in $(seq 1 150); do
    [[ -S "${DAEMON_SOCKET}" ]] && return 0
    sleep 0.1
  done
  fail "podlazd.service did not create its socket"
}

run_installed_podlaz() {
  sudo -n runuser -u "$(id -un)" -g podlaz -- env \
    XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
    XDG_STATE_HOME="${XDG_STATE_HOME}" \
    XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
    /usr/bin/podlaz "$@"
}

capture_secret_import() {
  local uri="$1" out err
  out="$(mktemp "${E2E_TMP_ROOT}/issue241-profile-import.stdout.XXXXXX")"
  err="$(mktemp "${E2E_TMP_ROOT}/issue241-profile-import.stderr.XXXXXX")"
  if ! run_installed_podlaz profile import "${uri}" >"${out}" 2>"${err}"; then
    rm -f -- "${out}" "${err}"
    fail "issue 241 profile import failed"
  fi
  PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${out}")"
  rm -f -- "${out}" "${err}"
  assert_nonempty "${PROFILE_ID}" "issue 241 imported profile id"
  append_sensitive_value "${PROFILE_ID}"
  write_evidence profile_import pass
}

collect_host_sensitive_values() {
  local values
  values="$({
    hostname -f 2>/dev/null || true
    ip -o -4 addr show scope global 2>/dev/null | awk '{split($4, value, "/"); print value[1]; print $2}'
    ip -o -6 addr show scope global 2>/dev/null | awk '{split($4, value, "/"); print value[1]; print $2}'
    ip -4 route show default 2>/dev/null | awk '{for (i=1; i<=NF; i++) {if ($i=="via" || $i=="dev") print $(i+1)}}'
  } | sed '/^[[:space:]]*$/d' | sort -u)"
  append_sensitive_value "${values}"
}

collect_uri_sensitive_values() {
  local uri="$1" values
  values="$(python3 - "${uri}" <<'PY'
import base64
import ipaddress
import json
import re
import sys
import urllib.parse

uri = sys.argv[1].strip()
out = set()


def useful(value):
    text = urllib.parse.unquote(str(value or "")).strip()
    if not text:
        return False
    try:
        ipaddress.ip_address(text)
        return True
    except ValueError:
        pass
    if re.fullmatch(r"(?i)[0-9a-f]{8}-[0-9a-f-]{27,}", text):
        return True
    if len(text) >= 8 and ("." in text or ":" in text or "/" in text):
        return True
    return len(text) >= 16


def add(value):
    text = urllib.parse.unquote(str(value or "")).strip()
    if useful(text):
        out.add(text)


def walk(value):
    if isinstance(value, dict):
        for child in value.values():
            walk(child)
    elif isinstance(value, list):
        for child in value:
            walk(child)
    elif isinstance(value, str):
        add(value)

try:
    parsed = urllib.parse.urlsplit(uri)
except ValueError:
    parsed = None
if parsed is not None:
    add(parsed.hostname)
    add(parsed.username)
    add(parsed.fragment)
    for _, value in urllib.parse.parse_qsl(parsed.query, keep_blank_values=False):
        add(value)

if uri.lower().startswith("vmess://"):
    opaque = uri.split("://", 1)[1].split("#", 1)[0].split("?", 1)[0]
    normalized = opaque.replace("-", "+").replace("_", "/")
    normalized += "=" * ((4 - len(normalized) % 4) % 4)
    try:
        decoded = base64.b64decode(normalized, validate=False).decode("utf-8")
        walk(json.loads(decoded))
    except Exception:
        pass

for value in sorted(out):
    print(value)
PY
)"
  append_sensitive_value "${values}"
}

verify_package_provenance() {
  local extract_dir expected_cli expected_daemon expected_xray installed_cli installed_daemon installed_xray
  local package_version package_arch main_pid running_exe running_hash version_output build_commit

  [[ -f "${DEV_DEB}" ]] || fail "issue 241 built package is missing"
  extract_dir="$(mktemp -d "${E2E_TMP_ROOT}/issue241-package-extract.XXXXXX")"
  dpkg-deb -x "${DEV_DEB}" "${extract_dir}"
  expected_cli="$(sha256sum "${extract_dir}/usr/bin/podlaz" | awk '{print $1}')"
  expected_daemon="$(sha256sum "${extract_dir}/usr/bin/podlazd" | awk '{print $1}')"
  expected_xray="$(sha256sum "${extract_dir}/usr/lib/podlaz/xray" | awk '{print $1}')"
  installed_cli="$(sha256sum /usr/bin/podlaz | awk '{print $1}')"
  installed_daemon="$(sha256sum /usr/bin/podlazd | awk '{print $1}')"
  installed_xray="$(sha256sum /usr/lib/podlaz/xray | awk '{print $1}')"
  [[ "${installed_cli}" == "${expected_cli}" ]] || fail "installed podlaz hash does not match built package"
  [[ "${installed_daemon}" == "${expected_daemon}" ]] || fail "installed podlazd hash does not match built package"
  [[ "${installed_xray}" == "${expected_xray}" ]] || fail "installed Xray hash does not match built package"

  package_version="$(dpkg-deb --field "${DEV_DEB}" Version)"
  package_arch="$(dpkg-deb --field "${DEV_DEB}" Architecture)"
  [[ "${package_arch}" == "${PODLAZ_DEB_ARCH}" ]] || fail "built package architecture mismatch"
  dpkg-query -W -f='${db:Status-Status}\n' podlaz 2>/dev/null | grep -Fx installed >/dev/null || fail "podlaz package is not installed"
  dpkg-query -W -f='${Version}\n' podlaz 2>/dev/null | grep -Fx "${package_version}" >/dev/null || fail "installed package version mismatch"

  main_pid="$(systemctl show -p MainPID --value podlazd.service)"
  [[ "${main_pid}" =~ ^[1-9][0-9]*$ ]] || fail "podlazd.service has no running MainPID"
  running_exe="$(sudo -n readlink -f "/proc/${main_pid}/exe")"
  [[ "${running_exe}" == "/usr/bin/podlazd" ]] || fail "running daemon executable is not /usr/bin/podlazd"
  running_hash="$(sudo -n sha256sum "/proc/${main_pid}/exe" | awk '{print $1}')"
  [[ "${running_hash}" == "${expected_daemon}" ]] || fail "running daemon hash does not match built package"

  build_commit="${GITHUB_SHA:-$(git rev-parse HEAD)}"
  version_output="$(mktemp "${E2E_TMP_ROOT}/issue241-version.XXXXXX")"
  /usr/bin/podlaz version >"${version_output}"
  grep -F "${build_commit}" "${version_output}" >/dev/null || fail "installed CLI version does not identify the tested commit"
  rm -rf -- "${extract_dir}" "${version_output}"

  write_evidence package_version "${package_version}"
  write_evidence package_arch "${package_arch}"
  write_evidence podlaz_sha256 "${expected_cli}"
  write_evidence podlazd_sha256 "${expected_daemon}"
  write_evidence xray_sha256 "${expected_xray}"
  write_evidence running_daemon_match pass
}

capture_journal_cursor() {
  local output
  output="$(mktemp "${E2E_TMP_ROOT}/issue241-journal-cursor.XXXXXX")"
  sudo -n journalctl -u podlazd -n 1 --show-cursor --no-pager -o cat >"${output}" 2>/dev/null || fail "failed to capture journal cursor"
  JOURNAL_CURSOR="$(sed -n 's/^-- cursor: //p' "${output}" | tail -n 1)"
  rm -f -- "${output}"
  assert_nonempty "${JOURNAL_CURSOR}" "issue 241 journal cursor"
}

collect_active_sensitive_values() {
  local status_file="$1" config_path config_copy resolved_copy resolved_values runtime_values
  config_path="$(sed -n 's/^Runtime config: //p' "${status_file}" | head -n 1)"
  assert_nonempty "${config_path}" "active runtime config path"
  append_sensitive_value "${config_path}"

  config_copy="$(mktemp "${E2E_TMP_ROOT}/issue241-runtime-config.XXXXXX")"
  sudo -n cat -- "${config_path}" >"${config_copy}" || fail "cannot read active generated config for privacy needles"
  runtime_values="$(python3 - "${config_copy}" <<'PY'
import ipaddress
import json
import re
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
values = set()
uuid_re = re.compile(r"(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")
common = {"freedom", "blackhole", "direct", "block", "vless", "vmess", "trojan", "shadowsocks", "tcp", "udp", "tls", "none", "reality"}


def useful(text):
    try:
        ipaddress.ip_address(text)
        return True
    except ValueError:
        pass
    if uuid_re.fullmatch(text):
        return True
    if len(text) >= 8 and ("." in text or ":" in text or "/" in text):
        return True
    return len(text) >= 16


def walk(value):
    if isinstance(value, dict):
        for child in value.values():
            walk(child)
        return
    if isinstance(value, list):
        for child in value:
            walk(child)
        return
    if not isinstance(value, str):
        return
    text = value.strip()
    if not text or text.lower() in common:
        return
    if useful(text):
        values.add(text)

walk(payload)
for value in sorted(values):
    print(value)
PY
)"
  rm -f -- "${config_copy}"
  append_sensitive_value "${runtime_values}"

  resolved_copy="$(mktemp "${E2E_TMP_ROOT}/issue241-resolved.XXXXXX")"
  sudo -n resolvectl status podlaz0 --no-pager >"${resolved_copy}" 2>/dev/null || fail "active resolved status unavailable"
  resolved_values="$(python3 - "${resolved_copy}" <<'PY'
import ipaddress
import re
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    text = handle.read()
values = set()
for token in re.split(r"[\s,]+", text):
    token = token.strip("[]()")
    if not token:
        continue
    try:
        ipaddress.ip_address(token)
    except ValueError:
        continue
    values.add(token)
for value in sorted(values):
    print(value)
PY
)"
  rm -f -- "${resolved_copy}"
  append_sensitive_value "${resolved_values}"
}

assert_active_status_output() {
  local phase="$1" read_name="$2" output="$3"
  grep -Fx "Connection: active" "${output}" >/dev/null || fail "${phase}/${read_name}: status is not active"
  grep -Fx "Stale state: none" "${output}" >/dev/null || fail "${phase}/${read_name}: active status did not prove clean stale state"
  grep -Fx "Startup recovery scan: clean for active connection" "${output}" >/dev/null || fail "${phase}/${read_name}: active scan semantics are inconsistent"
  grep -Fx "Transaction: committed" "${output}" >/dev/null || fail "${phase}/${read_name}: committed transaction is not visible"
  if grep -F "Inspection warnings:" "${output}" >/dev/null; then
    fail "${phase}/${read_name}: normal active status contains inspection warnings"
  fi
  if grep -F "clean inactive state" "${output}" >/dev/null || grep -F "Stale state: unknown" "${output}" >/dev/null; then
    fail "${phase}/${read_name}: active status contains inactive or unknown recovery semantics"
  fi
}

active_status_stable_fields() {
  local output="$1"
  grep -E '^(Connection|Mode|Stale state|Startup recovery scan|Transaction|Rollback available|State path): ' "${output}"
}

assert_active_status() {
  local phase="$1" first second first_state second_state first_fields second_fields
  first="$(mktemp "${E2E_TMP_ROOT}/issue241-${phase}-status-first.XXXXXX")"
  second="$(mktemp "${E2E_TMP_ROOT}/issue241-${phase}-status-second.XXXXXX")"

  if ! run_installed_podlaz status >"${first}" 2>&1; then
    fail "${phase}/first: active status returned non-zero"
  fi
  assert_active_status_output "${phase}" first "${first}"

  if ! run_installed_podlaz status >"${second}" 2>&1; then
    fail "${phase}/second: repeated active status returned non-zero"
  fi
  assert_active_status_output "${phase}" second "${second}"

  first_state="$(sed -n 's/^State path: //p' "${first}" | head -n 1)"
  second_state="$(sed -n 's/^State path: //p' "${second}" | head -n 1)"
  assert_nonempty "${first_state}" "${phase} first active transaction identity"
  assert_nonempty "${second_state}" "${phase} second active transaction identity"
  [[ "${first_state}" == "${second_state}" ]] || fail "${phase}: active transaction identity changed between status reads"

  first_fields="$(active_status_stable_fields "${first}")"
  second_fields="$(active_status_stable_fields "${second}")"
  [[ "${first_fields}" == "${second_fields}" ]] || fail "${phase}: active lifecycle/recovery semantics changed between status reads"

  collect_active_sensitive_values "${first}"
  collect_active_sensitive_values "${second}"
  rm -f -- "${first}" "${second}"
  write_evidence "active_status_${phase}" pass
  write_evidence "active_status_stability_${phase}" pass
}

assert_inactive_status() {
  local phase="$1" output
  output="$(mktemp "${E2E_TMP_ROOT}/issue241-${phase}-inactive-status.XXXXXX")"
  if ! run_installed_podlaz status >"${output}" 2>&1; then
    fail "${phase}: inactive status returned non-zero"
  fi
  grep -Fx "Connection: inactive" "${output}" >/dev/null || fail "${phase}: status is not inactive"
  grep -Fx "Stale state: none" "${output}" >/dev/null || fail "${phase}: stale state is not clean after disconnect"
  grep -Fx "Startup recovery scan: clean inactive state" "${output}" >/dev/null || fail "${phase}: inactive scan is not clean"
  if grep -F "Recovery candidates:" "${output}" >/dev/null || grep -F "Inspection warnings:" "${output}" >/dev/null; then
    fail "${phase}: post-disconnect status still has recovery or inspection evidence"
  fi
  rm -f -- "${output}"
  write_evidence "inactive_status_${phase}" pass
}

assert_no_recovery_candidates() {
  local phase="$1" output
  output="$(mktemp "${E2E_TMP_ROOT}/issue241-${phase}-recover.XXXXXX")"
  run_installed_podlaz recover --json >"${output}" 2>/dev/null || fail "${phase}: recovery inspection failed"
  python3 - "${output}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)

if payload.get("status") != "ok":
    raise SystemExit("recovery inspection status is not ok")
if payload.get("warnings"):
    raise SystemExit("top-level recovery warnings remain")
recovery = payload.get("recovery")
if not isinstance(recovery, dict):
    raise SystemExit("recovery payload is missing")
if recovery.get("candidates"):
    raise SystemExit("recovery candidates remain")
if recovery.get("warnings"):
    raise SystemExit("recovery inspection warnings remain")
PY
  rm -f -- "${output}"
  write_evidence "recovery_clean_${phase}" pass
}

snapshot_network_manifest() {
  local phase="$1" manifest="$2"
  sudo -n rm -f -- "${manifest}" >/dev/null 2>&1 || fail "${phase}: failed to clear network manifest"
  sudo -n python3 "${FALLBACK_NETWORK_HELPER}" snapshot "${TRANSACTION_DIR}" "${manifest}" >/dev/null || fail "${phase}: network ownership snapshot failed"
  sudo -n test -f "${manifest}" || fail "${phase}: network manifest was not persisted"
}

verify_scoped_dns_query() {
  local phase="$1" output
  output="$(mktemp "${E2E_TMP_ROOT}/issue241-${phase}-dns.XXXXXX")"
  sudo -n resolvectl --cache=no --interface=podlaz0 -4 query "${PODLAZ_E2E_DNS_CHECK_HOST}" >"${output}" 2>/dev/null || fail "${phase}: scoped DNS query failed"
  grep -F -- "-- link: podlaz0" "${output}" >/dev/null || fail "${phase}: scoped DNS query did not use podlaz0"
  rm -f -- "${output}"
  write_evidence "scoped_dns_${phase}" pass
}

exercise_tun_traffic() {
  local phase="$1" public_ip
  verify_scoped_dns_query "${phase}"
  public_ip="$(curl -4 -fsS --max-time 30 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}")" || fail "${phase}: IPv4 HTTPS egress failed"
  assert_nonempty "${public_ip}" "${phase} public IPv4 egress"
  append_sensitive_value "${public_ip}"
  write_evidence "https_egress_${phase}" pass
}

run_normal_cycle() {
  local phase="$1" manifest
  manifest="${E2E_TMP_ROOT}/issue241-${phase}-network.json"

  run_installed_podlaz connect --mode tun "${PROFILE_ID}" >/dev/null 2>&1 || fail "${phase}: TUN connect failed"
  assert_tun_package_address_present "${phase}"
  assert_active_status "${phase}"
  exercise_tun_traffic "${phase}"
  snapshot_network_manifest "${phase}" "${manifest}"

  run_installed_podlaz disconnect >/dev/null 2>&1 || fail "${phase}: disconnect failed"
  verify_tun_package_resources_absent "${phase}" "${FALLBACK_NETWORK_HELPER}" "${manifest}" || fail "${phase}: owned resources did not converge after disconnect"
  assert_inactive_status "${phase}"
  assert_no_recovery_candidates "${phase}"
  write_evidence "normal_cycle_${phase}" pass
}

verify_journal_privacy() {
  local scan_dir journal_file report
  scan_dir="$(mktemp -d "${E2E_TMP_ROOT}/issue241-journal.XXXXXX")"
  journal_file="${scan_dir}/journal.log"
  report="${scan_dir}/redaction-scan.txt"

  sudo -n journalctl -u podlazd --after-cursor="${JOURNAL_CURSOR}" --no-pager -o cat >"${journal_file}" 2>/dev/null || fail "issue 241 bounded journal capture failed"

  python3 - "${journal_file}" <<'PY'
import re
import sys

marker = "podlazd: core xray "
allowed = [
    re.compile(r"^started pid=[1-9][0-9]*$"),
    re.compile(r"^start failed$"),
    re.compile(r"^stopped pid=[1-9][0-9]*$"),
    re.compile(r"^exited pid=[1-9][0-9]*$"),
    re.compile(r"^(stdout|stderr|unknown) output received pid=[1-9][0-9]*$"),
]
seen_started = False
seen_stopped = False
seen_output = False
with open(sys.argv[1], encoding="utf-8", errors="replace") as handle:
    for raw in handle:
        if marker not in raw:
            continue
        suffix = raw.rsplit(marker, 1)[1].strip()
        if not any(pattern.fullmatch(suffix) for pattern in allowed):
            raise SystemExit("non-structural core journal payload observed")
        seen_started = seen_started or suffix.startswith("started pid=")
        seen_stopped = seen_stopped or suffix.startswith("stopped pid=")
        seen_output = seen_output or " output received pid=" in suffix
if not seen_started or not seen_stopped:
    raise SystemExit("core lifecycle structural events were not observed")
if not seen_output:
    raise SystemExit("child-output privacy boundary was not exercised")
PY

  if ! python3 "${E2E_REDACTION_SCAN}" sensitive-values "${scan_dir}" "${report}" \
    "${PODLAZ_E2E_PROFILE_URI}" \
    "${PODLAZ_E2E_PROFILE_URI_LIST}" \
    "${SENSITIVE_VALUES}"; then
    fail "issue 241 journal contains a configured sensitive value"
  fi
  if grep -F "accepted tcp:" "${journal_file}" >/dev/null || grep -F "accepted udp:" "${journal_file}" >/dev/null; then
    fail "issue 241 journal contains raw Xray access output"
  fi

  rm -rf -- "${scan_dir}"
  write_evidence journal_privacy pass
  write_evidence journal_structural_events pass
}

setup_isolated_xdg "issue241-package-acceptance"
: >"${EVIDENCE_FILE}"
collect_host_sensitive_values

PRIMARY_URI="$(first_profile_uri)"
assert_nonempty "${PRIMARY_URI}" "issue 241 primary profile URI"
collect_uri_sensitive_values "${PRIMARY_URI}"

sudo -n systemctl daemon-reload
sudo -n systemctl restart podlazd.service
wait_for_daemon_socket
verify_package_provenance
capture_secret_import "${PRIMARY_URI}"
capture_journal_cursor

run_normal_cycle first
run_normal_cycle reconnect
verify_journal_privacy

assert_artifacts_do_not_contain_sensitive_values \
  "issue241-package-acceptance" \
  "${PODLAZ_E2E_PROFILE_URI}" \
  "${PODLAZ_E2E_PROFILE_URI_LIST}" \
  "${SENSITIVE_VALUES}"

write_evidence installed_package_acceptance pass
log "issue 241 installed-package acceptance completed"