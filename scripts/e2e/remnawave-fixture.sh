#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd curl docker jq openssl python3

: "${E2E_TMP_ROOT:=$(mktemp -d)}"
PRIVATE_ROOT="${E2E_TMP_ROOT}/remnawave-fixture"
COMPOSE_FILE="${PRIVATE_ROOT}/compose.yml"
PANEL_ENV="${PRIVATE_ROOT}/panel.env"
NODE_ENV="${PRIVATE_ROOT}/node.env"
STATE_DIR="${PRIVATE_ROOT}/state"
PANEL_URL="http://127.0.0.1:3000"
PANEL_METRICS_URL="http://127.0.0.1:3001/health"
PANEL_IMAGE="${PODLAZ_REMNAWAVE_PANEL_IMAGE:-ghcr.io/remnawave/backend:3.4.5}"
NODE_IMAGE="${PODLAZ_REMNAWAVE_NODE_IMAGE:-ghcr.io/remnawave/node:3.4.2}"
POSTGRES_IMAGE="${PODLAZ_REMNAWAVE_POSTGRES_IMAGE:-postgres:18.4}"
VALKEY_IMAGE="${PODLAZ_REMNAWAVE_VALKEY_IMAGE:-valkey/valkey:9-alpine}"
NODE_PORT=2222
XRAY_PORT=24443

ADMIN_TOKEN=""
USER_ID=""
USER_SHORT_UUID=""
FIXTURE_STARTED=0

api() {
  local method="$1" path="$2" body="${3:-}" output="$4"
  local args=(-fsS --max-time 20 -X "${method}" "${PANEL_URL}${path}" -H 'accept: application/json')
  if [[ -n "${ADMIN_TOKEN}" ]]; then
    args+=(-H "authorization: Bearer ${ADMIN_TOKEN}")
  fi
  if [[ -n "${body}" ]]; then
    args+=(-H 'content-type: application/json' --data-binary "@${body}")
  fi
  curl "${args[@]}" >"${output}"
}

