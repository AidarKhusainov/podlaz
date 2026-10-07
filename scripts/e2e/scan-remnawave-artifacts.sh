#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd find python3

case "${1:-}" in
  proxy)
    report="${E2E_ARTIFACT_DIR}/remnawave-proxy.txt"
    expected='candidate.commit,candidate.package_sha256,remnawave.panel_version,remnawave.node_version,remnawave.proxy_data_plane,remnawave.proxy_path_attribution,remnawave.proxy_cleanup'
    ;;
  subscription)
    report="${E2E_ARTIFACT_DIR}/remnawave-subscription.txt"
    expected='candidate.commit,candidate.package_sha256,remnawave.panel_version,remnawave.node_version,remnawave.subscription_import,remnawave.subscription_refresh,remnawave.hwid_registration,remnawave.hwid_stable,remnawave.hwid_device_limit,remnawave.rejected_state_preserved'
    ;;
  *)
    fail "usage: $0 proxy|subscription"
    ;;
esac

mapfile -d '' -t entries < <(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 -print0)
[[ "${#entries[@]}" -eq 1 && "${entries[0]}" == "${report}" ]] || fail "Remnawave public artifacts must contain only the normalized report"
[[ -f "${report}" && ! -L "${report}" ]] || fail "Remnawave public report must be a regular non-symlink file"

python3 - "${report}" "${expected}" <<'PY'
import re
import sys
from pathlib import Path

path = Path(sys.argv[1])
expected = set(sys.argv[2].split(","))
values = {}
for line in path.read_text(encoding="utf-8").splitlines():
    if "=" not in line:
        raise SystemExit("public Remnawave report contains non-normalized data")
    key, value = line.split("=", 1)
    if key not in expected or key in values:
        raise SystemExit("public Remnawave report has an unexpected or duplicate key")
    values[key] = value
if set(values) != expected:
    raise SystemExit("public Remnawave report schema is incomplete")
if not re.fullmatch(r"[0-9a-f]{40}", values["candidate.commit"]):
    raise SystemExit("candidate commit is invalid")
if not re.fullmatch(r"[0-9a-f]{64}", values["candidate.package_sha256"]):
    raise SystemExit("candidate digest is invalid")
if values["remnawave.panel_version"] != "3.4.5" or values["remnawave.node_version"] != "3.4.2":
    raise SystemExit("Remnawave version evidence is invalid")
for key, value in values.items():
    if key.startswith("remnawave.") and key not in {"remnawave.panel_version", "remnawave.node_version"} and value != "pass":
        raise SystemExit(f"required Remnawave evidence is not successful: {key}")
PY
