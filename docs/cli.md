# CLI Contract

This document is the canonical command-line interface contract for TunWarden.

Other documents may show examples, but this file owns command names, argument shape, safety semantics, output expectations, and milestone boundaries.

TunWarden is a Linux-first, CLI-first networking tool. The CLI must optimize for clarity, safe defaults, recoverability, and observability instead of command count.

State layout, JSON compatibility, redaction, confirmation behavior, systemd hardening, and core process safety are owned by [State and security requirements](./state-and-security.md). Package dependency direction is owned by [Package boundaries](./package-boundaries.md).

## 1. Design principles

### User task names before implementation names

Public commands describe user tasks and stable domain objects:

- `profile`
- `subscription`
- `import`
- `connect`
- `disconnect`
- `status`
- `doctor`
- `logs`
- `plan`
- `recover`

Avoid exposing implementation concepts as primary workflows unless they become real user-facing objects.

### Safe by default

Commands that can affect Linux networking must be inspectable before they change state.

Rules:

- read-only commands must not require root;
- recovery cleanup must require explicit `--execute` and `--yes` flags;
- full-tunnel networking changes must be planned before they are applied;
- proxy-only mode must not mutate routes, DNS, TUN, nftables, or firewall state;
- TUN planning must not create TUN devices, mutate routes, mutate policy rules, mutate DNS, mutate nftables/firewall state, start Xray, or write runtime config files;
- TUN execution must happen only through daemon-owned transaction state and rollback metadata;
- rollback must remove only state that the transaction actually applied.

### Human-readable first, automation-friendly when useful

Default output should be stable, concise, and readable by technical users.

Commands likely to be scripted should support `--json`, especially:

- `status`
- `doctor`
- `profile list`
- `profile show`
- `subscription list`
- `subscription show`
- `plan`

If a command-specific implementation issue explicitly defers JSON, that command must fail fast for `--json` with exit code `2` until the JSON contract is implemented in a dedicated change.

Primary output goes to stdout. Errors go to stderr.

Exit codes:

| Code | Meaning |
| ---: | --- |
| 0 | Command completed successfully and no unhealthy state was found. |
| 1 | Runtime or operation failed. |
| 2 | Invalid usage, invalid flags, or invalid arguments. |
| 3 | Diagnostic command completed but found unhealthy state. |
| 4 | Permission or authorization failure. |
| 5 | Daemon unavailable when the command requires daemon access. |

### Redaction

`status`, `doctor`, `logs`, `plan`, `recover`, and every `--json` output must follow the redaction rules in [State and security requirements](./state-and-security.md).

Default output must not print full subscription URLs, full share URIs, generated core configs containing credentials, private keys, passwords, authorization headers, provider tokens, or transaction content that could contain secret-looking values.

## 2. Global behavior

Every command and subcommand must support help:

```bash
tunwarden --help
tunwarden help
tunwarden help <command>
tunwarden <command> --help
tunwarden <command> -h
```

Common flags, where relevant:

```bash
--json       Print machine-readable JSON output.
--verbose    Print additional diagnostic detail.
--quiet      Print only essential output.
--yes        Confirm a command that removes state or executes recovery cleanup.
```

Short flags should be added only for frequent operations. Rare or high-impact flags such as `--execute` and `--yes` must stay long-only.

Commands that remove user state or execute recovery cleanup must follow this model:

- interactive TTY: ask for confirmation unless `--yes` is passed;
- non-interactive mode: fail unless `--yes` is passed;
- `--json` mode: fail unless `--yes` is passed.

Connection modes:

```text
proxy-only
tun
```

The default connection mode is `proxy-only`.

## 3. Implemented command contract

### Version and help

```bash
tunwarden version
tunwarden help
```

Mutation level: read-only.

Daemon requirement: none.

### Import convenience

```bash
tunwarden import <uri-or-file-or-url>
```

Purpose: user-friendly import entrypoint with format detection.

Expected behavior:

