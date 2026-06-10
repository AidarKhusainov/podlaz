# State and Security Requirements

This document owns TunWarden state layout, output redaction, daemon hardening, recovery safety boundaries, and core process safety rules.

## 1. State ownership model

TunWarden must keep three levels of state separate.

### 1.1 User intent and user state

User intent is owned by the user-facing CLI experience.

Examples:

- imported profiles,
- subscription sources,
- user preferences,
- selected defaults,
- import metadata useful to the user.

Preferred locations:

```text
User config:
  $XDG_CONFIG_HOME/tunwarden/
  default: ~/.config/tunwarden/

User state:
  $XDG_STATE_HOME/tunwarden/
  default: ~/.local/state/tunwarden/

User cache:
  $XDG_CACHE_HOME/tunwarden/
  default: ~/.cache/tunwarden/
```

Rules:

- User files must not require root ownership.
- Profile and subscription source of truth should not be hidden only in daemon-private directories.
- The daemon should receive the selected intent through its local API instead of reading arbitrary user home directories.
- If the daemon later owns shared system-wide profiles, that must be an explicit separate feature.

### 1.2 Daemon runtime and daemon state

Daemon state is owned by `tunwardend`.

Examples:

- active connection state,
- active profile snapshot,
- lock files,
- generated runtime config,
- child process state,
- pending or committed transaction state,
- daemon socket.

Preferred locations:

```text
Runtime:
  /run/tunwarden/

Persistent daemon state:
  /var/lib/tunwarden/

Daemon logs:
  journald first, package logs only if needed later
```

For packaged systemd units, prefer:

```ini
RuntimeDirectory=tunwarden
StateDirectory=tunwarden
```

`LogsDirectory=tunwarden` is intentionally not required while the daemon logs to stdout/stderr and the unit sends those streams to journald. Add a logs directory only when file-based package logs become a real product requirement.

#### Transaction runtime state

The implemented transaction state schema is volatile daemon runtime state and is stored under:

```text
/run/tunwarden/transactions/<id>.json
```

Transaction files must be:

- owned by TunWarden and include `owner: "tunwarden"`;
- versioned with `schema_version: "tunwarden.transaction.v1"`;
- written with file mode `0600` under a daemon-owned runtime directory;
- written atomically with temp-file fsync, rename, and directory fsync;
- safe to scan repeatedly after daemon restart;
- free of persistent secrets.

Transaction files may store non-secret rollback metadata for TunWarden-owned TUN devices, routes, policy rules, DNS state, nftables tables/chains, generated runtime config paths, and child-process labels or PIDs. They must not store share URIs, subscription URLs with provider tokens, passwords, authorization headers, private keys, or provider API tokens.

`status`, `doctor`, and `recover` must be able to explain pending, failed, rolling-back, or otherwise stale transaction state using only redacted summaries. They must not apply cleanup as part of read-only inspection.

#### Recovery cleanup boundary

Recovery has two distinct modes:

- `tunwarden recover` is a read-only scanner and must not mutate host state.
- `tunwarden recover --execute --yes` is an explicit cleanup intent sent by the CLI to `tunwardend`; the CLI must not perform privileged host cleanup directly.

Daemon-owned recovery execution may mutate only clearly TunWarden-owned volatile state. Eligible targets are limited to documented runtime children, generated runtime configs, the managed TUN interface, TunWarden-owned nftables objects, and route, rule, DNS, TUN, and generated-config rollback entries recorded as TunWarden-owned transaction metadata.

Ambiguous rollback metadata must be reported as skipped and left unchanged. A stale PID recorded in transaction metadata is not sufficient process identity; recovery must not signal a process from PID metadata alone unless a future daemon-supervised identity check can prove the process is still the TunWarden-owned child.

The runtime root `/run/tunwarden` must not be removed wholesale. Recovery may remove specific stale owned children only when their paths are proven to be inside the documented TunWarden runtime layout.

Transaction state should be removed only after the cleanup sequence completes safely. If any cleanup action fails or any ambiguous resource is skipped, the transaction file should remain available for later diagnostics or a future safer recovery attempt.

### 1.3 System networking state

System networking state is not persistent application data.

Examples:

- TUN interface,
- routes,
- policy rules,
- DNS link configuration,
- nftables tables and chains.

Rules:

- It must be applied only through daemon-owned transactions.
- It must be identifiable as TunWarden-owned.
- It must be inspectable through `plan`, `status`, `doctor`, and `recover`.
- It must be recoverable without relying on the original CLI process.

## 2. JSON compatibility

JSON output is a public interface starting with v0.1.

Rules:

- Every JSON response must include `schema_version`.
- Existing field names and meanings must not change without a documented compatibility note.
- New fields may be added.
- Consumers must ignore unknown fields.
- Human output and JSON output must apply the same redaction policy.

Common top-level fields:

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
  transactions

doctor:
  checks

plan:
  mode
  plan
  steps
  rollback_steps

recover:
  mode
  recovery
