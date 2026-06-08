# CLI Contract

This document is the canonical command-line interface contract for TunWarden.

Other documents may show examples, but this file owns command names, argument shape, safety semantics, output expectations, and milestone boundaries.

TunWarden is a Linux-first, CLI-first networking tool. The CLI must optimize for clarity, safe defaults, recoverability, and observability instead of command count.

State layout, JSON compatibility, redaction, confirmation behavior, systemd hardening, and core process safety are owned by [State and security requirements](./state-and-security.md).

## 1. Design principles

### User task names before implementation names

Public commands should describe user tasks and stable domain objects:

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
- TUN planning must not create TUN devices, mutate routes, mutate policy rules, mutate DNS, mutate nftables/firewall state, start Xray, or write runtime config files.

### Object groups for long-lived state

Use command groups for resources with independent lifecycles:

```bash
tunwarden profile ...
tunwarden subscription ...
```

A convenience `tunwarden import` command may exist for common first-run workflows, but it must not erase the distinction between profiles and subscriptions.

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

`doctor` returns `0` only when diagnostics complete and required checks are healthy. It returns `3` when diagnostics complete but one or more checks fail. It returns `1` when diagnostics cannot complete because of an unexpected runtime error.

`status` returns `0` for a clean inactive local state in the v0.1 local fallback. It returns `3` when the local fallback finds stale runtime state or incomplete visibility.

### JSON compatibility

JSON output is a stable public interface once implemented for a command.

Rules:

- every JSON response must include `schema_version`;
- existing field names and meanings must not change without a documented compatibility note;
- new fields may be added;
- consumers must ignore unknown fields;
- human output and JSON output must apply the same redaction policy.

Common top-level shape:

```json
{
  "schema_version": "v1",
  "status": "ok|warn|fail",
  "warnings": [],
  "errors": []
}
```

Command-specific top-level fields:

```text
status:
  daemon
  connection
  runtime

doctor:
  checks

profile list:
  profiles

profile show:
  profile

plan:
  mode
  plan
  steps
  rollback_steps
```

The detailed schema can evolve during implementation, but these top-level meanings are part of the CLI contract once the corresponding command's `--json` output is implemented.

If a command-specific implementation issue explicitly defers JSON, that command must fail fast for `--json` with exit code `2` until the JSON contract is implemented in a dedicated change.

### Redaction

`status`, `doctor`, `logs`, `plan`, `recover`, and every `--json` output must follow the redaction rules in [State and security requirements](./state-and-security.md).

Default output must not print full subscription URLs, full share URIs, generated core configs containing credentials, private keys, passwords, auth headers, or provider tokens.

### Flags over command proliferation

Use flags to select facets of an existing task.

Preferred:

```bash
tunwarden doctor --core --xray <path>
tunwarden doctor --dns
tunwarden doctor --routes
tunwarden logs --core
tunwarden logs --daemon
```

Avoid separate command families for checks that belong under `doctor`.

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

Short flags should be added only for frequent operations. For example, `logs -f` may alias `logs --follow`, but rare or high-impact flags such as `--execute` and `--yes` must stay long-only.

Commands that remove user state or execute recovery cleanup must follow this model:

- interactive TTY: ask for confirmation unless `--yes` is passed;
- non-interactive mode: fail unless `--yes` is passed;
- `--json` mode: fail unless `--yes` is passed.

Connection modes:

```text
proxy-only
tun
```

Initial default mode should be `proxy-only` until TUN mode is implemented and safe.

## 3. v0.1 command contract: proxy-only technical preview

v0.1 must deliver a coherent proxy-only flow without TUN, route, DNS, nftables, or firewall mutation.

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

Purpose: explicit lifecycle management for individual profiles.

Implemented foundation profile management view:

- manual profile add, list, show, and delete;
- VLESS, VMess, Trojan, and Shadowsocks share URI import through `profile import <share-uri>`;
- deterministic imported profile IDs based on display name plus stable connection fingerprint;
- persistent local profile storage at the documented XDG user state location;
- human output for all implemented profile commands;
- `profile list --json` and `profile show --json` with `schema_version: "v1"`;
- required-field validation for manual profile name, protocol, server, and port;
- required-field and compatibility validation for imported share URI identities, server, port, transport, and security;
- clear failure for malformed share URI payloads and query percent-encoding;
- warnings for unsupported share URI options that are ignored by the current build;
- redaction of imported identity/authentication fields in human and JSON output;
- atomic profile store writes with restrictive file permissions;
- corrupt or unreadable profile storage fails safely with a clear error;
- `profile delete` requires `--yes` in the current non-interactive v0.1 CLI path.

