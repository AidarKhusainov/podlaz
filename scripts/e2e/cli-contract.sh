#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd bash go python3 base64 grep awk sed mktemp timeout
build_podlaz_binary
setup_isolated_xdg "cli-contract"

PODLAZ=("${PODLAZ_BIN}")
FIXTURES="${E2E_HOME}/fixtures"
mkdir -p "${FIXTURES}"

VALID_PROFILE_URI='vless://00000000-0000-0000-0000-000000000002@example.net:443?type=tcp&security=reality&encryption=none&flow=xtls-rprx-vision&sni=www.example.net&fp=chrome&pbk=public-key&sid=abcd&spx=%2F#e2e-valid'
LOCAL_URI='vless://00000000-0000-0000-0000-000000000003@uri.example.com:443?type=tcp&security=tls&encryption=none#plain-cli'
LOCAL_B64_URI='vless://00000000-0000-0000-0000-000000000004@base64.example.com:443?type=tcp&security=tls&encryption=none#base64-cli'
SUB_URI='vless://00000000-0000-0000-0000-000000000005@subscription.example.com:443?type=tcp&security=tls&encryption=none#sub-cli'

FAKE_JOURNALCTL_DIR="${E2E_HOME}/fake-journalctl-bin"
FAKE_JOURNALCTL_ARGS="${E2E_HOME}/fake-journalctl.args"
mkdir -p "${FAKE_JOURNALCTL_DIR}"
cat >"${FAKE_JOURNALCTL_DIR}/journalctl" <<'SH'
#!/usr/bin/env bash
printf '%s\n' "$@" >>"${PODLAZ_FAKE_JOURNALCTL_ARGS:?}"
printf 'podlazd.service: fake follow line\n'
while :; do
  sleep 1
done
SH
chmod +x "${FAKE_JOURNALCTL_DIR}/journalctl"

expect_logs_follow_timeout() {
  local name="$1"
  shift
  : >"${FAKE_JOURNALCTL_ARGS}"
  expect_exit 124 "${name}" timeout --kill-after=2 3 env \
    "PATH=${FAKE_JOURNALCTL_DIR}:${PATH}" \
    "PODLAZ_FAKE_JOURNALCTL_ARGS=${FAKE_JOURNALCTL_ARGS}" \
    "${PODLAZ[@]}" logs "$@"
  assert_contains "${LAST_STDOUT}" "podlaz daemon logs"
  assert_contains "${LAST_STDOUT}" "podlazd.service: fake follow line"
  assert_contains "${FAKE_JOURNALCTL_ARGS}" "--follow"
}

