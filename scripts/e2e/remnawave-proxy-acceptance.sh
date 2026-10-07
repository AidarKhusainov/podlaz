#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd bash python3 docker jq sha256sum sudo apt stat

: "${PODLAZ_E2E_PACKAGE_PATH:=}"
: "${PODLAZ_E2E_RELIABILITY_CYCLES:=0}"
FIXTURE_TOOL="${SCRIPT_DIR}/lib/remnawave_fixture.py"
FIXTURE_ROOT="${E2E_TMP_ROOT}/remnawave-fixture-${RANDOM}-${RANDOM}"
PRIVATE_ROOT="${E2E_TMP_ROOT}/remnawave-private-${RANDOM}-${RANDOM}"
PUBLIC_REPORT="${E2E_ARTIFACT_DIR}/remnawave-proxy-result.txt"
DIGESTS_FILE="${PRIVATE_ROOT}/image-digests.json"
PACKAGE_KEPT=0
FIXTURE_STARTED=0
FIXTURE_CLEAN=unknown
RESULT=FAIL
FAILURE_CLASS=fixture
FAILURE_STEP=bootstrap

mkdir -p "${PRIVATE_ROOT}"
chmod 0700 "${PRIVATE_ROOT}"
: >"${PUBLIC_REPORT}"

write_report() {
  local panel_digest="" node_digest="" postgres_digest="" valkey_digest=""
  if [[ -f "${DIGESTS_FILE}" ]]; then
    panel_digest="$(jq -r '.panel // ""' "${DIGESTS_FILE}")"
    node_digest="$(jq -r '.node // ""' "${DIGESTS_FILE}")"
    postgres_digest="$(jq -r '.postgres // ""' "${DIGESTS_FILE}")"
    valkey_digest="$(jq -r '.valkey // ""' "${DIGESTS_FILE}")"
  fi
  cat >"${PUBLIC_REPORT}" <<EOF
result=${RESULT}
remnawave.panel.version=3.4.5
remnawave.node.version=3.4.2
remnawave.panel.digest=${panel_digest}
remnawave.node.digest=${node_digest}
remnawave.postgres.digest=${postgres_digest}
remnawave.valkey.digest=${valkey_digest}
fixture.cleanup=${FIXTURE_CLEAN}
failure.class=$([[ "${RESULT}" == PASS ]] && printf none || printf '%s' "${FAILURE_CLASS}")
failure.step=$([[ "${RESULT}" == PASS ]] && printf none || printf '%s' "${FAILURE_STEP}")
EOF
}

cleanup() {
  local rc=$?
  set +e
  if [[ "${FIXTURE_STARTED}" == 1 || -d "${FIXTURE_ROOT}" ]]; then
    if python3 "${FIXTURE_TOOL}" --root "${FIXTURE_ROOT}" cleanup; then
      FIXTURE_CLEAN=pass
    else
      FIXTURE_CLEAN=fail
      RESULT=FAIL
      FAILURE_CLASS=infrastructure
      FAILURE_STEP=fixture_cleanup
      rc=1
    fi
  else
    FIXTURE_CLEAN=pass
  fi
  if [[ "${PACKAGE_KEPT}" == 1 ]]; then
    sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
    sudo -n apt remove -y podlaz >/dev/null 2>&1 || true
  fi
  write_report
  rm -rf -- "${PRIVATE_ROOT}"
  if [[ "${RESULT}" != PASS ]]; then
    rc=1
  fi
  exit "${rc}"
}
trap cleanup EXIT

mask_file() {
  local path="$1" line
  [[ -f "${path}" ]] || return 0
  while IFS= read -r line; do
    [[ -n "${line}" ]] && mask_value "${line}"
  done <"${path}"
}

client_env() {
  local root="$1"
  shift
  env \
    XDG_CONFIG_HOME="${root}/config" \
    XDG_STATE_HOME="${root}/state" \
    XDG_CACHE_HOME="${root}/cache" \
    /usr/bin/podlaz "$@"
}

client_run_private() {
  local label="$1" root="$2"
  shift 2
  mkdir -p "${root}/config" "${root}/state" "${root}/cache"
  local stdout="${PRIVATE_ROOT}/${label}.stdout" stderr="${PRIVATE_ROOT}/${label}.stderr"
  client_env "${root}" "$@" >"${stdout}" 2>"${stderr}"
}

