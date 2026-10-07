#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/remnawave.sh
source "${SCRIPT_DIR}/lib/remnawave.sh"

require_cmd find python3

case "${1:-}" in
  proxy)
    report="${E2E_ARTIFACT_DIR}/remnawave-proxy.txt"
    expected='remnawave.proxy_data_plane,remnawave.proxy_path_attribution,remnawave.proxy_cleanup,fixture.cleanup'
    ;;
  subscription)
    report="${E2E_ARTIFACT_DIR}/remnawave-subscription.txt"
    expected='remnawave.subscription_import,remnawave.subscription_refresh,remnawave.hwid_registration,remnawave.hwid_stable,remnawave.hwid_device_limit,remnawave.rejected_state_preserved,fixture.cleanup'
    ;;
  *)
    fail "usage: $0 proxy|subscription"
    ;;
esac

mapfile -d '' -t entries < <(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 -print0)
[[ "${#entries[@]}" -eq 1 && "${entries[0]}" == "${report}" ]] || fail "Remnawave public artifacts must contain only the normalized report"
[[ -f "${report}" && ! -L "${report}" ]] || fail "Remnawave public report must be a regular non-symlink file"

python3 - "${report}" "${expected}" "${REMNAWAVE_PANEL_VERSION}" "${REMNAWAVE_NODE_VERSION}" <<'PY'
import re
import sys
from pathlib import Path

path = Path(sys.argv[1])
evidence = set(sys.argv[2].split(","))
panel_version = sys.argv[3]
node_version = sys.argv[4]
metadata = {
    "candidate.commit",
    "candidate.package_sha256",
    "remnawave.panel_version",
    "remnawave.node_version",
    "failure.class",
    "failure.step",
}
expected = evidence | metadata
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
if values["remnawave.panel_version"] != panel_version or values["remnawave.node_version"] != node_version:
    raise SystemExit("Remnawave version evidence is invalid")

for key in evidence:
    if values[key] not in {"pass", "fail"}:
        raise SystemExit(f"Remnawave evidence is not normalized: {key}")

failure_class = values["failure.class"]
failure_step = values["failure.step"]
allowed_classes = {
    "none",
    "product",
    "remnawave",
    "fixture",
    "infrastructure",
    "capability",
    "diagnostic_unknown",
}
if failure_class not in allowed_classes:
    raise SystemExit("Remnawave failure class is invalid")
if not re.fullmatch(r"[A-Za-z0-9_.-]+", failure_step):
    raise SystemExit("Remnawave failure step is invalid")

failed = [key for key in evidence if values[key] == "fail"]
if failure_class == "none":
    if failure_step != "none" or failed:
        raise SystemExit("successful Remnawave report contains failure evidence")
else:
    if failure_step == "none" or not failed:
        raise SystemExit("failed Remnawave report is missing normalized failure evidence")
PY
