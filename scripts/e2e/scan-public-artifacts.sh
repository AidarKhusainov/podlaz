#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd awk curl hostname ip python3 sed sort

private_values_file="${E2E_TMP_ROOT}/data-plane-sensitive-values.txt"
sensitive_values=(
  "${PODLAZ_E2E_PROFILE_URI:-}"
  "${PODLAZ_E2E_PROFILE_URI_LIST:-}"
  "${PODLAZ_E2E_EXPECTED_EGRESS_IP:-}"
)

if [[ -f "${private_values_file}" ]]; then
  while IFS= read -r value; do
    [[ -n "${value}" ]] && sensitive_values+=("${value}")
  done <"${private_values_file}"
fi

while IFS= read -r value; do
  [[ -n "${value}" ]] && sensitive_values+=("${value}")
done < <(
  {
    hostname -f 2>/dev/null || true
    ip -o -4 addr show scope global 2>/dev/null | awk '{split($4, value, "/"); print value[1]; print $2}' || true
    ip -o -6 addr show scope global 2>/dev/null | awk '{split($4, value, "/"); print value[1]; print $2}' || true
    ip -4 route show default 2>/dev/null | awk '{for (i=1; i<=NF; i++) if ($i=="via" || $i=="dev") print $(i+1)}' || true
    if command -v resolvectl >/dev/null 2>&1; then
      resolvectl status --no-pager 2>/dev/null | awk -F: '/Current DNS Server|DNS Servers|DNS Domain/ {gsub(/^[[:space:]]+/, "", $2); for (i=1; i<=split($2, values, /[[:space:]]+/); i++) print values[i]}' || true
    fi
    curl -4 -fsS --max-time 10 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:-https://api.ipify.org}" 2>/dev/null || true
  } | sed '/^[[:space:]]*$/d' | sort -u
)

assert_artifacts_do_not_contain_sensitive_values "real-provider-public-artifacts" "${sensitive_values[@]}"
