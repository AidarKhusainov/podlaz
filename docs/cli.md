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
- `completion`

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
- `profile validate`
- `subscription list`
- `subscription show`
- `subscription delete`
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

`status`, `doctor`, `logs`, `plan`, `recover`, `profile validate`, and every `--json` output must follow the redaction rules in [State and security requirements](./state-and-security.md).

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

Shell completion must be generated through the public CLI command and written to stdout only. Completion generation must be static and read-only: it must not contact `tunwardend`, start Xray, inspect profile/subscription state, read secrets, mutate Linux networking, or require root.

## 3. Implemented command contract

### Version and help

```bash
tunwarden version
tunwarden help
```

Mutation level: read-only.

Daemon requirement: none.

### Shell completion

```bash
tunwarden completion bash
tunwarden completion zsh
tunwarden completion fish
```

Purpose: generate shell completion definitions for supported interactive shells.

Mutation level: read-only.

Daemon requirement: none.

Implemented behavior:

- writes the requested completion script to stdout;
- supports `bash`, `zsh`, and `fish` as explicit shell names;
- completes stable top-level command names;
- completes implemented nested subcommands for `profile`, `subscription`, `completion`, and `help`;
- completes implemented flags for command scopes that currently define flags;
- completes static enum values including connection modes `proxy-only` and `tun`, `profile validate --mode` values `proxy-only` and `tun`, and supported profile protocol names;
- intentionally does not complete dynamic user-specific values such as profile IDs, subscription IDs, file paths, URLs, daemon state, transaction IDs, routes, DNS data, or firewall state.

Safety requirements:

- completion generation must not read profile stores, subscription stores, runtime transaction files, daemon state, logs, generated core configs, or secrets;
- completion generation must not start `tunwardend`, start Xray, open the local daemon socket, create files, mutate TUN devices, mutate routes, mutate policy rules, mutate DNS, mutate nftables/firewall state, or require elevated privileges;
- completion scripts may contain shell functions and static metadata only.

Packaging requirement: Debian packages must install generated completion files under conventional distro locations for bash, zsh, and fish. The exact packaged paths are owned by [Debian package contract](./debian-package.md).

Output compatibility: the supported shell names and CLI behavior are stable contract. The exact generated script text is implementation detail except for shell validity and the completion coverage promised above.

### Import convenience

```bash
tunwarden import <share-uri>
tunwarden import <local-path>
tunwarden import <file-or-http-subscription-url>
```

Purpose: user-friendly first-run import entrypoint with format detection.

Expected behavior:

- supported share URI creates one `imported_uri` profile;
- ordinary local paths are one-shot local imports, not tracked subscriptions;
- local import detects Xray JSON object files, plain URI-list files, and Base64 URI-list files;
- supported local entries are normalized into `imported_file` profiles and persisted in user-owned profile state;
- local Xray JSON import supports VLESS outbounds that map cleanly to the existing normalized profile model;
- unsupported local Xray JSON outbounds or URI-list entries are reported clearly when at least one supported profile is imported;
- malformed JSON object files fail as Xray JSON errors and must not fall back to URI-list parsing;
- duplicate profile IDs inside one local import batch and existing-profile ID collisions fail before profile-store replacement, leaving existing state unchanged;
- `file://`, `http://`, and `https://` inputs create subscription sources and import supported subscription profiles;
- subscription responses support Base64 URI-list and Xray JSON object/array formats;
- subscription Xray JSON import supports VLESS outbounds that map cleanly to the existing normalized profile model;
- malformed subscription JSON that starts with `{` or `[` fails on the JSON path and must not fall back to Base64 parsing;
- the detected subscription format is persisted in subscription metadata and shown by `subscription list` and `subscription show`;
- unsupported input fails clearly.

Mutation level: persistent local TunWarden state only.

Safety requirements:

- import must not connect, start `tunwardend`, start Xray, require root, create TUN devices, mutate routes, mutate DNS, mutate nftables, or mutate firewall state;
- local and subscription Xray JSON must be parsed and normalized into profiles only; raw Xray JSON must not be stored as persistent source of truth or runtime configuration;
- default output must redact share URIs, identities, passwords, provider tokens, generated core configs, and secret-looking values.

Detailed local file format behavior is documented in [Local import formats](./local-import-formats.md).

`import --json` is deferred. Until implemented, it must fail fast as invalid usage with exit code `2`.

### Profile management

```bash
tunwarden profile add --name <name> --server <host> --port <port> --protocol <vless|vmess|trojan|shadowsocks>
tunwarden profile import <share-uri>
tunwarden profile list [--json]
tunwarden profile show <profile-id> [--json]
tunwarden profile validate <profile-id> [--mode proxy-only|tun] [--json]
tunwarden profile delete <profile-id> --yes
```