cat >"${FIXTURES}/xray-vless.json" <<'JSON'
{
  "outbounds": [
    {
      "tag": "json-cli",
      "protocol": "vless",
      "settings": {
        "vnext": [
          {
            "address": "json.example.com",
            "port": 443,
            "users": [
              {"id": "00000000-0000-0000-0000-000000000006", "encryption": "none", "flow": "xtls-rprx-vision"}
            ]
          }
        ]
      },
      "streamSettings": {
        "network": "tcp",
        "security": "reality",
        "realitySettings": {
          "serverName": "json.example.com",
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
printf '%s\nhysteria2://unsupported.example\n' "${LOCAL_URI}" >"${FIXTURES}/profiles.txt"
printf '%s\n' "${LOCAL_B64_URI}" | base64 -w0 >"${FIXTURES}/profiles.base64"
printf '%s\n' "${SUB_URI}" | base64 -w0 >"${FIXTURES}/subscription.txt"
printf '{"outbounds":' >"${FIXTURES}/broken.json"
printf '%s\n%s\n' "${LOCAL_URI}" "${LOCAL_URI}" >"${FIXTURES}/duplicates.txt"

log "global help and version"
expect_success root-help "${PODLAZ[@]}" --help
expect_success help "${PODLAZ[@]}" help
expect_success version "${PODLAZ[@]}" version
expect_success version-help "${PODLAZ[@]}" version --help
expect_exit 2 version-extra "${PODLAZ[@]}" version extra
expect_exit 2 unknown-command "${PODLAZ[@]}" definitely-not-a-command

for command in profile subscription import plan connect disconnect status doctor logs recover completion; do
  expect_success "help-${command}" "${PODLAZ[@]}" help "${command}"
done

log "completion command"
expect_success completion-bash "${PODLAZ[@]}" completion bash
expect_success completion-zsh "${PODLAZ[@]}" completion zsh
expect_success completion-fish "${PODLAZ[@]}" completion fish
expect_success completion-bash-help "${PODLAZ[@]}" completion --help
expect_exit 2 completion-unsupported-shell "${PODLAZ[@]}" completion powershell

log "profile command"
expect_success profile-help-long "${PODLAZ[@]}" profile --help
expect_success profile-help-short "${PODLAZ[@]}" profile -h
expect_success profile-add-manual "${PODLAZ[@]}" profile add --name manual-vless --server example.com --port 443 --protocol vless
assert_contains "${LAST_STDOUT}" "Profile added: manual-vless"
expect_success profile-add-inline-flags "${PODLAZ[@]}" profile add --name=manual-vmess --server=vmess.example.com --port=443 --protocol=vmess
expect_exit 2 profile-add-json-deferred "${PODLAZ[@]}" profile add --json --name bad --server example.com --port 443 --protocol vless
expect_exit 2 profile-add-invalid-port "${PODLAZ[@]}" profile add --name bad-port --server example.com --port 0 --protocol vless
expect_exit 2 profile-add-invalid-protocol "${PODLAZ[@]}" profile add --name bad-protocol --server example.com --port 443 --protocol hysteria2
expect_success profile-list "${PODLAZ[@]}" profile list
assert_contains "${LAST_STDOUT}" "manual-vless"
expect_success profile-list-json "${PODLAZ[@]}" profile list --json
assert_json_file "${LAST_STDOUT}"
expect_success profile-show "${PODLAZ[@]}" profile show manual-vless
assert_contains "${LAST_STDOUT}" "Name: manual-vless"
expect_success profile-show-json "${PODLAZ[@]}" profile show manual-vless --json
assert_json_file "${LAST_STDOUT}"
expect_exit 3 profile-validate-manual-not-renderable "${PODLAZ[@]}" profile validate manual-vless
expect_exit 3 profile-validate-manual-tun-not-renderable "${PODLAZ[@]}" profile validate manual-vless --mode tun
expect_exit 2 profile-validate-invalid-mode "${PODLAZ[@]}" profile validate manual-vless --mode wireguard
expect_exit 1 profile-validate-missing-profile "${PODLAZ[@]}" profile validate missing-profile
expect_exit 2 profile-delete-without-yes "${PODLAZ[@]}" profile delete manual-vmess
expect_exit 2 profile-delete-json-deferred "${PODLAZ[@]}" profile delete manual-vmess --json --yes
expect_success profile-delete "${PODLAZ[@]}" profile delete manual-vmess --yes

expect_success profile-import-valid "${PODLAZ[@]}" profile import "${VALID_PROFILE_URI}"
PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${LAST_STDOUT}")"
assert_nonempty "${PROFILE_ID}" "imported profile id"
assert_not_contains "${LAST_STDOUT}" "00000000-0000-0000-0000-000000000002"
expect_exit 2 profile-import-json-deferred "${PODLAZ[@]}" profile import --json "${VALID_PROFILE_URI}"
expect_success profile-validate-imported "${PODLAZ[@]}" profile validate "${PROFILE_ID}"
expect_success profile-validate-imported-json "${PODLAZ[@]}" profile validate "${PROFILE_ID}" --json
assert_json_file "${LAST_STDOUT}"
expect_success profile-validate-imported-tun "${PODLAZ[@]}" profile validate "${PROFILE_ID}" --mode tun

log "import convenience command"
expect_success import-help "${PODLAZ[@]}" import --help
expect_success import-local-xray-json "${PODLAZ[@]}" import "${FIXTURES}/xray-vless.json"
assert_contains "${LAST_STDOUT}" "Format: xray-json"
assert_not_contains "${LAST_STDOUT}" "00000000-0000-0000-0000-000000000006"
expect_success import-local-uri-list "${PODLAZ[@]}" import "${FIXTURES}/profiles.txt"
assert_contains "${LAST_STDOUT}" "Format: uri-list"
assert_contains "${LAST_STDOUT}" "Skipped: 1"
expect_success import-local-base64-uri-list "${PODLAZ[@]}" import "${FIXTURES}/profiles.base64"
assert_contains "${LAST_STDOUT}" "Format: base64-uri-list"
expect_exit 2 import-malformed-json "${PODLAZ[@]}" import "${FIXTURES}/broken.json"
expect_exit 2 import-duplicate-atomic "${PODLAZ[@]}" import "${FIXTURES}/duplicates.txt"
expect_exit 2 import-json-deferred "${PODLAZ[@]}" import --json "${FIXTURES}/profiles.txt"

log "subscription command"
SUB_URL="file://${FIXTURES}/subscription.txt"
expect_success subscription-help "${PODLAZ[@]}" subscription --help
expect_success subscription-add "${PODLAZ[@]}" subscription add --name fixture-sub --url "${SUB_URL}"
SUB_ID="$(awk '/^Subscription added:/ {print $3}' "${LAST_STDOUT}")"
assert_nonempty "${SUB_ID}" "subscription id"
expect_exit 2 subscription-add-json-deferred "${PODLAZ[@]}" subscription add --json --name bad-sub --url "${SUB_URL}"
expect_success subscription-list "${PODLAZ[@]}" subscription list
assert_contains "${LAST_STDOUT}" "fixture-sub"
expect_success subscription-list-json "${PODLAZ[@]}" subscription list --json
assert_json_file "${LAST_STDOUT}"
expect_success subscription-show "${PODLAZ[@]}" subscription show "${SUB_ID}"
assert_contains "${LAST_STDOUT}" "URL: REDACTED"
expect_success subscription-show-json "${PODLAZ[@]}" subscription show "${SUB_ID}" --json
assert_json_file "${LAST_STDOUT}"
expect_success subscription-update "${PODLAZ[@]}" subscription update "${SUB_ID}"
assert_contains "${LAST_STDOUT}" "Subscription updated"
assert_not_contains "${LAST_STDOUT}" "00000000-0000-0000-0000-000000000005"
expect_exit 2 subscription-update-json-deferred "${PODLAZ[@]}" subscription update "${SUB_ID}" --json
expect_exit 2 subscription-delete-without-yes "${PODLAZ[@]}" subscription delete "${SUB_ID}"
expect_exit 2 subscription-delete-json-deferred "${PODLAZ[@]}" subscription delete "${SUB_ID}" --json --yes
expect_success subscription-delete-keep-profiles "${PODLAZ[@]}" subscription delete "${SUB_ID}" --yes --keep-profiles

log "plan command"
expect_success plan-help "${PODLAZ[@]}" plan --help
expect_success plan-proxy-only "${PODLAZ[@]}" plan --mode proxy-only "${PROFILE_ID}"
assert_contains "${LAST_STDOUT}" "Proxy-only plan"
expect_success plan-proxy-only-json "${PODLAZ[@]}" plan --mode=proxy-only "${PROFILE_ID}" --json
assert_json_file "${LAST_STDOUT}"
expect_success plan-tun "${PODLAZ[@]}" plan --mode tun "${PROFILE_ID}"
assert_contains "${LAST_STDOUT}" "podlaz TUN plan"
assert_contains "${LAST_STDOUT}" "No changes were applied."
expect_success plan-tun-json "${PODLAZ[@]}" plan --mode=tun "${PROFILE_ID}" --json
assert_json_file "${LAST_STDOUT}"
expect_exit 2 plan-missing-mode "${PODLAZ[@]}" plan "${PROFILE_ID}"
expect_exit 2 plan-invalid-mode "${PODLAZ[@]}" plan --mode wireguard "${PROFILE_ID}"
expect_exit 1 plan-missing-profile "${PODLAZ[@]}" plan --mode proxy-only missing-profile

log "daemon-backed command argument gates"
expect_success connect-help "${PODLAZ[@]}" connect --help
expect_exit 2 connect-json-deferred "${PODLAZ[@]}" connect --json "${PROFILE_ID}"
expect_exit 2 connect-invalid-mode "${PODLAZ[@]}" connect --mode wireguard "${PROFILE_ID}"
expect_exit 2 connect-missing-profile-arg "${PODLAZ[@]}" connect --mode proxy-only
expect_success disconnect-help "${PODLAZ[@]}" disconnect --help
expect_exit 2 disconnect-json-deferred "${PODLAZ[@]}" disconnect --json
expect_exit 2 disconnect-unsupported-flag "${PODLAZ[@]}" disconnect --force

log "status, doctor, logs, and recover"
expect_success status-help "${PODLAZ[@]}" status --help
expect_exit_in "0 3 5" status-human "${PODLAZ[@]}" status
expect_exit 2 status-json-deferred "${PODLAZ[@]}" status --json
expect_success doctor-help "${PODLAZ[@]}" doctor --help
expect_exit_in "0 3" doctor-human "${PODLAZ[@]}" doctor
expect_exit 2 doctor-json-deferred "${PODLAZ[@]}" doctor --json
expect_exit 2 doctor-core-without-xray "${PODLAZ[@]}" doctor --core
expect_exit 2 doctor-scope-network "${PODLAZ[@]}" doctor --network
expect_exit 2 doctor-scope-dns "${PODLAZ[@]}" doctor --dns
expect_exit 2 doctor-scope-routes "${PODLAZ[@]}" doctor --routes
expect_exit 2 doctor-scope-firewall "${PODLAZ[@]}" doctor --firewall
expect_exit_in "0 3" doctor-core-xray-json-shape "${PODLAZ[@]}" doctor --core --xray "${PODLAZ_BIN}" --json
assert_json_file "${LAST_STDOUT}"
expect_success logs-help "${PODLAZ[@]}" logs --help
expect_exit 2 logs-json-deferred "${PODLAZ[@]}" logs --json
expect_exit 2 logs-invalid-since "${PODLAZ[@]}" logs --since
expect_logs_follow_timeout logs-follow-short-bounded -f
expect_logs_follow_timeout logs-follow-long-bounded --follow
expect_success recover-help "${PODLAZ[@]}" recover --help
expect_exit_in "0 3" recover-dry-run "${PODLAZ[@]}" recover
expect_exit 2 recover-execute-without-yes "${PODLAZ[@]}" recover --execute

log "CLI contract e2e completed"