- supported share URI creates one profile;
- subscription URL or file creates a subscription source and imports supported profiles;
- unsupported input fails clearly.

Mutation level: persistent local TunWarden state only.

Non-goal: this command must not connect or start Xray.

### Profile management

```bash
tunwarden profile add --name <name> --server <host> --port <port> --protocol <vless|vmess|trojan|shadowsocks>
tunwarden profile import <share-uri>
tunwarden profile list [--json]
tunwarden profile show <profile-id> [--json]
tunwarden profile delete <profile-id> --yes
```

Implemented behavior:

- manual profile add, list, show, and delete;
- VLESS, VMess, Trojan, and Shadowsocks share URI import through `profile import <share-uri>`;
- deterministic imported profile IDs;
- persistent local profile storage at the documented XDG user state location;
- `profile list --json` and `profile show --json` with `schema_version: "v1"`;
- required-field validation and clear failure for malformed payloads;
- redaction of identity/authentication fields;
- atomic profile store writes with restrictive file permissions;
- `profile delete` requires `--yes` in the current non-interactive path.

Mutation level:

- `list` and `show`: read-only;
- `add`, `import`, and `delete`: persistent local TunWarden state only.

Non-goals: no Xray process start and no networking mutation.

### Subscription management

```bash
tunwarden subscription add --name <name> --url <url>
tunwarden subscription update <subscription-id>
tunwarden subscription list [--json]
tunwarden subscription show <subscription-id> [--json]
tunwarden subscription delete <subscription-id> [--yes]
```

Purpose: explicit lifecycle management for subscription sources.

Mutation level:

- `list` and `show`: read-only;
- `add`, `update`, and `delete`: persistent local TunWarden state only.

Required behavior:

- failed update preserves last known good imported profiles;
- unsupported entries are reported clearly;
- deleting a subscription must have clear behavior for imported profiles before implementation.

### Status

```bash
tunwarden status
```

Purpose: report local and daemon-backed TunWarden state.

Mutation level: read-only.

Daemon requirement: optional. The command must use daemon-backed status when available and a conservative local fallback otherwise.

Implemented behavior:

- human output only;
- daemon-backed inactive, active proxy-only, and active TUN transaction state;
- active mode when a lifecycle is running;
- local proxy listener state when proxy-only lifecycle is running;
- Xray crash visibility through daemon warnings;
- explicit TUN, route, DNS, and firewall state from the daemon;
- transaction summaries with state, rollback availability, cleanup requirement, and redacted transaction path;
- conservative local fallback when daemon is unavailable;
- runtime directory state;
- stale runtime state summary;
- guidance to `tunwarden recover` when recovery candidates exist.

`status --json` is deferred to a separate issue. Until implemented, `status --json` must fail fast as invalid usage with exit code `2`.

### Doctor

```bash
tunwarden doctor [--json]
tunwarden doctor --core --xray <path> [--json]
tunwarden doctor --network [--json]
tunwarden doctor --dns [--json]
tunwarden doctor --routes [--json]
tunwarden doctor --firewall [--json]
```

Purpose: explain environment and runtime health.

Mutation level: read-only.

Daemon requirement: optional. The default command must use daemon-backed diagnostics when available and local read-only diagnostics otherwise. The `doctor --core --xray <path>` scope is explicitly local-only.

Implemented behavior:

- default human output with daemon-backed diagnostics or local fallback;
- local host diagnostics for platform, command availability, default route, default interface, resolved/TunWarden DNS visibility, and stale TunWarden-owned resources;
- local-only `doctor --core --xray <path>` validation of an explicitly provided Xray binary;
- `doctor --core --xray <path> --json` with the common top-level JSON shape and `checks`;
- transaction-state diagnostics through stale resource checks;
- shared human/JSON redaction for doctor output.

`doctor --json` without `--core` is deferred to a separate issue. Until implemented, it must fail fast as invalid usage with exit code `2`.

`doctor --core` without `--xray <path>` is deferred. It must fail fast as invalid usage with exit code `2` instead of guessing a default Xray path.

