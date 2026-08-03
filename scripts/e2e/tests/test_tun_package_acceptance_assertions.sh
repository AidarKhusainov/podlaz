#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib/tun_package_assertions.sh
source "${SCRIPT_DIR}/../lib/tun_package_assertions.sh"

fail_test() {
  printf 'test failure: %s\n' "$*" >&2
  exit 1
}

run_manifest_status_mapping() (
  sudo() { return 0; }
  inspect_exact_network_manifest_state helper manifest || fail_test "clean manifest was not absent"

  sudo() { return 1; }
  if inspect_exact_network_manifest_state helper manifest; then
    fail_test "remaining exact route/rule tuple was treated as absent"
  else
    status=$?
  fi
  [[ "${status}" == "1" ]] || fail_test "manifest residue returned ${status}"

  sudo() { return 2; }
  if inspect_exact_network_manifest_state helper manifest; then
    fail_test "manifest inspection error was treated as absent"
  else
    status=$?
  fi
  [[ "${status}" == "2" ]] || fail_test "manifest inspection error returned ${status}"
)

run_tun_address_status_mapping() (
  sudo() { printf '7: podlaz0    inet 198.18.0.1/32 scope global podlaz0\n'; return 0; }
  if inspect_tun_package_address_state podlaz0 198.18.0.1/32; then
    fail_test "exact daemon-owned TUN address was treated as absent"
  else
    status=$?
  fi
  [[ "${status}" == "1" ]] || fail_test "exact TUN address returned ${status}"

  sudo() { return 0; }
  inspect_tun_package_address_state podlaz0 198.18.0.1/32 || fail_test "empty TUN address inventory was not absent"

  sudo() { printf '7: podlaz0    inet 198.18.0.2/32 scope global podlaz0\n'; return 0; }
  if inspect_tun_package_address_state podlaz0 198.18.0.1/32; then
    fail_test "foreign TUN address was treated as absent"
  else
    status=$?
  fi
  [[ "${status}" == "2" ]] || fail_test "foreign TUN address returned ${status}"

  sudo() { return 2; }
  inspect_link_state() { return 0; }
  inspect_tun_package_address_state podlaz0 198.18.0.1/32 || fail_test "missing link was not converged address absence"

  inspect_link_state() { return 1; }
  if inspect_tun_package_address_state podlaz0 198.18.0.1/32; then
    fail_test "address inspection error on an existing link was treated as absent"
  else
    status=$?
  fi
  [[ "${status}" == "2" ]] || fail_test "address inspection error returned ${status}"
)

run_xray_status_mapping() (
  sudo() { return 1; }
  inspect_tun_package_xray_state || fail_test "pgrep no-match was not absence"

  sudo() { return 2; }
  if inspect_tun_package_xray_state; then
    fail_test "pgrep operational error was treated as absence"
  else
    status=$?
  fi
  [[ "${status}" == "2" ]] || fail_test "pgrep error returned ${status}"

  sudo() { printf '123\n'; return 0; }
  if inspect_tun_package_xray_state; then
    fail_test "live packaged Xray was treated as absent"
  else
    status=$?
  fi
  [[ "${status}" == "1" ]] || fail_test "live Xray returned ${status}"
)

install_clean_baseline() {
  inspect_exact_network_manifest_state() { return 0; }
  inspect_tun_package_address_state() { return 0; }
  inspect_link_state() { return 0; }
  inspect_resolved_link_state() { return 0; }
  inspect_nft_table_state() { return 0; }
  inspect_tun_package_xray_state() { return 0; }
  inspect_directory_content_state() { return 0; }
}

run_resource_inspection_failure_case() (
  local inspector="$1"
  install_clean_baseline
  case "${inspector}" in
    network) inspect_exact_network_manifest_state() { return 2; } ;;
    address) inspect_tun_package_address_state() { return 2; } ;;
    link) inspect_link_state() { return 2; } ;;
    resolved) inspect_resolved_link_state() { return 2; } ;;
    nftables) inspect_nft_table_state() { return 2; } ;;
    xray) inspect_tun_package_xray_state() { return 2; } ;;
    generated) inspect_directory_content_state() { [[ "$1" == *generated ]] && return 2; return 0; } ;;
    transactions) inspect_directory_content_state() { [[ "$1" == *transactions ]] && return 2; return 0; } ;;
    *) fail_test "unknown inspector ${inspector}" ;;
  esac
  if verify_tun_package_resources_absent test helper manifest; then
    fail_test "${inspector} operational error produced resources-absent success"
  fi
)

run_manifest_status_mapping
run_tun_address_status_mapping
run_xray_status_mapping

install_clean_baseline
verify_tun_package_resources_absent clean helper manifest || fail_test "clean resource state failed"

for inspector in network address link resolved nftables xray generated transactions; do
  run_resource_inspection_failure_case "${inspector}"
done

install_clean_baseline
inspect_exact_network_manifest_state() { return 1; }
if verify_tun_package_resources_absent residue helper manifest; then
  fail_test "exact main-table or managed-table residue produced success"
fi

printf 'tun package acceptance assertion tests passed\n'