Implemented behavior:

- manual profile add, list, show, validate, and delete;
- VLESS, VMess, Trojan, and Shadowsocks share URI import through `profile import <share-uri>`;
- deterministic imported profile IDs;
- persistent local profile storage at the documented XDG user state location;
- `profile list --json`, `profile show --json`, and `profile validate --json` with `schema_version: "v1"`;
- required-field validation and clear failure for malformed payloads;
- selected-mode backend renderability validation through the supported Xray config generation path;
- redaction of identity/authentication fields;
- atomic profile store writes with restrictive file permissions;
- `profile delete` requires `--yes` in the current non-interactive path.

Mutation level:

- `list`, `show`, and `validate`: read-only;
- `add`, `import`, and `delete`: persistent local TunWarden state only.

`profile validate` details:

- default mode is `proxy-only`;
- `--mode tun` validates against the TUN-mode Xray renderability path;
- success returns exit code `0`;
- an existing profile that fails mode/backend renderability validation returns exit code `3`;
- invalid arguments or unsupported modes return exit code `2`;
- missing profile lookup returns exit code `1`;
- human and JSON output must apply equivalent redaction;
- the command must not start `tunwardend`, start Xray, require root, create TUN devices, mutate routes, mutate DNS, mutate nftables, mutate firewall state, or write runtime configuration.

`profile validate --json` output uses the common top-level JSON shape with `schema_version`, `status`, `warnings`, and `errors`. It also includes `profile`, `mode`, `backend`, and boolean `valid` fields.

Non-goals: no Xray process start and no networking mutation.

### Subscription management

```bash
tunwarden subscription add --name <name> --url <url>
tunwarden subscription update <subscription-id>
tunwarden subscription list [--json]
tunwarden subscription show <subscription-id> [--json]
tunwarden subscription delete <subscription-id> [--yes] [--keep-profiles]
```

Purpose: explicit lifecycle management for subscription sources.

Supported source schemes: `file`, `http`, and `https`.

Supported response formats:

- Base64 URI-list;
- Xray JSON object or array.

Mutation level:

- `list` and `show`: read-only;
- `add`, `update`, and `delete`: persistent local TunWarden state only.

Required behavior:

- `add` stores a subscription source without fetching it;
- `update` fetches the source, detects the response format, normalizes supported profiles, and replaces only profiles owned by that subscription;
- `delete` removes subscription metadata and, by default, removes profiles owned by that subscription;
- `delete --keep-profiles` removes only subscription metadata and leaves imported profiles in the profile store;
- `delete` must ask for interactive TTY confirmation unless `--yes` is passed;
- `delete` must fail with exit code `2` in non-interactive mode unless `--yes` is passed;
- successful import/update persists the detected format, imported profile IDs, and last update time in subscription metadata;
- failed update preserves last known good imported profiles and subscription metadata;
- failed delete preserves existing subscription metadata and profile state;
- unsupported entries are reported clearly when at least one supported profile is imported;
- responses with no supported profiles fail clearly and leave existing state unchanged;
- `list` and `show` output must redact subscription URLs while showing persisted format, profile count, and last update time;
- `delete` output must redact subscription URLs, profile IDs, credentials, provider tokens, and secret-looking values.

`subscription update --json` and `subscription delete --json` are deferred. Until implemented, they must fail fast as invalid usage with exit code `2`.

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
- typed nftables chain/rule desired state for apply, verify, rollback, and recover behavior;
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

Implemented `connect --mode tun` preview behavior:

- daemon-owned transaction file is written before any TUN mutation;
- current host networking is captured through the read-only snapshot model;
- existing TUN full-tunnel planner output is used as desired state;
- executor creates the TunWarden-owned TUN interface;
- executor adds, never replaces, planned routes;
- executor adds, never treats pre-existing rules as owned, planned policy rules;
- executor applies systemd-resolved per-link DNS from `TunDNSPlan.Servers` with `resolvectl dns`, `resolvectl domain '~.'`, and `resolvectl default-route yes`;
- executor applies, verifies, and rolls back TunWarden-owned nftables state for `table inet tunwarden`;
- TUN-mode Xray runtime config is generated under the daemon runtime directory;
- daemon starts Xray with the TUN-mode runtime config and refuses to start it as root;
- daemon starts the `tun2socks` adapter against the planned TUN device and private SOCKS endpoint;
- route verify checks the destination plus expected device and gateway where applicable;
- policy-rule verify checks the expected selector and lookup table;
- DNS verify checks planned DNS servers and route-only domain `~.`;
- nftables verify checks the TunWarden-owned table, chains, and rules;
- pre-commit connectivity verification checks the full-tunnel route and routed TCP path;
- transaction commits only after network apply/verify, Xray startup verification, TUN adapter startup verification, and connectivity verification succeed;
- apply, verify, startup, connectivity, or transition failure rolls back only the steps actually applied by the transaction, including DNS via `resolvectl revert` and nftables table cleanup when those steps were applied;
- `disconnect` rolls back the active TunWarden-owned TUN transaction and is safe to repeat;
- status exposes transaction state, rollback availability, cleanup requirement, redacted transaction path, DNS desired state, and firewall state.