### Logs

```bash
tunwarden logs [--follow] [--daemon] [--core] [--since <duration>]
tunwarden logs -f
```

Purpose: inspect TunWarden daemon and core logs.

Mutation level: read-only.

Implemented behavior:

- human output only;
- recent `tunwardend.service` logs through the system journal with `journalctl --system`;
- `--follow` and `-f` for live log following;
- `--daemon` as the explicit daemon log source;
- `--core` for Xray lifecycle lines and daemon-forwarded Xray stdout/stderr;
- `--since <duration>` and `--since=<duration>` passed to journalctl;
- shared human-output redaction for each printed log line;
- clear no-core-log guidance when `--core` finds no recent matching lines in non-follow mode.

`logs --json` is deferred to a separate issue. Until implemented, it must fail fast as invalid usage with exit code `2`.

If `journalctl` is unavailable, the command must fail clearly with an actionable message. If the current user cannot read the system journal, the command must surface the redacted `journalctl` error.

### Plan

```bash
tunwarden plan --mode proxy-only <profile-id> [--json]
tunwarden plan --mode tun <profile-id> [--json]
```

Purpose: show what a connection setup can inspect or create before starting Xray or changing host networking.

Mutation level: read-only.

Implemented proxy-only output:

- selected profile;
- selected mode;
- generated Xray config path;
- local proxy listeners;
- explicit statement that no TUN, routes, DNS, nftables, or firewall state will be changed;
- warnings for unsupported profile settings.

Implemented TUN full-tunnel dry-run output:

- selected profile;
- user-visible `Mode: full-tunnel`;
- explicit dry-run guarantee that no TUN, route, policy-rule, DNS, nftables/firewall, Xray, or runtime config state is changed;
- planned TUN device desired state, initially `create tunwarden0`;
- dedicated TunWarden routing table, initially `tunwarden` with ID `51820`;
- default IPv4 route desired state through the TunWarden table;
- policy-rule desired state for default IPv4 traffic through the TunWarden table;
- VPN server bypass route and policy-rule desired state only when the current read-only snapshot resolved the server route to a concrete IP address;
- DNS desired state for systemd-resolved per-link DNS on `tunwarden0`, including planned DNS servers, route-only domain `~.`, default-route `yes`, and rollback intent;
- nftables/firewall desired state for TunWarden-owned `table inet tunwarden`;
- typed nftables chain/rule desired state for future apply, verify, and recover behavior;
- rollback steps for planned nftables, DNS, TUN device, route, and policy-rule desired state;
- final `No changes were applied.` confirmation.

Implemented TUN JSON output keeps the common `schema_version`, `status`, `warnings`, and `errors` fields. Top-level `mode` remains the CLI mode selector value `tun`. The command-specific `plan.tunnel_mode` field is `full-tunnel`. The `plan` object includes `profile`, `tun`, `routes`, `policy_rules`, `server_bypass`, `dns`, `firewall`, safety flags, and the full current `snapshot`. `plan.dns` includes `servers`, `route_only_domain`, and `default_route` so the exact DNS desired state is inspectable before mutation.

`plan --mode tun` is still read-only. It produces intended TUN/route/policy-rule/DNS/nftables/firewall/kill-switch dry-run and rollback descriptions, but it does not apply anything.

### Connect and disconnect

```bash
tunwarden connect [--mode proxy-only|tun] <profile-id>
tunwarden disconnect
```

Purpose: start and stop daemon-managed connection lifecycle.

Daemon requirement: required through the local Unix socket API.

Default mode: `proxy-only`.

Implemented proxy-only behavior:

- stored profile lookup in user-owned local profile state before sending a normalized profile snapshot to the daemon;
- daemon-side profile validation before process start;
- generated runtime Xray config under the daemon runtime directory;
- daemon-managed Xray start and stop;
- packaged proxy-only `tunwardend` and Xray run as the unprivileged `tunwarden:tunwarden` service identity;
- manual root proxy-only connect is rejected instead of starting Xray as root;
- graceful stop and forced-stop fallback;
- idempotent disconnect;
- Xray crash visible in daemon-backed `status`.

