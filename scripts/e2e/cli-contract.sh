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
parent_pid="${PPID}"
printf '%s\n' "$@" >>"${PODLAZ_FAKE_JOURNALCTL_ARGS:?}"
printf 'podlazd.service: fake follow line\n'
while kill -0 "${parent_pid}" 2>/dev/null; do
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
    "${PODLAZ[@]}" debug logs "$@"
  assert_contains "${LAST_STDOUT}" "podlaz daemon logs"
  assert_contains "${LAST_STDOUT}" "podlazd.service: fake follow line"
  assert_contains "${FAKE_JOURNALCTL_ARGS}" "--follow"
}

printf '%s\nhysteria2://unsupported.example\n' "${LOCAL_URI}" >"${FIXTURES}/profiles.txt"
printf '%s\n' "${LOCAL_B64_URI}" | base64 -w0 >"${FIXTURES}/profiles.base64"
printf '%s\n' "${SUB_URI}" | base64 -w0 >"${FIXTURES}/subscription.txt"
printf '{"outbounds":' >"${FIXTURES}/broken.json"
printf '%s\n%s\n' "${LOCAL_URI}" "${LOCAL_URI}" >"${FIXTURES}/duplicates.txt"

log "primary help and version"
expect_success root-help "${PODLAZ[@]}" --help
for want in "podlaz import" "podlaz connect" "podlaz status" "podlaz disconnect" "podlaz debug"; do
  assert_contains "${LAST_STDOUT}" "${want}"
done
for forbidden in "podlaz plan" "podlaz check" "podlaz recover" "podlaz doctor" "podlaz logs" "--mode" "--handoff"; do
  assert_not_contains "${LAST_STDOUT}" "${forbidden}"
done
expect_success help "${PODLAZ[@]}" help
expect_success version "${PODLAZ[@]}" version
expect_success version-help "${PODLAZ[@]}" version --help
expect_exit 2 version-extra "${PODLAZ[@]}" version extra
expect_exit 2 unknown-command "${PODLAZ[@]}" definitely-not-a-command

for command in profile subscription import connect disconnect status autostart debug completion; do
  expect_success "help-${command}" "${PODLAZ[@]}" help "${command}"
done
for removed in plan check doctor logs recover; do
  expect_exit 2 "removed-help-${removed}" "${PODLAZ[@]}" help "${removed}"
  expect_exit 2 "removed-command-${removed}" "${PODLAZ[@]}" "${removed}" --help
done

log "completion command"
for shell in bash zsh fish; do
  expect_success "completion-${shell}" "${PODLAZ[@]}" completion "${shell}"
done
expect_success completion-help "${PODLAZ[@]}" completion --help
expect_exit 2 completion-unsupported-shell "${PODLAZ[@]}" completion powershell

log "canonical import and profile selection"
expect_success import-share "${PODLAZ[@]}" import "${VALID_PROFILE_URI}"
assert_contains "${LAST_STDOUT}" "Imported 1 profile"
assert_contains "${LAST_STDOUT}" "Profile: e2e-valid"
assert_contains "${LAST_STDOUT}" "Next: podlaz connect"
assert_not_contains "${LAST_STDOUT}" "00000000-0000-0000-0000-000000000002"

expect_success profile-list "${PODLAZ[@]}" profile list
assert_contains "${LAST_STDOUT}" "e2e-valid"
assert_contains "${LAST_STDOUT}" "*"
assert_not_contains "${LAST_STDOUT}" "example.net"
assert_not_contains "${LAST_STDOUT}" "00000000-0000-0000-0000-000000000002"

expect_success profile-show-name "${PODLAZ[@]}" profile show E2E-VALID
assert_contains "${LAST_STDOUT}" "Name: e2e-valid"
assert_not_contains "${LAST_STDOUT}" "00000000-0000-0000-0000-000000000002"
expect_success profile-use "${PODLAZ[@]}" profile use e2e-valid
assert_contains "${LAST_STDOUT}" "Selected profile: e2e-valid"

for args in \
  "profile add --name old" \
  "profile import ${VALID_PROFILE_URI}" \
  "profile validate e2e-valid" \
  "profile list --json" \
  "profile show e2e-valid --json"; do
  # shellcheck disable=SC2086
  expect_exit 2 "removed-${args// /-}" "${PODLAZ[@]}" ${args}
done