Deferred behavior:

- `profile import --json`;
- VLESS custom string IDs;
- generated proxy-only Xray config support for non-VLESS imported profiles.

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

Implemented foundation status behavior:

- human output only;
- daemon-backed inactive and active proxy-only state;
- active mode when proxy-only lifecycle is running;
- local proxy listener state when proxy-only lifecycle is running;
- Xray crash visibility through daemon warnings;
- explicit TUN, route, DNS, and firewall non-mutation state from the daemon;
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

Daemon requirement: optional. The default command must use daemon-backed diagnostics when available and local read-only diagnostics otherwise. The v0.1 `doctor --core --xray <path>` scope is explicitly local-only.

Implemented foundation doctor behavior:

- default human output with daemon-backed diagnostics or local fallback;
- local host diagnostics for platform, command availability, default route, default interface, and stale TunWarden-owned resources;
- local-only `doctor --core --xray <path>` validation of an explicitly provided Xray binary;
- `doctor --core --xray <path> --json` with the common top-level JSON shape and `checks`;
- shared human/JSON redaction for doctor output.

`doctor --json` without `--core` is deferred to a separate issue. Until implemented, it must fail fast as invalid usage with exit code `2`.

`doctor --core` without `--xray <path>` is deferred in v0.1. It must fail fast as invalid usage with exit code `2` instead of guessing a default Xray path.

`doctor --core` is the preferred public UX for validating the Xray binary and runtime core health. A lower-level `core check` command is not part of the v0.1 public contract.

### Logs

```bash
tunwarden logs [--follow] [--daemon] [--core] [--since <duration>]
tunwarden logs -f
```

Purpose: inspect TunWarden daemon and core logs.

Mutation level: read-only.

Implemented v0.1 journald-backed log behavior:

- human output only;
- recent `tunwardend.service` logs through the system journal with `journalctl --system`;
- `--follow` and `-f` for live log following;
- `--daemon` as the explicit daemon log source;
- `--core` for Xray lifecycle lines and daemon-forwarded Xray stdout/stderr marked with `tunwardend: core xray ...`;
- `--since <duration>` and `--since=<duration>` passed to journalctl, including relative values such as `-1h`;
- shared human-output redaction for each printed log line;
- clear no-core-log guidance when `--core` finds no recent matching lines in non-follow mode.

`logs --json` is deferred to a separate issue. Until implemented, it must fail fast as invalid usage with exit code `2`.

If `journalctl` is unavailable, the command must fail clearly with an actionable message. If the current user cannot read the system journal, the command must surface the redacted `journalctl` error.

`-f` may alias `--follow` because it is a common log-following pattern.

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
- core binary path/version if known;
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
- blocked/incomplete server-bypass output and warnings when the server target is not a concrete IP address;
- current default IPv4 and IPv6 route state;
- current default interface when detected;
- route to the VPN server candidate after resolving hostname servers to an IP address with a read-only resolver timeout;
- DNS mode and systemd-resolved availability/state;
- NetworkManager availability and advisory state;
- nftables availability and TunWarden-owned `inet tunwarden` table presence;
- IPv4/IPv6 assumptions;
- known TunWarden TUN device presence for names such as `tunwarden0`;
- stale TunWarden-owned resources;
- warnings for incomplete visibility, DNS resolution failure, optional backend absence, stale resources, and route-loop risk;
- rollback steps for the planned TUN device, route, and policy-rule desired state;
- final `No changes were applied.` confirmation.

Implemented TUN JSON output keeps the common `schema_version`, `status`, `warnings`, and `errors` fields. Top-level `mode` remains the CLI mode selector value `tun`. The command-specific `plan.tunnel_mode` field is `full-tunnel`. The `plan` object includes `profile`, `tun`, `routes`, `policy_rules`, `server_bypass`, safety flags, and the full current `snapshot`. The snapshot includes `os`, default routes, server route, DNS, NetworkManager, nftables, TUN devices, IPv4, IPv6, and stale resources with the same redaction policy as human output.

