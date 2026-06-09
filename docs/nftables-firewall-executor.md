# nftables Firewall Executor

This document describes the implemented nftables executor slice for TUN transactions.

The canonical networking invariants remain owned by [Networking and Reliability Requirements](./networking-reliability.md). This document records the concrete executor behavior added for TunWarden-owned firewall state.

## Ownership boundary

TunWarden creates and mutates only this nftables object tree:

```text
table inet tunwarden
```

TunWarden must not mutate user-owned nftables tables, chains, or rules outside `inet tunwarden`.

The executor enforces this boundary itself. It rejects every apply, verify, or rollback target except:

```text
family = inet
table  = tunwarden
```

This check is intentionally duplicated in the privileged executor instead of trusting planner output or transaction rollback metadata. Corrupt or stale metadata must fail closed and must not cause deletion of arbitrary user firewall state.

The initial executor owns one output chain inside that table:

```text
chain output {
  type filter hook output priority 0; policy accept;
}
```

Rules are marked with explicit TunWarden comments:

```text
tunwarden:firewall:server-bypass
tunwarden:firewall:tun-egress
tunwarden:firewall:kill-switch
```

These markers are part of the inspectable desired state and the verification contract.

## Planned rules

For a concrete VPN server IP, the executor installs the planner-provided server bypass rule before non-TUN blocking:

```text
ip daddr <server-ip> counter comment "tunwarden:firewall:server-bypass" accept
```

TUN egress is allowed explicitly:

```text
oifname "tunwarden0" counter comment "tunwarden:firewall:tun-egress" accept
```

The kill-switch rule depends on the selected planner policy:

```text
oifname != "tunwarden0" counter comment "tunwarden:firewall:kill-switch" reject  # soft
oifname != "tunwarden0" counter comment "tunwarden:firewall:kill-switch" drop    # strict
```

For `off`, the kill-switch blocking rule is skipped.

## Apply order

The daemon transaction applies networking state in this order:

1. TUN interface, routes, and policy rules.
2. systemd-resolved per-link DNS.
3. TunWarden-owned nftables table, chain, and rules.
4. Verification before transaction commit.

If nftables apply fails after creating partial state, the executor removes `inet tunwarden` immediately. The transaction then rolls back the already-recorded DNS, route, policy-rule, and TUN steps.

## Verification

Before commit, the executor runs:

```bash
nft list table inet tunwarden
```

Verification requires:

- the planned chain to be visible;
- every planned rule to appear on one nft output line with the expected expression, `counter comment`, TunWarden owner marker, and verdict in order.

This avoids accepting output where the expression, owner marker, and verdict appear on different rules.

A failed verification triggers transaction rollback.

## Rollback and disconnect cleanup

Rollback deletes the whole TunWarden-owned table:

```bash
nft delete table inet tunwarden
```

This is intentionally idempotent. Missing-table errors are treated as already-clean state.

Rollback rejects non-owned targets instead of silently no-oping. This keeps corrupted transaction metadata visible and prevents accidental deletion of arbitrary nftables state.

Disconnect uses transaction rollback metadata to remove `inet tunwarden` before DNS, route, policy-rule, and TUN cleanup. Recovery commands that execute cleanup must use the same ownership boundary: remove TunWarden-owned nftables state, not arbitrary firewall state.

## Existing table behavior

If snapshot/planner state reports that `inet tunwarden` already exists before apply, the current executor does not silently validate-or-replace it. The connect transaction fails before firewall mutation so the user can inspect or recover stale TunWarden-owned state explicitly.