subscription_id() {
  python3 - "$1/state/podlaz/subscriptions.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    data = json.load(handle)
items = data.get("subscriptions", [])
if len(items) != 1:
    raise SystemExit("expected exactly one subscription")
print(items[0]["id"])
PY
}

assert_server_identity() {
  local snapshot="$1" client_id_file="$2" description="$3"
  local total server client
  total="$(jq -r '.total' "${snapshot}")"
  [[ "${total}" == 1 ]] || fail "${description}: expected exactly one provider HWID, got ${total}"
  server="$(jq -r '.devices[0].hwid' "${snapshot}")"
  client="$(tr -d '\r\n[:space:]' <"${client_id_file}")"
  [[ -n "${client}" && "${server}" == "${client}" ]] || fail "${description}: provider HWID does not match Podlaz stable client identity"
  mask_value "${client}"
}

state_digest() {
  local root="$1"
  sha256sum \
    "${root}/state/podlaz/client-id" \
    "${root}/state/podlaz/subscriptions.json" \
    "${root}/state/podlaz/profiles.json" | sha256sum | awk '{print $1}'
}

log "bootstrap ephemeral Remnawave fixture"
python3 "${FIXTURE_TOOL}" --root "${FIXTURE_ROOT}" bootstrap
FIXTURE_STARTED=1
python3 "${FIXTURE_TOOL}" --root "${FIXTURE_ROOT}" digests --output "${DIGESTS_FILE}"
FAILURE_STEP=proxy_q05
FAILURE_CLASS=product

DATA_SUB_URL_FILE="${FIXTURE_ROOT}/users/data-plane/subscription-url"
DATA_PROFILE_URI_FILE="${FIXTURE_ROOT}/users/data-plane/profile-uri"
NODE_ACCESS_LOG="$(jq -r '.node_access_log' "${FIXTURE_ROOT}/state.json")"
mask_file "${DATA_SUB_URL_FILE}"
mask_file "${DATA_PROFILE_URI_FILE}"
mask_value "$(jq -r '.token' "${FIXTURE_ROOT}/state.json")"
mask_value "$(jq -r '.admin_pass' "${FIXTURE_ROOT}/state.json")"
mask_value "$(jq -r '.runner_ip' "${FIXTURE_ROOT}/state.json")"

log "run existing Q05 assertions through ephemeral Remnawave"
PACKAGE_KEPT=1
E2E_ARTIFACT_DIR="${FIXTURE_ROOT}/data-plane-artifacts" \
E2E_TMP_ROOT="${FIXTURE_ROOT}/data-plane-tmp" \
PODLAZ_E2E_PROFILE_URI="$(cat "${DATA_SUB_URL_FILE}")" \
PODLAZ_E2E_EXPECTED_EGRESS_IP="" \
PODLAZ_E2E_PROVIDER_LOG_FILE="${NODE_ACCESS_LOG}" \
PODLAZ_E2E_PACKAGE_PATH="${PODLAZ_E2E_PACKAGE_PATH}" \
PODLAZ_E2E_RELIABILITY_CYCLES="${PODLAZ_E2E_RELIABILITY_CYCLES}" \
PODLAZ_E2E_KEEP_PACKAGE=true \
bash "${SCRIPT_DIR}/data-plane.sh"

FAILURE_STEP=hwid_lifecycle
FAILURE_CLASS=product
HWID_URL_FILE="${FIXTURE_ROOT}/users/hwid/subscription-url"
mask_file "${HWID_URL_FILE}"
HWID_URL="$(cat "${HWID_URL_FILE}")"
CLIENT_A="${PRIVATE_ROOT}/client-a"
CLIENT_B="${PRIVATE_ROOT}/client-b"

