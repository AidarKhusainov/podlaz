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

## Networking safety

TUN mode may touch only podlaz-owned networking state:

- managed TUN interface;
- podlaz-owned routes and policy rules;
- podlaz-owned DNS link state;
- podlaz-owned nftables/firewall table, chains, and rules.

Apply/verify/rollback must be explicit. Rollback must remove only what the active
transaction actually applied. Ambiguous host state must be skipped, not guessed.

## Recovery

- `podlaz recover` is read-only.
- `podlaz recover --execute --yes` sends cleanup intent to `podlazd`.
- The CLI must not perform privileged cleanup directly.
- Recovery may clean only clearly podlaz-owned volatile state.
- `/run/podlaz` must not be deleted wholesale.
- Stale PID metadata alone is not enough to signal a process.

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

```ini
User=root
Group=podlaz
UMask=0077
NoNewPrivileges=yes
CapabilityBoundingSet=CAP_CHOWN CAP_SETUID CAP_SETGID CAP_KILL CAP_NET_ADMIN
AmbientCapabilities=CAP_SETUID CAP_KILL
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryDenyWriteExecute=yes
RuntimeDirectory=podlaz
RuntimeDirectoryMode=0711
StateDirectory=podlaz
StateDirectoryMode=0700
```

Rules:

- Xray/core children must run as the dedicated unprivileged child identity.
- Xray/core children must not inherit daemon networking privileges.
- The daemon socket is the only intentionally group-accessible runtime object.
- Generated configs and transaction files remain daemon-private.
- Any privilege expansion must update this file and the unit in the same PR.
