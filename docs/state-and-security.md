# State and security

This is the only permanent engineering reference for state ownership, redaction,
daemon privilege boundaries, and privileged networking safety.

## State ownership

| State | Location | Owner |
| --- | --- | --- |
| User config/state/cache | `$XDG_CONFIG_HOME/podlaz`, `$XDG_STATE_HOME/podlaz`, `$XDG_CACHE_HOME/podlaz` | invoking user |
| Daemon runtime | `/run/podlaz` | `podlazd` via systemd `RuntimeDirectory=` |
| Daemon persistent state | `/var/lib/podlaz` | `podlazd` via systemd `StateDirectory=` |
| Transaction files | `/run/podlaz/transactions/*.json` | `podlazd` |
| Generated runtime config | `/run/podlaz/generated/` | `podlazd` and the dedicated core child identity |
| TUN packet ingestion | `podlaz0` native Xray `tun` inbound | Xray child process, orchestrated by `podlazd` |

Rules:

- User profile/subscription state must not require root.
- Runtime config is generated output, not persistent source of truth.
- Transaction files must be atomic, private, versioned, and redaction-safe.
- Read-only commands may inspect state but must not clean it up.

## CLI and daemon boundary

- The CLI parses user intent and talks to the local daemon API.
- The CLI must not be SUID and must not directly mutate TUN devices, routes,
  policy rules, DNS, nftables, firewall state, or system resolver files.
- Privileged host changes belong to `podlazd` and must be transaction-backed.
- `proxy-only` must not mutate host networking.
- `tun` execution must record enough rollback metadata to recover after failure
  or daemon restart.

Packaged daemon access has two local socket boundaries. The filesystem socket is tried first. A transport-level permission failure may fall back to the packaged abstract socket, where the daemon can enforce peer-credential/polkit authorization without widening filesystem socket access. Once the daemon returns an HTTP, authorization, JSON, or schema error, that response is authoritative and must not be downgraded to a generic daemon-unavailable error.

## Networking safety

TUN mode may touch only podlaz-owned networking state around the Xray-owned TUN link:

- podlaz-owned routes and policy rules;
- podlaz-owned DNS link state;
- podlaz-owned nftables/firewall table, chains, and rules.

Xray owns `podlaz0` packet ingestion through the native `tun` inbound. `podlazd` may verify that `podlaz0` exists before applying host networking, but it must not record Xray-created `podlaz0` as a podlaz-owned TUN device rollback target. Stopping Xray is the release mechanism for the Xray-owned TUN link.

Apply/verify/rollback must be explicit. Rollback must remove only what the active
transaction actually applied. Ambiguous host state must be skipped, not guessed.

For `systemd-resolved`, verification must check the target link, planned DNS servers, the `~.` route-only domain, the configured DNS default-route flag, and absence of a foreign active `~.` owner. `Current Scopes` is derived runtime state and must not be used as proof that podlaz's per-link configuration was or was not applied; it may remain `none` while the expected configuration is present.

For native Xray TUN startup, durable rollback order is:

1. roll back podlaz-owned nftables, DNS, routes, and policy rules;
2. stop the Xray child process;
3. verify or surface stale `podlaz0` state through recovery/status diagnostics.

## Recovery

- `podlaz recover` is read-only.
- `podlaz recover --execute --yes` sends cleanup intent to `podlazd`.
- The CLI must not perform privileged cleanup directly.
- Recovery may clean only clearly podlaz-owned volatile state.
- `/run/podlaz` must not be deleted wholesale.
- Stale PID metadata alone is not enough to signal a process.
- Generated configs must be recorded in transaction rollback metadata before they are written, including Xray TUN preflight configs.
- Stale `systemd-resolved` link records for missing `podlaz0` may be reverted with `resolvectl revert podlaz0`; recovery must not silently restart `systemd-resolved` and must tell the user when a manual restart is still required.
- A non-zero `resolvectl status` or `resolvectl revert` result stating that `podlaz0` is already missing is idempotent for stale-link cleanup, transaction recovery, and runtime DNS rollback. This classification is specific to `resolvectl`; generic command missing-resource classification remains unchanged. Stale-link cleanup must continue with post-revert inspection and report `skipped` with manual restart guidance when a resolved record still persists.

## Redaction

Human and JSON output must redact secrets and generated runtime configuration.
This applies to `status`, `doctor`, `logs`, `plan`, `recover`, validation output,
and all JSON responses.

JSON output must include `schema_version`. Existing JSON field meanings must not
change without an explicit compatibility note.

## Confirmation

Commands that remove user state or execute recovery cleanup must require
confirmation:

- interactive TTY: prompt unless `--yes` is passed;
- non-interactive mode: fail unless `--yes` is passed;
- JSON mode: fail unless `--yes` is passed.

High-impact flags such as `--execute` and `--yes` are long-only.

## Packaged service baseline

The packaged daemon runs as `root:podlaz` because TUN mode and recovery need
privileged networking operations. The CLI remains unprivileged and uses the
socket access boundary.

Expected systemd baseline:
