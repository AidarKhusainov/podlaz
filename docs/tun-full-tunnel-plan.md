# TUN Full-Tunnel Dry-Run Plan

`tunwarden plan --mode tun <profile-id>` builds a read-only full-tunnel plan. It is a planner output, not an executor transaction, and it must not mutate host networking.

## User-visible contract

The human output starts with `TunWarden TUN plan` and includes:

- selected profile and `Mode: full-tunnel`;
- planned TUN device desired state, initially `create tunwarden0`;
- the dedicated TunWarden routing table, initially `tunwarden` with ID `51820`;
- a default IPv4 traffic route through the TunWarden table;
- a policy rule that keeps the VPN server bypass on the main uplink path;
- a policy rule that sends default IPv4 traffic to the TunWarden table;
- an explicit VPN server bypass route through the current default gateway/interface;
- current snapshot inputs for default route, server route, DNS, NetworkManager, nftables, IPv4/IPv6, TUN devices, and stale TunWarden-owned resources;
- route-loop risks and warnings;
- rollback steps corresponding to the planned TUN, route, and policy-rule changes;
- `No changes were applied.`

JSON output keeps the common `schema_version`, `status`, `warnings`, and `errors` fields. The command-specific `plan` object includes `tunnel_mode`, `tun`, `routes`, `policy_rules`, `server_bypass`, and the current `snapshot`. Top-level `rollback_steps` correspond to the planned changes.

## Safety boundary

The command is dry-run only. It must not create TUN devices, add/delete routes, add/delete policy rules, change DNS, change nftables/firewall state, start Xray, or write generated runtime config.

The CLI may collect local read-only snapshots and render the plan in the foundation build. Actual privileged execution remains future daemon-owned transaction work.

## Planned ownership

The planned system state is TunWarden-owned and must remain identifiable before it can be applied in a future executor:

- TUN interface: `tunwarden0`;
- routing table: `tunwarden` / `51820`;
- policy rule priorities: `51819` for VPN server bypass and `51820` for default TunWarden traffic;
- nftables remains deferred in this dry-run and is not planned as an apply step here.

## IPv4 and IPv6 assumptions

The implemented dry-run is IPv4-first. IPv6 state is still shown from the snapshot, but initial full-tunnel planning keeps IPv6 disabled or bypassed until full IPv6 route, DNS, and leak handling are designed and tested.

## Loop-risk handling

The planner must warn when the current route to the VPN server candidate points at `tunwarden0`. That plan is unsafe because it would route control traffic back into the tunnel. The planner also warns when the current default IPv4 route already points at `tunwarden0`, because a direct uplink snapshot is required before safe full-tunnel apply work.

## Rollback model

Rollback steps are generated in reverse apply order:

1. remove planned policy rules if they were created by the transaction;
2. remove planned routes if they were created by the transaction;
3. delete `tunwarden0` only if the transaction created it and ownership matches TunWarden.

These rollback steps are descriptive only in the dry-run. Future execution work must turn them into daemon-owned, audited, idempotent rollback operations.