wait_http() {
  local url="$1" attempts="${2:-120}" i
  for i in $(seq 1 "${attempts}"); do
    if curl -fsS --max-time 2 "${url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_panel_api() {
  local output="${STATE_DIR}/auth-status.json" i
  for i in $(seq 1 120); do
    if curl -fsS --max-time 3 "${PANEL_URL}/api/auth/status" >"${output}" 2>/dev/null &&
       jq -e '.response.isRegisterAllowed == true' "${output}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_node_connected() {
  local node_uuid="$1" output="${STATE_DIR}/node-status.json" i
  for i in $(seq 1 90); do
    api GET "/api/nodes/${node_uuid}" "" "${output}" || true
    if jq -e '.response.isConnected == true and .response.isDisabled == false' "${output}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

write_compose() {
  cat >"${COMPOSE_FILE}" <<EOF
services:
  remnawave-db:
    image: ${POSTGRES_IMAGE}
    environment:
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
      POSTGRES_DB: postgres
      TZ: UTC
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres -d postgres"]
      interval: 2s
      timeout: 5s
      retries: 30
    volumes:
      - db:/var/lib/postgresql
    networks: [remnawave]

  remnawave-redis:
    image: ${VALKEY_IMAGE}
    command:
      - valkey-server
      - --save
      - ""
      - --appendonly
      - "no"
      - --maxmemory-policy
      - noeviction
      - --port
      - "6379"
    healthcheck:
      test: ["CMD", "valkey-cli", "ping"]
      interval: 2s
      timeout: 3s
      retries: 30
    networks: [remnawave]

  remnawave:
    image: ${PANEL_IMAGE}
    env_file: [${PANEL_ENV}]
    ports:
      - "127.0.0.1:3000:3000"
      - "127.0.0.1:3001:3001"
    depends_on:
      remnawave-db:
        condition: service_healthy
      remnawave-redis:
        condition: service_healthy
    healthcheck:
      test: ["CMD-SHELL", "curl -f http://localhost:3001/health"]
      interval: 5s
      timeout: 3s
      retries: 30
      start_period: 10s
    networks: [remnawave]

  remnanode:
    image: ${NODE_IMAGE}
    env_file: [${NODE_ENV}]
    ports:
      - "127.0.0.1:${XRAY_PORT}:${XRAY_PORT}"
    networks: [remnawave]

networks:
  remnawave:
    name: podlaz-remnawave-fixture

volumes:
  db:
    name: podlaz-remnawave-db
EOF
  chmod 0600 "${COMPOSE_FILE}"
}

write_panel_env() {
  cat >"${PANEL_ENV}" <<EOF
APP_PORT=3000
METRICS_PORT=3001
API_INSTANCES=1
DATABASE_URL=postgresql://postgres:${POSTGRES_PASSWORD}@remnawave-db:5432/postgres
REDIS_HOST=remnawave-redis
REDIS_PORT=6379
APP_SECRET=${APP_SECRET}
PANEL_DOMAIN=127.0.0.1
FRONT_END_DOMAIN=*
SUB_PUBLIC_DOMAIN=127.0.0.1:3000/api/sub
METRICS_USER=fixture
METRICS_PASS=${METRICS_PASS}
WEBHOOK_ENABLED=false
WEBHOOK_URL=http://127.0.0.1/
WEBHOOK_SECRET_HEADER=${WEBHOOK_SECRET}
IS_TELEGRAM_NOTIFICATIONS_ENABLED=false
SERVICE_SNI_VERIFICATION=false
POSTGRES_USER=postgres
POSTGRES_PASSWORD=${POSTGRES_PASSWORD}
POSTGRES_DB=postgres
EOF
  chmod 0600 "${PANEL_ENV}"
}

create_config_profile() {
  local body="${STATE_DIR}/create-profile.json" response="${STATE_DIR}/profile-response.json"
  cat >"${body}" <<EOF
{
  "name": "Podlaz Fixture",
  "config": {
    "log": {
      "access": "/tmp/podlaz-remnawave-access.log",
      "error": "/tmp/podlaz-remnawave-error.log",
      "loglevel": "warning"
    },
    "inbounds": [
      {
        "tag": "PODLAZ_VLESS_TCP",
        "listen": "0.0.0.0",
        "port": ${XRAY_PORT},
        "protocol": "vless",
        "settings": {
          "clients": [],
          "decryption": "none"
        },
        "streamSettings": {
          "network": "tcp",
          "security": "none"
        },
        "sniffing": {
          "enabled": true,
          "destOverride": ["http", "tls"]
        }
      }
    ],
    "outbounds": [
      {"protocol": "freedom", "tag": "DIRECT"},
      {"protocol": "blackhole", "tag": "BLOCK"}
    ],
    "routing": {"rules": []}
  }
}
EOF
  chmod 0600 "${body}"
  api POST /api/config-profiles "${body}" "${response}"
  PROFILE_UUID="$(jq -er '.response.uuid' "${response}")"
  INBOUND_UUID="$(jq -er '.response.inbounds[0].uuid' "${response}")"
}

create_node() {
  local body="${STATE_DIR}/create-node.json" response="${STATE_DIR}/node-response.json"
  jq -n     --arg profile "${PROFILE_UUID}"     --arg inbound "${INBOUND_UUID}"     '{
      name:"Podlaz Node",
      address:"remnanode",
      port:2222,
      countryCode:"ZZ",
      configProfile:{
        activeConfigProfileUuid:$profile,
        activeInbounds:[$inbound]
      }
    }' >"${body}"
  chmod 0600 "${body}"
  api POST /api/nodes "${body}" "${response}"
  NODE_UUID="$(jq -er '.response.uuid' "${response}")"
}

start_node() {
  local key_response="${STATE_DIR}/node-key.json"
  api GET /api/keygen "" "${key_response}"
  NODE_SECRET="$(jq -er '.response.secretKey' "${key_response}")"
  cat >"${NODE_ENV}" <<EOF
NODE_PORT=${NODE_PORT}
SECRET_KEY=${NODE_SECRET}
EOF
  chmod 0600 "${NODE_ENV}"
  docker compose -f "${COMPOSE_FILE}" up -d remnanode >/dev/null
  wait_node_connected "${NODE_UUID}" || fail "Remnawave Node did not become connected"
}