log "prove real Remnawave subscription and stable x-hwid lifecycle"
client_run_private a-import "${CLIENT_A}" import "${HWID_URL}"
A_SUB_ID="$(subscription_id "${CLIENT_A}")"
python3 "${FIXTURE_TOOL}" --root "${FIXTURE_ROOT}" hwid-snapshot --name hwid --output "${PRIVATE_ROOT}/a-initial.json"
assert_server_identity "${PRIVATE_ROOT}/a-initial.json" "${CLIENT_A}/state/podlaz/client-id" "initial import"
client_run_private a-refresh "${CLIENT_A}" subscription update "${A_SUB_ID}"
python3 "${FIXTURE_TOOL}" --root "${FIXTURE_ROOT}" hwid-snapshot --name hwid --output "${PRIVATE_ROOT}/a-refresh.json"
assert_server_identity "${PRIVATE_ROOT}/a-refresh.json" "${CLIENT_A}/state/podlaz/client-id" "same-client refresh"

python3 "${FIXTURE_TOOL}" --root "${FIXTURE_ROOT}" hwid-clear --name hwid
client_run_private b-import "${CLIENT_B}" import "${HWID_URL}"
B_SUB_ID="$(subscription_id "${CLIENT_B}")"
python3 "${FIXTURE_TOOL}" --root "${FIXTURE_ROOT}" hwid-snapshot --name hwid --output "${PRIVATE_ROOT}/b-initial.json"
assert_server_identity "${PRIVATE_ROOT}/b-initial.json" "${CLIENT_B}/state/podlaz/client-id" "second clean identity setup"
B_COMMITTED_DIGEST="$(state_digest "${CLIENT_B}")"

python3 "${FIXTURE_TOOL}" --root "${FIXTURE_ROOT}" hwid-clear --name hwid
client_run_private a-reregister "${CLIENT_A}" subscription update "${A_SUB_ID}"
python3 "${FIXTURE_TOOL}" --root "${FIXTURE_ROOT}" hwid-snapshot --name hwid --output "${PRIVATE_ROOT}/a-reregister.json"
assert_server_identity "${PRIVATE_ROOT}/a-reregister.json" "${CLIENT_A}/state/podlaz/client-id" "first identity re-registration"

set +e
client_run_private b-rejected "${CLIENT_B}" subscription update "${B_SUB_ID}"
B_REJECT_RC=$?
set -e
[[ "${B_REJECT_RC}" != 0 ]] || fail "hwidDeviceLimit=1 did not reject the second clean identity"
B_AFTER_REJECT_DIGEST="$(state_digest "${CLIENT_B}")"
[[ "${B_AFTER_REJECT_DIGEST}" == "${B_COMMITTED_DIGEST}" ]] || fail "rejected refresh modified committed subscription/profile/client identity state"
python3 "${FIXTURE_TOOL}" --root "${FIXTURE_ROOT}" hwid-snapshot --name hwid --output "${PRIVATE_ROOT}/after-reject.json"
assert_server_identity "${PRIVATE_ROOT}/after-reject.json" "${CLIENT_A}/state/podlaz/client-id" "provider state after rejected second identity"

client_run_private a-final-refresh "${CLIENT_A}" subscription update "${A_SUB_ID}"
python3 "${FIXTURE_TOOL}" --root "${FIXTURE_ROOT}" hwid-snapshot --name hwid --output "${PRIVATE_ROOT}/a-final.json"
assert_server_identity "${PRIVATE_ROOT}/a-final.json" "${CLIENT_A}/state/podlaz/client-id" "final same-client refresh"

FAILURE_STEP=artifact_privacy
FAILURE_CLASS=fixture
assert_artifacts_do_not_contain_sensitive_values \
  remnawave-proxy \
  "${HWID_URL}" \
  "$(cat "${DATA_SUB_URL_FILE}")" \
  "$(cat "${DATA_PROFILE_URI_FILE}")" \
  "$(cat "${CLIENT_A}/state/podlaz/client-id")" \
  "$(cat "${CLIENT_B}/state/podlaz/client-id")" \
  "$(jq -r '.token' "${FIXTURE_ROOT}/state.json")" \
  "$(jq -r '.admin_pass' "${FIXTURE_ROOT}/state.json")" \
  "$(jq -r '.runner_ip' "${FIXTURE_ROOT}/state.json")"

RESULT=PASS
FAILURE_CLASS=none
FAILURE_STEP=none
log "ephemeral Remnawave Q05/HWID qualification completed"