The current `plan --mode tun` implementation is still read-only. It produces an intended TUN/route/policy-rule dry-run and rollback description, but it does not yet apply anything and does not yet produce DNS, nftables/firewall, kill-switch, or health-check apply plans.

### Connect and disconnect

```bash
tunwarden connect [--mode proxy-only] <profile-id>
tunwarden disconnect
```

Purpose: start and stop proxy-only Xray lifecycle.

Mutation level: process lifecycle and volatile TunWarden runtime state only.

Implemented v0.1 behavior:

- daemon-required lifecycle through the local Unix socket API;
- stored profile lookup in user-owned local profile state before sending a normalized profile snapshot to the daemon;
- daemon-side profile validation before process start;
- generated runtime Xray config under the daemon runtime directory;
- daemon-managed Xray start and stop;
- packaged `tunwardend` and Xray run as the unprivileged `tunwarden:tunwarden` service identity;
- manual root `connect` is rejected instead of starting Xray as root;
- graceful stop and forced-stop fallback;
- idempotent disconnect;
- Xray crash visible in daemon-backed `status`.

v0.1 safety boundary:

- no TUN interface;
- no route mutation;
- no DNS mutation;
- no nftables/firewall mutation;
- no automatic Xray download/update;
- no full leak-protection claim.

### Recovery

```bash
tunwarden recover
tunwarden recover --execute --yes
```

Purpose: inspect and later clean up stale TunWarden-owned state.

Mutation level:

- `recover`: read-only dry-run;
- `recover --execute --yes`: explicit cleanup of TunWarden-owned state only; deferred until safe TUN work.

v0.1 requirement:

- `recover` must be dry-run only in v0.1;
- `--execute` must not be implemented in v0.1.

Expected cleanup candidates:

- TunWarden-owned runtime files;
- TunWarden-owned generated configs;
- TunWarden-owned core processes;
- future TunWarden-owned TUN interfaces;
- future TunWarden-owned routes/rules;
- future TunWarden-owned nftables state;
- future TunWarden-owned DNS state where reversible.

## 4. v0.2 command additions: safe TUN preview

v0.2 adds privileged networking only through the transaction model.

```bash
tunwarden plan --mode tun <profile-id> [--json]
tunwarden connect --mode tun <profile-id>
tunwarden reconnect
tunwarden recover --execute --yes
tunwarden logs --network
tunwarden doctor --network
tunwarden doctor --dns
tunwarden doctor --routes
tunwarden doctor --firewall
```

The current `plan --mode tun` implementation is the read-only full-tunnel TUN/route dry-run required before full TUN mutation work. It combines the current host snapshot with intended TUN device, route, policy-rule, VPN server bypass, route-loop warning, and rollback output. It still does not apply anything and does not yet plan DNS, nftables/firewall, kill-switch, or health-check apply behavior.

`connect --mode tun` must apply changes only through daemon-owned network transactions.

`reconnect` should become public only when the daemon has a real state machine for core crash, suspend/resume, and network change handling.

`recover --execute --yes` must affect only identifiable TunWarden-owned state and must print what changed and what could not be changed.

## 5. Explicitly deferred commands

These commands are not part of v0.1 unless a later issue explicitly changes the milestone:

```bash
tunwarden core check --xray
tunwarden explain routes
tunwarden explain dns
tunwarden explain firewall
tunwarden latency
tunwarden test-url
tunwarden auto-select
```

Notes:

- `core check` is deferred because `doctor --core` is the preferred user-facing workflow.
- `explain ...` commands are deferred until `doctor` and `plan` output become too large for one command.
- latency, URL testing, and auto-select are convenience features, not reliability foundations.

## 6. Naming decisions

### `import` as convenience, not replacement

`tunwarden import` exists for first-run convenience and format detection.

It must not replace explicit `profile` and `subscription` command groups, because profiles and subscriptions have different lifecycles.

### `plan` is a safety command

`plan` is required because TunWarden changes Linux networking in later milestones.

It must not become decorative. If a plan cannot explain meaningful changes or non-changes, it should not be exposed for that mode yet.
