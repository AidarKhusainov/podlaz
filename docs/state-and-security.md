# State and Security Requirements

This document owns TunWarden state layout, output redaction, daemon hardening, and core process safety rules.

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

doctor:
  checks

plan:
  mode
  plan
  steps
  rollback_steps
```

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

Implemented v0.1 service behavior:

- `packaging/systemd/tunwardend.service` starts `tunwardend` as `root:tunwarden` so the daemon can own `/run/tunwarden` and expose a group-readable Unix socket.
- The dedicated `tunwarden` group is the packaged socket access boundary for read-only CLI commands.
- `RuntimeDirectory=tunwarden` with `RuntimeDirectoryMode=0750` keeps `/run/tunwarden` accessible only to root and the `tunwarden` group.
- The daemon itself applies socket mode `0660` to `/run/tunwarden/tunwardend.sock`.
- `StateDirectory=tunwarden` reserves `/var/lib/tunwarden` for future daemon-owned persistent state, but v0.1 does not write persistent daemon state yet.
- `StandardOutput=journal` and `StandardError=journal` make daemon logs visible through `journalctl -u tunwardend`.

Current v0.1 hardening baseline:

```ini
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

Notes:

- v0.1 performs service startup only and must not mutate TUN, route, DNS, nftables, firewall, or core process state.
- The service intentionally grants no capabilities in v0.1. Add `CAP_NET_ADMIN` only when a later issue implements and documents daemon-owned TUN, route, DNS, or firewall mutations.
- Add `CAP_NET_RAW` only if a concrete health check or networking feature needs it and the PR documents why.
- Broad file permission bypass capabilities must not be in the baseline.
- `PrivateDevices=yes`, restrictive address-family filters, and kernel-tunable protections are deferred because they can conflict with future `/dev/net/tun`, netlink, routing, or nftables work and must be validated together with those features.
- Privileged daemon release is blocked until the unit file documents the final hardening choices and justifies deviations from the documented baseline.

## 6. Core engine process safety

The core engine process is a child process managed by the daemon, not the owner of TunWarden system state.

Rules:

- The core process must not inherit broad daemon privileges unless strictly required.
- Generated core configs must be mode `0600`.
- Generated core configs must be written atomically.
- Generated core configs must be treated as runtime output, not persistent source of truth.
- Generated core configs must not be printed or logged in full by default.
- The daemon must be able to stop the core process and explain core process failures through `status`, `doctor`, and `logs`.