create_squad() {
  local body="${STATE_DIR}/create-squad.json" response="${STATE_DIR}/squad-response.json"
  jq -n --arg inbound "${INBOUND_UUID}" '{name:"Podlaz Squad",inbounds:[$inbound]}' >"${body}"
  chmod 0600 "${body}"
  api POST /api/internal-squads "${body}" "${response}"
  SQUAD_UUID="$(jq -er '.response.uuid' "${response}")"
}

create_host() {
  local body="${STATE_DIR}/create-host.json" response="${STATE_DIR}/host-response.json"
  jq -n     --arg profile "${PROFILE_UUID}"     --arg inbound "${INBOUND_UUID}"     --arg node "${NODE_UUID}"     --arg squad "${SQUAD_UUID}"     --argjson port "${XRAY_PORT}"     '{
      inbound:{configProfileUuid:$profile,configProfileInboundUuid:$inbound},
      remark:"Podlaz Fixture",
      address:"127.0.0.1",
      port:$port,
      securityLayer:"DEFAULT",
      nodes:[$node],
      internalSquads:{mode:"ALLOW_ONLY",squads:[$squad]}
    }' >"${body}"
  chmod 0600 "${body}"
  api POST /api/hosts "${body}" "${response}"
  jq -e '.response.uuid | strings' "${response}" >/dev/null
}

enable_hwid() {
  local current="${STATE_DIR}/subscription-settings.json" body="${STATE_DIR}/update-subscription-settings.json" response="${STATE_DIR}/subscription-settings-updated.json"
  api GET /api/subscription-settings "" "${current}"
  settings_uuid="$(jq -er '.response.uuid' "${current}")"
  jq -n --arg uuid "${settings_uuid}"     '{uuid:$uuid,hwidSettings:{enabled:true,fallbackDeviceLimit:1,maxDevicesAnnounce:null}}' >"${body}"
  chmod 0600 "${body}"
  api PATCH /api/subscription-settings "${body}" "${response}"
  jq -e '.response.hwidSettings.enabled == true' "${response}" >/dev/null
}

create_user() {
  local body="${STATE_DIR}/create-user.json" response="${STATE_DIR}/user-response.json"
  expires="$(python3 - <<'PY'
from datetime import datetime, timedelta, timezone
print((datetime.now(timezone.utc) + timedelta(hours=2)).isoformat().replace("+00:00", "Z"))
PY
)"
  jq -n     --arg expires "${expires}"     --arg squad "${SQUAD_UUID}"     '{
      username:"podlaz_fixture",
      expireAt:$expires,
      hwidDeviceLimit:1,
      activeInternalSquads:[$squad]
    }' >"${body}"
  chmod 0600 "${body}"
  api POST /api/users "${body}" "${response}"
  USER_ID="$(jq -er '.response.id' "${response}")"
  USER_SHORT_UUID="$(jq -er '.response.shortUuid' "${response}")"
  SUBSCRIPTION_URL="http://127.0.0.1:3000/api/sub/${USER_SHORT_UUID}"
  printf '%s' "${SUBSCRIPTION_URL}" >"${STATE_DIR}/subscription-url"
  chmod 0600 "${STATE_DIR}/subscription-url"
}

assert_hwid_limit() {
  local first_hwid second_hwid first="${STATE_DIR}/subscription-first.out" second="${STATE_DIR}/subscription-second.out" devices="${STATE_DIR}/devices.json"
  first_hwid="$(openssl rand -base64 24 | tr -d '/+' | head -c 24)"
  second_hwid="$(openssl rand -base64 24 | tr -d '/+' | head -c 24)"
  mask_value "${first_hwid}"
  mask_value "${second_hwid}"

  curl -fsS --max-time 20     -H 'user-agent: podlaz'     -H "x-hwid: ${first_hwid}"     "${SUBSCRIPTION_URL}" >"${first}"
  [[ -s "${first}" ]] || fail "first HWID subscription response is empty"

  api GET "/api/hwid/devices/${USER_ID}" "" "${devices}"
  jq -e '.response.total == 1 and (.response.devices | length) == 1' "${devices}" >/dev/null ||     fail "Remnawave did not register exactly one HWID device"

  curl -fsS --max-time 20     -H 'user-agent: podlaz'     -H "x-hwid: ${first_hwid}"     "${SUBSCRIPTION_URL}" >"${first}.refresh"
  [[ -s "${first}.refresh" ]] || fail "same HWID refresh failed"

  status="$(curl -sS -o "${second}" -w '%{http_code}' --max-time 20     -H 'user-agent: podlaz'     -H "x-hwid: ${second_hwid}"     "${SUBSCRIPTION_URL}")"
  [[ "${status}" == 404 ]] || fail "second HWID identity was not rejected by Remnawave: HTTP ${status}"

  api GET "/api/hwid/devices/${USER_ID}" "" "${devices}"
  jq -e '.response.total == 1 and (.response.devices | length) == 1' "${devices}" >/dev/null ||     fail "rejected HWID changed server-side device state"
}