log "unified local import"
expect_success import-local-uri-list "${PODLAZ[@]}" import "${FIXTURES}/profiles.txt"
assert_contains "${LAST_STDOUT}" "Imported 1 profile"
assert_contains "${LAST_STDOUT}" "Skipped unsupported entries: 1"
expect_success import-local-base64 "${PODLAZ[@]}" import "${FIXTURES}/profiles.base64"
assert_contains "${LAST_STDOUT}" "Imported 1 profile"
expect_exit 2 import-malformed-json "${PODLAZ[@]}" import "${FIXTURES}/broken.json"
expect_exit 2 import-duplicate-atomic "${PODLAZ[@]}" import "${FIXTURES}/duplicates.txt"
expect_exit 2 import-json-removed "${PODLAZ[@]}" import --json "${FIXTURES}/profiles.txt"

log "subscription management after canonical import"
SUB_URL="file://${FIXTURES}/subscription.txt"
expect_success import-subscription "${PODLAZ[@]}" import "${SUB_URL}"
assert_contains "${LAST_STDOUT}" "Subscription imported"
assert_contains "${LAST_STDOUT}" "Profiles: 1"
assert_not_contains "${LAST_STDOUT}" "${SUB_URL}"
assert_not_contains "${LAST_STDOUT}" "00000000-0000-0000-0000-000000000005"

expect_success subscription-list "${PODLAZ[@]}" subscription list
SUB_ID="$(awk 'NR == 2 {print $1; exit}' "${LAST_STDOUT}")"
assert_nonempty "${SUB_ID}" "subscription id"
expect_success subscription-show "${PODLAZ[@]}" subscription show "${SUB_ID}"
assert_not_contains "${LAST_STDOUT}" "URL:"
assert_not_contains "${LAST_STDOUT}" "${SUB_URL}"
expect_success subscription-update "${PODLAZ[@]}" subscription update "${SUB_ID}"
assert_contains "${LAST_STDOUT}" "Subscription updated"
expect_exit 2 subscription-add-removed "${PODLAZ[@]}" subscription add --name fixture-sub --url "${SUB_URL}"
expect_exit 2 subscription-list-json-removed "${PODLAZ[@]}" subscription list --json
expect_exit 2 subscription-show-json-removed "${PODLAZ[@]}" subscription show "${SUB_ID}" --json
expect_exit 2 subscription-delete-without-yes "${PODLAZ[@]}" subscription delete "${SUB_ID}"
expect_success subscription-delete-keep-profiles "${PODLAZ[@]}" subscription delete "${SUB_ID}" --yes --keep-profiles

log "primary lifecycle argument gates"
expect_success connect-help "${PODLAZ[@]}" connect --help
expect_exit 2 connect-mode-removed "${PODLAZ[@]}" connect --mode tun e2e-valid
expect_exit 2 connect-handoff-removed "${PODLAZ[@]}" connect --handoff replace-podlaz e2e-valid
expect_exit 2 connect-json-removed "${PODLAZ[@]}" connect --json e2e-valid
expect_success disconnect-help "${PODLAZ[@]}" disconnect --help
expect_exit 2 disconnect-unsupported-flag "${PODLAZ[@]}" disconnect --force
expect_success autostart-help "${PODLAZ[@]}" autostart --help
expect_exit 2 autostart-mode-removed "${PODLAZ[@]}" autostart enable --mode tun e2e-valid

log "status and progressive debug surface"
expect_success status-help "${PODLAZ[@]}" status --help
expect_exit_in "0 3 5" status-human "${PODLAZ[@]}" status
expect_exit 2 status-json-removed "${PODLAZ[@]}" status --json

expect_success debug-help "${PODLAZ[@]}" debug --help
for subcommand in doctor logs proxy recover; do
  assert_contains "${LAST_STDOUT}" "debug ${subcommand}"
done
expect_success debug-doctor-help "${PODLAZ[@]}" debug doctor --help
expect_exit_in "0 3" debug-doctor-human "${PODLAZ[@]}" debug doctor
expect_success debug-logs-help "${PODLAZ[@]}" debug logs --help
expect_exit 2 debug-logs-invalid-since "${PODLAZ[@]}" debug logs --since
expect_logs_follow_timeout debug-logs-follow-short -f
expect_logs_follow_timeout debug-logs-follow-long --follow
expect_success debug-recover-help "${PODLAZ[@]}" debug recover --help
expect_exit_in "0 3" debug-recover-dry-run "${PODLAZ[@]}" debug recover
expect_exit 2 debug-recover-yes-removed "${PODLAZ[@]}" debug recover --yes

log "destructive confirmation remains explicit"
expect_exit 2 profile-delete-noninteractive "${PODLAZ[@]}" profile delete e2e-valid
expect_success profile-delete-yes "${PODLAZ[@]}" profile delete e2e-valid --yes

log "CLI contract e2e completed"