```

Daemon-backed status may include `transactions`. Each item is a redacted summary with stable facts only:

```json
{
  "id": "tx-1",
  "state": "applying",
  "rollback_available": true,
  "requires_cleanup": true,
  "path": "/run/tunwarden/transactions/tx-1.json"
}
```

Human-readable transaction phrases such as `pending apply` are rendered by clients from `state` and cleanup flags; they are not a separate source of truth in the daemon API.

Recovery execute JSON must use `status: "fail"` when cleanup actions fail. It must use `status: "warn"` when recovery is incomplete, for example when transaction state is preserved because ambiguous resources were skipped. A warning status is not a successful full cleanup for automation.

## 3. Output redaction

TunWarden must be observable without leaking sensitive material.

Default human output and `--json` output must redact:

- full subscription URLs,
- full share URIs,
- UUID-like user identifiers,
- passwords and private keys,
- authorization headers,
- generated core config content that includes credentials,
- provider tokens and query parameters that look secret.

Allowed output shape examples:

```text
uuid: abcd…7890
subscription: https://example.com/sub?token=REDACTED
```

Rules:

- Generated core configs must not be logged in full by default.
- Transaction files must reject persistent secret-looking keys and values before they are written.
- `logs`, `doctor`, `status`, `plan`, and `recover` must use the same redaction helpers.
- A future explicit debug mode must document exactly what additional data it reveals.

## 4. Confirmation model

Commands that remove user state or execute recovery cleanup must have consistent confirmation behavior.

Rules:

- In an interactive TTY, ask for confirmation unless `--yes` is passed.
- In non-interactive mode, fail unless `--yes` is passed.
- In `--json` mode, fail unless `--yes` is passed.
- High-impact flags such as `--execute` and `--yes` must not have short aliases.

Examples:

```bash
tunwarden profile delete <profile-id> --yes
tunwarden subscription delete <subscription-id> --yes
tunwarden recover --execute --yes
```

`disconnect` is a normal lifecycle command and should not require confirmation unless a future flag changes its meaning beyond stopping the active connection.

## 5. systemd service hardening

The daemon service must start from least privilege. Every relaxation must be justified in documentation or in comments near the unit file.

Packaged baseline service behavior:

- `packaging/sysusers.d/tunwarden.conf` creates the dedicated unprivileged `tunwarden` daemon service identity and the dedicated `tunwarden-xray` proxy-core child identity.
- `packaging/systemd/tunwardend.service` starts `tunwardend` as `tunwarden:tunwarden` for the packaged proxy-only baseline.
- In the default packaged proxy-only path, Xray child processes inherit the same unprivileged `tunwarden:tunwarden` service identity.
- If a UID 0 daemon deployment is used for proxy-only mode, `tunwardend` must start Xray under the dedicated `tunwarden-xray:tunwarden-xray` execution identity instead of letting the child inherit UID 0.
- For the UID 0 daemon path, generated Xray runtime config is owned by `root:tunwarden-xray`, the generated config directory is mode `0750`, and the generated config file is mode `0640`. This is the documented private equivalent to `0600`: only the daemon/root owner and the dedicated Xray child group can read the generated config.
- The packaged baseline unit grants no ambient or bounding capabilities.
- Proxy-only mode must not grant `CAP_NET_ADMIN`, `CAP_NET_RAW`, broad file capabilities, or ambient capabilities to the daemon or Xray child.
- The dedicated `tunwarden` group is the packaged socket access boundary for CLI commands that use the daemon.
- `RuntimeDirectory=tunwarden` with `RuntimeDirectoryMode=0750` keeps `/run/tunwarden` accessible only to the service identity and the `tunwarden` group.
- The daemon itself applies socket mode `0660` to `/run/tunwarden/tunwardend.sock`.
- `StateDirectory=tunwarden` reserves `/var/lib/tunwarden` for future daemon-owned persistent state, but the current baseline does not write persistent daemon state yet.
- `StandardOutput=journal` and `StandardError=journal` make daemon logs visible through `journalctl -u tunwardend`.

Current hardening baseline:

```ini
User=tunwarden
Group=tunwarden
NoNewPrivileges=yes
CapabilityBoundingSet=
AmbientCapabilities=
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
RuntimeDirectory=tunwarden
RuntimeDirectoryMode=0750
StateDirectory=tunwarden
StateDirectoryMode=0750
```

Privilege status for the current milestone:

- The packaged baseline unit remains unprivileged and does not grant `CAP_NET_ADMIN`.
- Packaged proxy-only mode may start and stop an Xray child process and mutate only daemon-owned generated runtime config state under `/run/tunwarden`.
- Daemon-owned TUN execution and recovery cleanup paths are privileged runtime paths. They require a privileged daemon deployment or future helper with `CAP_NET_ADMIN`-equivalent rights when they mutate TUN devices, routes, policy rules, DNS link state, or nftables/firewall state.
- The CLI must not become a privileged or SUID networking mutator. It may parse arguments, render output, and send intents through the daemon socket only.
- Any packaged service privilege expansion must update this document and the unit file in the same change, including the exact capability set and why each capability is required.
- Add `CAP_NET_RAW` only if a concrete health check or networking feature needs it and the PR documents why.
- Broad file permission bypass capabilities must not be in the baseline.
- `PrivateDevices=yes`, restrictive address-family filters, and kernel-tunable protections are deferred because they can conflict with future `/dev/net/tun`, netlink, routing, or nftables work and must be validated together with those features.
- Privileged daemon release is blocked until the unit file documents the final hardening choices and justifies deviations from the documented baseline.

## 6. Core engine process safety

The core engine process is a child process managed by the daemon, not the owner of TunWarden system state.

Rules:

- The core process must not inherit broad daemon privileges unless strictly required.
- In packaged proxy-only mode, `tunwardend` and Xray both run as the unprivileged `tunwarden` service identity.
- If `tunwardend` is running with UID 0 for proxy-only mode, it must drop the Xray child to the dedicated `tunwarden-xray:tunwarden-xray` identity before starting the process.
- The proxy-only Xray child must not inherit supplementary groups.
- Generated core configs must be mode `0600` for same-user execution, or equivalently private as `root:tunwarden-xray` with directory mode `0750` and file mode `0640` for the UID 0 daemon path.
- Generated core configs must be written atomically.
- Generated core configs must be treated as runtime output, not persistent source of truth.
- Generated core configs must not be printed or logged in full by default.
- The daemon must be able to stop the core process and explain core process failures through `status`, `doctor`, and `logs`.
