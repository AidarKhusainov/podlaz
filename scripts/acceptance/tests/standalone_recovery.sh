#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
# shellcheck source=/dev/null
source "$ROOT/scripts/acceptance/release-laptop.sh"

fail() { printf 'standalone_recovery: %s\n' "$*" >&2; exit 1; }
assert_eq() { [[ "$1" == "$2" ]] || fail "expected [$2], got [$1]"; }
assert_contains() { grep -Fq -- "$2" <<<"$1" || fail "expected [$1] to contain [$2]"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
export RELEASE_ACCEPTANCE_TEST_MODE=1
export RELEASE_ACCEPTANCE_TEST_HOME="$TMP/home"
export RELEASE_ACCEPTANCE_STATE_HOME="$TMP/state"
export SUDO_USER=tester
mkdir -p "$RELEASE_ACCEPTANCE_TEST_HOME"
RA_USER=tester; RA_UID="$(id -u)"; RA_GID="$(id -g)"; RA_HOME="$RELEASE_ACCEPTANCE_TEST_HOME"; RA_ARTIFACT_DIR="$TMP/artifacts"
ra_init_paths
ra_artifacts_init_new test-run

candidate="$(jq -cn --arg p "$TMP/candidate.deb" '{path:$p,package:"podlaz",version:"2.0",architecture:"amd64",sha256:"abc",device:1,inode:2}')"
manifest='{"enabled":false}'
ra_state_init test-run "$candidate" "1.0" "$manifest" profile-a

ra_mut_begin_acquire fixture_a network_fixture '{"name":"fixture_a"}'
assert_eq "$(jq -r '.mutations.fixture_a.state' "$RA_CHECKPOINT")" acquiring
ra_mut_mark_acquired fixture_a
assert_eq "$(jq -r '.mutations.fixture_a.state' "$RA_CHECKPOINT")" acquired
ra_mut_begin_release fixture_a
assert_eq "$(jq -r '.mutations.fixture_a.state' "$RA_CHECKPOINT")" releasing
ra_mut_mark_released fixture_a
assert_eq "$(jq -r '.mutations.fixture_a.state' "$RA_CHECKPOINT")" released

# Reset fixture authority to an interrupted acquisition and prove exact partial cleanup.
spec="$(ra_fixture_spec fixture_a)"
identity="$(jq -c '. + {current_route:.route}' <<<"$spec")"
ra_state_jq '.mutations.fixture_a={state:"acquiring",kind:"network_fixture",identity:$i}' --argjson i "$identity"
COMMANDS=""
ra_capture() {
  local joined; printf -v joined '%q ' "$@"; COMMANDS+="$joined"$'\n'
  case "$joined" in
    *"ip -j -d link show dev podlaz-accept-a0 "*) RA_CAPTURE='[{"ifname":"podlaz-accept-a0","linkinfo":{"info_kind":"tun"}}]'; return 0 ;;
    *"ip -j -d link show dev podlaz-accept-adns0 "*) RA_CAPTURE=''; return 1 ;;
    *"ip -j -4 route show table 51820 "*) RA_CAPTURE='[{"type":"blackhole","dst":"198.51.100.254/32","table":51820}]'; return 0 ;;
    *"ip -4 rule show priority 9999 "*) RA_CAPTURE='9999: from all to 198.51.100.254/32 lookup 51820'; return 0 ;;
    *"ip -4 rule show priority 10000 "*) RA_CAPTURE=''; return 1 ;;
    *"nft -j list table inet podlaz_accept_a "*) RA_CAPTURE=''; return 1 ;;
    *) RA_CAPTURE=''; return 0 ;;
  esac
}
ra_fixture_release_partial fixture_a
assert_eq "$(jq -r '.mutations.fixture_a.state' "$RA_CHECKPOINT")" released
assert_contains "$COMMANDS" "ip -4 rule del priority 9999"
assert_contains "$COMMANDS" "ip -4 route del blackhole 198.51.100.254/32 table 51820"
assert_contains "$COMMANDS" "ip link del dev podlaz-accept-a0"
if grep -Fq 'ip link del dev podlaz-accept-adns0' <<<"$COMMANDS"; then fail "absent DNS link was deleted"; fi

# Foreign link kind must fail closed and issue no delete.
ra_state_jq '.mutations.fixture_a={state:"acquiring",kind:"network_fixture",identity:$i}' --argjson i "$identity"
COMMANDS=""
ra_capture() {
  local joined; printf -v joined '%q ' "$@"; COMMANDS+="$joined"$'\n'
  case "$joined" in
    *"ip -j -d link show dev podlaz-accept-a0 "*) RA_CAPTURE='[{"ifname":"podlaz-accept-a0","linkinfo":{"info_kind":"dummy"}}]'; return 0 ;;
    *"ip -j -d link show dev podlaz-accept-adns0 "*) RA_CAPTURE=''; return 1 ;;
    *"ip -j -4 route show table 51820 "*) RA_CAPTURE='[]'; return 0 ;;
    *"ip -4 rule show priority "*) RA_CAPTURE=''; return 1 ;;
    *"nft -j list table inet podlaz_accept_a "*) RA_CAPTURE=''; return 1 ;;
    *) RA_CAPTURE=''; return 0 ;;
  esac
}
if ra_fixture_release_partial fixture_a >/dev/null 2>&1; then fail "foreign link kind was accepted"; fi
assert_eq "$(jq -r '.mutations.fixture_a.state' "$RA_CHECKPOINT")" acquiring
if grep -Fq 'ip link del dev podlaz-accept-a0' <<<"$COMMANDS"; then fail "foreign link was deleted"; fi

# Interrupted release uses the same exact observation path and converges to released.
ra_state_jq '.mutations.fixture_a={state:"releasing",kind:"network_fixture",identity:$i}' --argjson i "$identity"
COMMANDS=""
ra_capture() {
  local joined; printf -v joined '%q ' "$@"; COMMANDS+="$joined"$'\n'
  case "$joined" in
    *"ip -j -d link show dev podlaz-accept-a0 "*) RA_CAPTURE=''; return 1 ;;
    *"ip -j -d link show dev podlaz-accept-adns0 "*) RA_CAPTURE=''; return 1 ;;
    *"ip -j -4 route show table 51820 "*) RA_CAPTURE='[]'; return 0 ;;
    *"ip -4 rule show priority "*) RA_CAPTURE=''; return 1 ;;
    *"nft -j list table inet podlaz_accept_a "*) RA_CAPTURE=''; return 1 ;;
    *) RA_CAPTURE=''; return 0 ;;
  esac
}
ra_fixture_release fixture_a
assert_eq "$(jq -r '.mutations.fixture_a.state' "$RA_CHECKPOINT")" released

printf 'standalone_recovery: PASS\n'
