#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd chmod find grep mv python3 sed wc

scan_provider_tun() {
  local result_file="${E2E_ARTIFACT_DIR}/hosted-real-provider-tun.txt"
  mapfile -d '' -t public_entries < <(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -print0)

  [[ "${#public_entries[@]}" -eq 1 ]] || fail "provider TUN public artifacts must contain exactly one filesystem entry"
  [[ "${public_entries[0]}" == "${result_file}" ]] || fail "unexpected provider TUN public artifact"
  [[ -f "${result_file}" && ! -L "${result_file}" ]] || fail "provider TUN result must be a regular file"

  python3 - "${result_file}" <<'PY'
import re
import sys
from pathlib import Path

path = Path(sys.argv[1])
required = {
    "candidate.provenance",
    "provider.material_private",
    "ordinary_user.boundary",
    "tun.verified_active",
    "tun.system_dns",
    "tun.ipv4_tcp",
    "tun.tls",
    "tun.https",
    "tun.provider_egress",
    "tun.doctor",
    "privacy.direct_uplink_blocked",
    "foreign.state_preserved",
    "tun.clean_disconnect",
    "tun.terminal_cleanup",
    "tun.recovery_clean",
    "guest.baseline_restored",
    "guest.ordinary_connectivity_restored",
    "outer.cleanup",
    "artifact.privacy",
}
values = {}
candidate = {}
failure = {}
for line in path.read_text(encoding="utf-8").splitlines():
    match = re.fullmatch(r"([a-z0-9_.-]+)=(pass|fail|observed|unavailable)", line)
    if match:
        key, value = match.groups()
        if key not in required or key in values:
            raise SystemExit("provider TUN report has an unexpected or duplicate evidence key")
        values[key] = value
        continue
    match = re.fullmatch(r"candidate\.(commit|package_sha256)=([0-9a-f]+)", line)
    if match:
        key, value = match.groups()
        expected_length = 40 if key == "commit" else 64
        if key in candidate or len(value) != expected_length:
            raise SystemExit("provider TUN report has invalid candidate provenance")
        candidate[key] = value
        continue
    match = re.fullmatch(r"failure\.(class|step)=([A-Za-z0-9_.-]+)", line)
    if match:
        key, value = match.groups()
        if key in failure:
            raise SystemExit("provider TUN report has duplicate failure metadata")
        failure[key] = value
        continue
    raise SystemExit("provider TUN report contains non-normalized data")

if set(values) != required:
    raise SystemExit("provider TUN report evidence schema is incomplete")
if set(candidate) != {"commit", "package_sha256"}:
    raise SystemExit("provider TUN report candidate provenance is incomplete")
if set(failure) != {"class", "step"}:
    raise SystemExit("provider TUN report failure metadata is incomplete")
if failure["class"] not in {
    "none", "product", "provider", "fixture", "infrastructure", "capability", "diagnostic_unknown"
}:
    raise SystemExit("provider TUN report failure class is invalid")
PY
}

if [[ "${1:-}" == "real-provider-tun" ]]; then
  scan_provider_tun
  exit 0
fi
(($# == 0)) || fail "usage: $0 [real-provider-tun]"

result_file="${E2E_ARTIFACT_DIR}/real-provider-result.txt"
mapfile -d '' -t public_entries < <(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -print0)

[[ "${#public_entries[@]}" -eq 1 ]] || fail "real-provider public artifacts must contain exactly one filesystem entry"
[[ "${public_entries[0]}" == "${result_file}" ]] || fail "unexpected real-provider public artifact"
[[ -f "${result_file}" && ! -L "${result_file}" ]] || fail "real-provider result must be a regular file"

IFS= read -r result <"${result_file}" || fail "real-provider result is empty"
case "${result}" in
  "real-provider data-plane: success")
    [[ "$(wc -l <"${result_file}")" -eq 1 ]] || fail "successful real-provider result must contain exactly one line"
    ;;
  "real-provider data-plane: failure")
    if [[ "$(wc -l <"${result_file}")" -eq 3 ]]; then
      sed -n '2p' "${result_file}" | grep -Eq '^step: [A-Za-z0-9._-]+$' || fail "real-provider failure step is invalid"
      sed -n '3p' "${result_file}" | grep -Eq '^class: (authorization-denied|authorization-unavailable|daemon-internal|daemon-unavailable|unclassified)$' || fail "real-provider failure class is invalid"
      exit 0
    fi
    [[ "$(wc -l <"${result_file}")" -eq 1 ]] || fail "failed real-provider result must contain one raw or three sanitized lines"

    private_dir="${E2E_TMP_ROOT}/private-command"
    failure_marker="${private_dir}/failed-command"
    failure_stderr=""
    failure_step="data-plane"
    failure_class="unclassified"

    if [[ -f "${failure_marker}" && ! -L "${failure_marker}" ]]; then
      IFS= read -r failure_stderr_name <"${failure_marker}" || fail "private failure marker is empty"
      [[ "${failure_stderr_name}" =~ ^[0-9]{3}-[A-Za-z0-9._-]+[.]stderr$ ]] || fail "private failure marker is invalid"
      failure_stderr="${private_dir}/${failure_stderr_name}"
      [[ -f "${failure_stderr}" && ! -L "${failure_stderr}" ]] || fail "private failure stderr is unavailable"
      failure_step="${failure_stderr_name%.stderr}"
      failure_step="${failure_step#*-}"
      failure_class="$(python3 "${SCRIPT_DIR}/lib/tun_soak_metrics.py" classify-cli-error --stderr-file "${failure_stderr}")"
    fi

    sanitized_result="${result_file}.tmp"
    printf 'real-provider data-plane: failure\nstep: %s\nclass: %s\n' \
      "$(safe_name "${failure_step}")" "${failure_class}" >"${sanitized_result}"
    chmod 0600 "${sanitized_result}"
    mv -f -- "${sanitized_result}" "${result_file}"

    [[ "$(wc -l <"${result_file}")" -eq 3 ]] || fail "failed real-provider result must contain exactly three lines"
    sed -n '2p' "${result_file}" | grep -Eq '^step: [A-Za-z0-9._-]+$' || fail "real-provider failure step is invalid"
    sed -n '3p' "${result_file}" | grep -Eq '^class: (authorization-denied|authorization-unavailable|daemon-internal|daemon-unavailable|unclassified)$' || fail "real-provider failure class is invalid"
    ;;
  *) fail "real-provider result has unexpected content" ;;
esac
