#!/usr/bin/env bash

: "${FOREIGN_NFT_FAMILY:=inet}"
: "${FOREIGN_NFT_TABLE:=podlaz_e2e_foreign_guard}"
: "${FOREIGN_ROUTE_TABLE:=42424}"
: "${FOREIGN_ROUTE_CIDR:=198.51.100.254/32}"
: "${FOREIGN_RULE_PRIORITY:=42424}"
: "${FOREIGN_DNS_LINK:=podlaz-e2e-dns0}"
: "${FOREIGN_DNS_SERVER:=192.0.2.53}"
: "${FOREIGN_DNS_DOMAIN:=~e2e.invalid}"
: "${FOREIGN_SERVICE:=podlaz-e2e-foreign.service}"

_tun_foreign_rule_present() {
  sudo -n ip -4 rule show 2>/dev/null | \
    grep -F "${FOREIGN_RULE_PRIORITY}:" | \
    grep -F "to ${FOREIGN_ROUTE_CIDR%/32}" | \
    grep -E "lookup (${FOREIGN_ROUTE_TABLE})([[:space:]]|$)" >/dev/null
}

_tun_foreign_route_present() {
  sudo -n ip -4 route show table "${FOREIGN_ROUTE_TABLE}" exact "${FOREIGN_ROUTE_CIDR}" 2>/dev/null | \
    grep -F "blackhole ${FOREIGN_ROUTE_CIDR%/32}" >/dev/null
}

assert_tun_foreign_state_absent_before_create() {
  if sudo -n nft list table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || \
    _tun_foreign_rule_present || _tun_foreign_route_present || \
    sudo -n ip link show dev "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || \
    systemctl is-active --quiet "${FOREIGN_SERVICE}"; then
    fail "E2E foreign-state sentinel residue exists before setup"
  fi
}

create_tun_foreign_state() {
  assert_tun_foreign_state_absent_before_create
  sudo -n nft add table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}"
  sudo -n ip -4 route add blackhole "${FOREIGN_ROUTE_CIDR}" table "${FOREIGN_ROUTE_TABLE}"
  sudo -n ip -4 rule add priority "${FOREIGN_RULE_PRIORITY}" to "${FOREIGN_ROUTE_CIDR}" lookup "${FOREIGN_ROUTE_TABLE}"
  sudo -n ip link add "${FOREIGN_DNS_LINK}" type dummy
  sudo -n ip link set dev "${FOREIGN_DNS_LINK}" up
  sudo -n resolvectl dns "${FOREIGN_DNS_LINK}" "${FOREIGN_DNS_SERVER}"
  sudo -n resolvectl domain "${FOREIGN_DNS_LINK}" "${FOREIGN_DNS_DOMAIN}"
  sudo -n resolvectl default-route "${FOREIGN_DNS_LINK}" no
  sudo -n systemd-run --unit="${FOREIGN_SERVICE%.service}" --property=Type=simple /bin/sh -c 'exec sleep 600' >/dev/null
}

assert_tun_foreign_state() {
  local phase="$1" tmp
  sudo -n nft list table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || fail "${phase}: unrelated nftables state changed"
  _tun_foreign_route_present || fail "${phase}: unrelated route changed"
  _tun_foreign_rule_present || fail "${phase}: unrelated policy rule changed"
  tmp="$(mktemp)" || fail "${phase}: cannot create foreign-state observation file"
  if ! sudo -n resolvectl status "${FOREIGN_DNS_LINK}" --no-pager >"${tmp}"; then
    rm -f -- "${tmp}"
    fail "${phase}: unrelated resolver link cannot be inspected"
  fi
  grep -F "${FOREIGN_DNS_SERVER}" "${tmp}" >/dev/null || {
    rm -f -- "${tmp}"
    fail "${phase}: unrelated DNS server changed"
  }
  grep -F "${FOREIGN_DNS_DOMAIN}" "${tmp}" >/dev/null || {
    rm -f -- "${tmp}"
    fail "${phase}: unrelated DNS domain changed"
  }
  rm -f -- "${tmp}"
  sudo -n systemctl is-active --quiet "${FOREIGN_SERVICE}" || fail "${phase}: unrelated service changed"
}

cleanup_tun_foreign_state() {
  local status=0
  sudo -n systemctl stop "${FOREIGN_SERVICE}" >/dev/null 2>&1 || true
  sudo -n systemctl reset-failed "${FOREIGN_SERVICE}" >/dev/null 2>&1 || true
  sudo -n resolvectl revert "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || true
  sudo -n ip link del dev "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || true
  sudo -n ip -4 rule del priority "${FOREIGN_RULE_PRIORITY}" to "${FOREIGN_ROUTE_CIDR}" lookup "${FOREIGN_ROUTE_TABLE}" >/dev/null 2>&1 || true
  sudo -n ip -4 route del blackhole "${FOREIGN_ROUTE_CIDR}" table "${FOREIGN_ROUTE_TABLE}" >/dev/null 2>&1 || true
  sudo -n nft delete table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || true
  sudo -n systemctl daemon-reload >/dev/null 2>&1 || status=1
  return "${status}"
}
