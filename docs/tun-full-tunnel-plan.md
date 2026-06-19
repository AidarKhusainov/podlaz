# TUN Full-Tunnel Dry-Run Plan

`podlaz plan --mode tun <profile-id>` builds a read-only full-tunnel plan. It is planner output, not an executor transaction, and it must not mutate host networking.

## User-visible contract

The human output starts with `podlaz TUN plan` and includes:

- selected profile and `Mode: full-tunnel`;
- planned TUN device desired state, initially `create podlaz0`;
- the dedicated podlaz routing table, initially `podlaz` with ID `51820`;
- a default IPv4 traffic route through the podlaz table;
- a policy rule that keeps the VPN server bypass on the main uplink path;
- a policy rule that sends default IPv4 traffic to the podlaz table;
- an explicit VPN server bypass route through the current default gateway/interface when the server route resolves to a concrete IP address;
- a DNS plan for systemd-resolved per-link DNS on `podlaz0`, including rollback to the previous per-link DNS state where possible;
- a firewall plan for a podlaz-owned `inet podlaz` nftables table, typed chain/rule desired state, VPN server bypass, kill-switch policy behavior, recovery warning, ownership markers, rollback keys, and rollback by removing the owned table;
- current snapshot inputs for default route, server route, DNS, NetworkManager, nftables, IPv4/IPv6, TUN devices, and stale podlaz-owned resources;
- route-loop risks, unsupported backend warnings, kill-switch limitations, and stale-resource warnings;
- rollback steps corresponding to the planned nftables, DNS, policy-rule, route, and TUN desired state;
- `No changes were applied.`

JSON output keeps the common `schema_version`, `status`, `warnings`, and `errors` fields. The command-specific `plan` object includes `tunnel_mode`, `tun`, `routes`, `policy_rules`, `server_bypass`, `dns`, `firewall`, and the current `snapshot`. `plan.firewall` includes `chains` and `rules` arrays with typed nftables desired-state fields. Top-level `rollback_steps` correspond to the planned dry-run changes. `plan.claims_leak_protection` is `false` until apply, verify, rollback, and recover execution are implemented.

## Safety boundary

The command is dry-run only. It must not create TUN devices, add/delete routes, add/delete policy rules, change DNS, change nftables/firewall state, start Xray, or write generated runtime config.

The CLI may collect local read-only snapshots and render the plan in the foundation build. Actual privileged execution remains future daemon-owned transaction work.

## Planned ownership

The planned system state is podlaz-owned and must remain identifiable before it can be applied in a future executor:

- TUN interface: `podlaz0`;
- routing table: `podlaz` / `51820`;
- policy rule priorities: `51819` for VPN server bypass and `51820` for default podlaz traffic;
- DNS backend: systemd-resolved per-link DNS on `podlaz0`;
- nftables table: `table inet podlaz`;
- nftables chain: `output` with `type filter`, `hook output`, `priority 0`, and `policy accept`;
- nftables rule ownership markers such as `podlaz:firewall:server-bypass`, `podlaz:firewall:tun-egress`, and `podlaz:firewall:kill-switch`;
- nftables rollback keys such as `inet/podlaz/output/server-bypass`, `inet/podlaz/output/tun-egress`, and `inet/podlaz/output/kill-switch`.

## DNS plan

The current dry-run DNS desired state is:

```text
DNS plan:
- backend: systemd-resolved per-link DNS
- target link: podlaz0
- rollback: restore previous per-link DNS state where possible
```

If systemd-resolved cannot be inspected, the DNS desired state is blocked and the plan emits an actionable warning. The planner still remains read-only and does not add fallback DNS mutation.

Direct writes to `/etc/resolv.conf` remain forbidden.

## Firewall and kill-switch plan

The current dry-run firewall desired state is:

```text
Firewall plan:
- backend: nftables
- create nftables table inet podlaz
- allow VPN server bypass outside TUN
- allow traffic through podlaz0
- kill-switch policy: soft
- block non-TUN traffic according to selected kill-switch policy
Firewall chains:
- create chain output type filter hook output priority 0 policy accept
Firewall rules:
- add output ip daddr <server-ip> -> accept owner=podlaz:firewall:server-bypass rollback=inet/podlaz/output/server-bypass
- add output oifname "podlaz0" -> accept owner=podlaz:firewall:tun-egress rollback=inet/podlaz/output/tun-egress
- add output oifname != "podlaz0" -> reject owner=podlaz:firewall:kill-switch rollback=inet/podlaz/output/kill-switch
- rollback: remove inet podlaz
```

The planner models nftables desired state as typed chains and rules, not free-form renderer strings. This keeps the dry-run output useful for users while preserving a future executor/verify/recover path that can reason about chain name, hook, priority, expression, verdict, ownership, and rollback key.

If nftables cannot be inspected, the firewall desired state is blocked and the plan emits an actionable warning. If a podlaz-owned nftables table already exists, future apply must validate ownership or recover it before replacing rules.

The initial selected kill-switch policy is `soft` until user configuration exists. Strict kill-switch planning is supported internally and must warn that direct connectivity may remain blocked after VPN failure until podlaz recovery removes owned nftables rules. No dry-run output may claim leak protection until apply, verify, rollback, and recover execution exist.

## IPv4 and IPv6 assumptions

The implemented dry-run is IPv4-first. IPv6 state is still shown from the snapshot, but initial full-tunnel planning keeps IPv6 disabled or bypassed until full IPv6 route, DNS, and leak handling are designed and tested.

## Loop-risk handling

The planner must warn when the current route to the VPN server candidate points at `podlaz0`. That plan is unsafe because it would route control traffic back into the tunnel. The planner also warns when the current default IPv4 route already points at `podlaz0`, because a direct uplink snapshot is required before safe full-tunnel apply work.

## Rollback model

Rollback steps are generated in reverse apply order:

1. remove the podlaz-owned nftables table if it was created by the transaction;
2. restore previous systemd-resolved per-link DNS state where possible;
3. remove planned policy rules if they were created by the transaction;
4. remove planned routes if they were created by the transaction;
5. delete `podlaz0` only if the transaction created it and ownership matches podlaz.

These rollback steps are descriptive only in the dry-run. Future execution work must turn them into daemon-owned, audited, idempotent rollback operations.