Current `connect --mode tun` limitations:

- it is a safe TUN preview, not a stable laptop VPN release;
- it does not claim stable full leak protection until the v0.2 manual acceptance gate records conclusive Tier 1 evidence;
- it supports systemd-resolved DNS only; non-systemd fallback and user DNS configuration are future work;
- it requires daemon process privileges equivalent to `CAP_NET_ADMIN` for TUN, route, policy-rule, DNS, and nftables mutation;
- packaged privileged daemon deployment, suspend/resume handling, Wi-Fi roaming, reconnect loops, and NetworkManager event handling are still deferred.

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
tunwarden recover --execute --yes --json
```

Purpose: inspect and explicitly clean up stale TunWarden-owned volatile state.

Daemon requirement:

- `recover`: none; local read-only scanner only;
- `recover --execute --yes`: required through `tunwardend` local Unix socket API.

Mutation level:

- `recover`: read-only dry-run;
- `recover --execute --yes`: daemon-owned explicit cleanup of clearly TunWarden-owned volatile state only.

Implemented behavior:

- `recover` remains dry-run and never mutates state;
- `recover --execute --yes` sends cleanup intent to `tunwardend`; the CLI does not perform privileged host cleanup;
- interactive execute prompts for `yes` unless `--yes` is passed;
- non-interactive execute requires `--yes`;
- JSON execute requires `--yes`;
- execute reports `recovered`, `skipped`, and `failed` cleanup results;
- failed cleanup returns exit code `1` and JSON `status: "fail"`;
- incomplete cleanup where transaction state is preserved returns exit code `1` and JSON `status: "warn"`;
- ambiguous resources are reported as `skipped` and left unchanged;
- runtime root cleanup is intentionally unsupported; `/run/tunwarden` is not deleted wholesale;
- stale PID metadata is not sufficient process identity and is not signalled by recovery;
- pending, failed, rolling-back, or stale transaction files under `/run/tunwarden/transactions/` are shown as recovery candidates;
- transaction candidates include state, rollback availability, cleanup requirement, and redacted transaction path;
- invalid or unreadable transaction files are reported as inspection warnings, not ignored.

Expected cleanup candidates:

- clearly TunWarden-owned generated runtime configs;
- clearly TunWarden-owned TUN interfaces;
- clearly TunWarden-owned nftables state;
- TunWarden-owned routes/rules actually recorded in transaction rollback metadata;
- TunWarden-owned DNS state recorded in transaction rollback metadata where reversible;
- transaction state files only after the cleanup sequence completes safely.

## 4. Milestone boundaries

The current implementation contains:

- proxy-only lifecycle for Xray;
- local import for VLESS Xray JSON, plain URI-list, and Base64 URI-list files through `tunwarden import <local-path>`;
- subscription import/update/delete for Base64 URI-list and Xray JSON responses over `file://`, `http://`, and `https://` sources;
- profile validation for normalized profile state and selected proxy-only/TUN Xray renderability;
- read-only full-tunnel TUN planning;
- static shell completion generation for bash, zsh, and fish;
- transaction-state persistence and diagnostics;
- daemon-owned privileged TUN preview execution for TUN interface, routes, policy rules, systemd-resolved DNS, TunWarden-owned nftables state, TUN-mode Xray runtime config, Xray startup, TUN adapter startup, and pre-commit route/TCP verification;
- daemon-owned recovery cleanup execution for clearly TunWarden-owned volatile state;
- DNS desired-state persistence in transaction state, including planned servers.

Still deferred:

- import JSON output and JSON outbound import for VMess/Trojan/Shadowsocks;
- dynamic shell completion for user-specific values such as profile IDs, subscription IDs, and runtime state;
- stable leak-protection release claim until v0.2 acceptance evidence is recorded;
- configurable DNS policy and non-systemd DNS fallback;
- packaged privileged daemon deployment;
- reconnect/suspend/resume state machine;
- GUI.
