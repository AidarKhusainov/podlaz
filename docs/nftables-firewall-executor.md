# nftables Firewall Executor

This document describes the implemented nftables executor slice for TUN transactions.

The canonical networking invariants remain owned by [Networking and Reliability Requirements](./networking-reliability.md). This document records the concrete executor behavior added for podlaz-owned firewall state.

## Ownership boundary

podlaz creates and mutates only this nftables object tree:

```text
table inet podlaz
```

podlaz must not mutate user-owned nftables tables, chains, or rules outside `inet podlaz`.

The executor enforces this boundary itself. It rejects every apply, verify, or rollback target except:

```text
family = inet
table  = podlaz
```

This check is intentionally duplicated in the privileged executor instead of trusting planner output or transaction rollback metadata. Corrupt or stale metadata must fail closed and must not cause deletion of arbitrary user firewall state.

The initial executor owns one output chain inside that table:

```text
chain output {
  type filter hook output priority 0; policy accept;
}
```

Rules are marked with explicit podlaz comments:

```text
podlaz:firewall:server-bypass
podlaz:firewall:tun-egress
podlaz:firewall:kill-switch
```

These markers are part of the inspectable desired state and the verification contract.

## Planned rules

For a concrete VPN server IP, the executor installs the planner-provided server bypass rule before non-TUN blocking:

```text
ip daddr <server-ip> counter comment "podlaz:firewall:server-bypass" accept
```

TUN egress is allowed explicitly:

```text
oifname "podlaz0" counter comment "podlaz:firewall:tun-egress" accept
```

The kill-switch rule depends on the selected planner policy:

```text
oifname != "podlaz0" counter comment "podlaz:firewall:kill-switch" reject  # soft
oifname != "podlaz0" counter comment "podlaz:firewall:kill-switch" drop    # strict
```

For `off`, the kill-switch blocking rule is skipped.

## Apply order

The daemon transaction applies networking state in this order:

1. TUN interface, routes, and policy rules.
2. systemd-resolved per-link DNS.
3. podlaz-owned nftables table, chain, and rules.
4. Verification before transaction commit.

If nftables apply fails after creating partial state, the executor removes `inet podlaz` immediately. The transaction then rolls back the already-recorded DNS, route, policy-rule, and TUN steps.

## Verification

Before commit, the executor runs:

```bash
nft list table inet podlaz
```

Verification requires:

- the planned chain to be visible;
- every planned rule to appear on one nft output line with the expected expression, `counter comment`, podlaz owner marker, and verdict in order.

This avoids accepting output where the expression, owner marker, and verdict appear on different rules.

A failed verification triggers transaction rollback.

## Rollback and disconnect cleanup

Rollback deletes the whole podlaz-owned table:

```bash
nft delete table inet podlaz
```

This is intentionally idempotent. Missing-table errors are treated as already-clean state.

Rollback rejects non-owned targets instead of silently no-oping. This keeps corrupted transaction metadata visible and prevents accidental deletion of arbitrary nftables state.

Disconnect uses transaction rollback metadata to remove `inet podlaz` before DNS, route, policy-rule, and TUN cleanup. Recovery commands that execute cleanup must use the same ownership boundary: remove podlaz-owned nftables state, not arbitrary firewall state.

## Existing table behavior

If snapshot/planner state reports that `inet podlaz` already exists before apply, the current executor does not silently validate-or-replace it. The connect transaction fails before firewall mutation so the user can inspect or recover stale podlaz-owned state explicitly.