Implemented `connect --mode tun` executor-slice behavior:

- daemon-owned transaction file is written before any TUN mutation;
- current host networking is captured through the read-only snapshot model;
- existing TUN full-tunnel planner output is used as desired state;
- executor creates the TunWarden-owned TUN interface;
- executor adds, never replaces, planned routes;
- executor adds, never treats pre-existing rules as owned, planned policy rules;
- executor applies systemd-resolved per-link DNS from `TunDNSPlan.Servers` with `resolvectl dns`, `resolvectl domain '~.'`, and `resolvectl default-route yes`;
- route verify checks the destination plus expected device and gateway where applicable;
- policy-rule verify checks the expected selector and lookup table;
- DNS verify checks planned DNS servers and route-only domain `~.`;
- transaction commits only after apply and verify succeed;
- apply, verify, or transition failure rolls back only the steps actually applied by the transaction, including DNS via `resolvectl revert` when DNS was applied;
- `disconnect` rolls back the active TunWarden-owned TUN transaction and is safe to repeat;
- status exposes transaction state, rollback availability, cleanup requirement, redacted transaction path, and DNS desired state.

Current `connect --mode tun` limitations:

- it is still an executor slice, not complete full VPN mode;
- it does not start Xray in TUN mode yet;
- it does not mutate nftables/firewall yet;
- it does not claim full leak protection yet;
- it supports systemd-resolved DNS only; non-systemd fallback and user DNS configuration are future work;
- it requires daemon process privileges equivalent to `CAP_NET_ADMIN` for TUN, route, and policy-rule mutation plus permission to call `resolvectl` against systemd-resolved.

Privilege model:

- proxy-only lifecycle must remain unprivileged and must not start Xray as root;
- TUN execution requires a separately documented privileged daemon deployment or future helper with `CAP_NET_ADMIN`-equivalent rights;
- the CLI must not become a privileged/SUID networking mutator;
- expanding packaged daemon privileges must update the service/security documentation in the same change.

`connect --json` and `disconnect --json` are deferred. Until implemented, they must fail fast as invalid usage with exit code `2`.

### Recovery

```bash
tunwarden recover
tunwarden recover --execute --yes
```

Purpose: inspect and later clean up stale TunWarden-owned state.

Mutation level:

- `recover`: read-only dry-run;
- `recover --execute --yes`: explicit cleanup of TunWarden-owned state only; deferred until safe cleanup execution exists.

Implemented behavior:

- `recover` is dry-run only;
- pending, failed, rolling-back, or stale transaction files under `/run/tunwarden/transactions/` are shown as recovery candidates;
- transaction candidates include state, rollback availability, cleanup requirement, and redacted transaction path;
- invalid or unreadable transaction files are reported as inspection warnings, not ignored.

Expected cleanup candidates:

- TunWarden-owned runtime files;
- TunWarden-owned generated configs;
- TunWarden-owned core processes;
- TunWarden-owned TUN interfaces;
- TunWarden-owned routes/rules actually recorded in transaction rollback metadata;
- future TunWarden-owned nftables state;
- TunWarden-owned DNS state recorded in transaction rollback metadata where reversible.

## 4. Milestone boundaries

The current implementation contains:

- proxy-only lifecycle for Xray;
- read-only full-tunnel TUN planning;
- transaction-state persistence and diagnostics;
- daemon-owned privileged TUN executor slice for TUN interface, routes, policy rules, and systemd-resolved DNS;
- DNS desired-state persistence in transaction state, including planned servers.

Still deferred:

- TUN-mode Xray lifecycle integration;
- configurable DNS policy and non-systemd DNS fallback;
- nftables/firewall mutation and rollback;
- full leak-protection verification;
- `recover --execute --yes` cleanup;
- reconnect/suspend/resume state machine;
- GUI.