record_image_digests() {
  local image digest
  for image in "${PANEL_IMAGE}" "${NODE_IMAGE}" "${POSTGRES_IMAGE}" "${VALKEY_IMAGE}"; do
    docker pull "${image}" >/dev/null
    digest="$(docker image inspect --format '{{index .RepoDigests 0}}' "${image}")"
    [[ "${digest}" == *@sha256:* ]] || fail "image digest unavailable for ${image}"
    printf 'fixture-image=%s\n' "${digest}"
  done
}

cleanup_fixture() {
  local code=$?
  set +e
  if [[ -f "${COMPOSE_FILE}" ]]; then
    docker compose -f "${COMPOSE_FILE}" down -v --remove-orphans >/dev/null 2>&1 || code=1
  fi
  if docker ps -aq --filter 'label=com.docker.compose.project=remnawave-fixture' | grep -q .; then
    code=1
  fi
  if docker network inspect podlaz-remnawave-fixture >/dev/null 2>&1; then
    code=1
  fi
  if docker volume inspect podlaz-remnawave-db >/dev/null 2>&1; then
    code=1
  fi
  rm -rf "${PRIVATE_ROOT}"
  exit "${code}"
}
trap cleanup_fixture EXIT INT TERM

main() {
  install -d -m 0700 "${PRIVATE_ROOT}" "${STATE_DIR}"
  APP_SECRET="$(openssl rand -hex 64)"
  METRICS_PASS="$(openssl rand -hex 32)"
  WEBHOOK_SECRET="$(openssl rand -hex 32)"
  POSTGRES_PASSWORD="$(openssl rand -hex 24)"
  ADMIN_USER="fixture_$(openssl rand -hex 4)"
  ADMIN_PASSWORD="A$(openssl rand -hex 16)a9Z$(openssl rand -hex 12)"

  for value in "${APP_SECRET}" "${METRICS_PASS}" "${WEBHOOK_SECRET}" "${POSTGRES_PASSWORD}" "${ADMIN_USER}" "${ADMIN_PASSWORD}"; do
    mask_value "${value}"
  done

  write_panel_env
  write_compose

  docker compose -f "${COMPOSE_FILE}" up -d remnawave-db remnawave-redis remnawave >/dev/null
  FIXTURE_STARTED=1
  wait_http "${PANEL_METRICS_URL}" || fail "Remnawave Panel health endpoint did not become ready"
  wait_panel_api || fail "Remnawave Panel API did not become ready for registration"

  register_body="${STATE_DIR}/register.json"
  register_response="${STATE_DIR}/register-response.json"
  jq -n --arg username "${ADMIN_USER}" --arg password "${ADMIN_PASSWORD}"     '{username:$username,password:$password}' >"${register_body}"
  chmod 0600 "${register_body}"
  api POST /api/auth/register "${register_body}" "${register_response}"
  ADMIN_TOKEN="$(jq -er '.response.accessToken' "${register_response}")"
  mask_value "${ADMIN_TOKEN}"

  create_config_profile
  create_node
  start_node
  create_squad
  create_host
  enable_hwid
  create_user
  assert_hwid_limit
  record_image_digests

  printf 'remnawave.fixture=pass\n'
  printf 'remnawave.panel_version=3.4.5\n'
  printf 'remnawave.node_version=3.4.2\n'
  printf 'remnawave.hwid_device_limit=pass\n'
}

main "$@"
