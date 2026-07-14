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
- `tun` execution must record enough durable desired and applied state to recover
  after failure or daemon restart.

Packaged daemon access has two local socket boundaries. The filesystem socket is tried first. A transport-level permission failure may fall back to the packaged abstract socket, where the daemon can enforce peer-credential/polkit authorization without widening filesystem socket access. Once the daemon returns an HTTP, authorization, JSON, or schema error, that response is authoritative and must not be downgraded to a generic daemon-unavailable error.

## Networking safety

TUN mode may touch only podlaz-owned networking state around the Xray-owned TUN link:

- podlaz-owned routes and policy rules;
- podlaz-owned DNS link state;
- podlaz-owned nftables/firewall table, chains, and rules.

Xray owns `podlaz0` packet ingestion through the native `tun` inbound. `podlazd` may verify that `podlaz0` exists before applying host networking, but it must not record Xray-created `podlaz0` as a podlaz-owned TUN device rollback target. Stopping Xray is the release mechanism for the Xray-owned TUN link.

Apply/verify/rollback must be explicit. Normal in-process rollback must remove only what the active transaction recorded as applied. If the daemon stops inside the apply crash window before an applied category is persisted, recovery may reconstruct only missing entries in reserved podlaz namespaces from the durable structured `desired_plan`: routing table `51820`, policy priority `10000`, DNS link `podlaz0`, and nftables table `inet podlaz`. Desired-only main-table server bypass state must be inspected but never deleted by assumption; a present route or rule without durable applied ownership evidence keeps the transaction blocked for explicit inspection. Ambiguous host state must be skipped, not guessed.

For `systemd-resolved`, apply first performs a scoped `resolvectl revert podlaz0` and then writes the planned DNS servers, `~.` route-only domain, and DNS default-route setting. An already-missing link is idempotent. If the kernel link exists before `systemd-resolved` registers it, only transient missing-link results from the `dns`, `domain`, and `default-route` commands are retried for a bounded interval of roughly two seconds. Verification checks the target link, planned DNS servers, `~.`, `+DefaultRoute`, and absence of a foreign active `~.` owner. `Current Scopes` is derived runtime state and must not be used as proof that the per-link configuration was or was not applied. Verification accepts a matching complete `podlaz0` record when a stale duplicate record temporarily coexists.

For native Xray TUN startup, durable rollback order is:

1. roll back podlaz-owned nftables, DNS, routes, and policy rules;
2. stop the Xray child process;
3. verify or surface stale `podlaz0` state through recovery/status diagnostics.

## Recovery

- `podlaz recover` is read-only.
- `podlaz recover --execute --yes` sends explicit cleanup intent to `podlazd`.
- The CLI must not perform privileged cleanup directly.
- Recovery may clean only clearly podlaz-owned volatile state.
- `/run/podlaz` must not be deleted wholesale.
- Stale PID metadata alone is not enough to signal a process.
- Generated configs must be recorded in transaction rollback metadata before they are written, including Xray TUN preflight configs.
- For non-interactive `connect --mode tun`, the connect request itself authorizes daemon-owned cleanup of unambiguous stale podlaz state. The daemon must recover, recollect the snapshot, and proceed only when owned state is clean. It must not stop foreign VPNs or remove ambiguous resources under the default `block` policy. `--handoff=ask` performs no automatic cleanup.
- A stale `systemd-resolved` record that cannot be removed while `podlaz0` is absent must not trigger a global resolver restart. Connect may defer only that exact persistent `dns-link` result until Xray has recreated `podlaz0`, then run `resolvectl revert podlaz0` immediately before writing podlaz DNS state. Any other skipped or failed recovery result remains a blocker.
- A non-zero `resolvectl status` or `resolvectl revert` result stating that `podlaz0` is already missing is idempotent for stale-link cleanup, transaction recovery, runtime DNS rollback, and doctor inspection. This classification is specific to `resolvectl`; generic command missing-resource classification remains unchanged.
- Unexpected cleanup errors, foreign ownership, invalid transaction files, incomplete transaction recovery, and unrecorded existing main-table bypass state remain blockers.

## Redaction

Human and JSON output must redact secrets and generated runtime configuration.
This applies to `status`, `doctor`, `logs`, `plan`, `recover`, validation output,
and all JSON responses.

JSON output must include `schema_version`. Existing JSON field meanings must not
change without an explicit compatibility note.

## Confirmation

Commands that remove user state or explicitly execute recovery cleanup must
require confirmation:

- interactive TTY: prompt unless `--yes` is passed;
- non-interactive mode: fail unless `--yes` is passed;
- JSON mode: fail unless `--yes` is passed.

A TUN connect is already an explicit privileged networking mutation request. It may therefore perform the narrowly scoped automatic podlaz-owned recovery described above without a second confirmation prompt. This exception does not authorize foreign VPN handoff, ambiguous cleanup, global `systemd-resolved` restart, or deletion of persistent user state.

High-impact flags such as `--execute` and `--yes` are long-only.

## Packaged service baseline

The packaged daemon runs as `root:podlaz` because TUN mode and recovery need
privileged networking operations. The CLI remains unprivileged and uses the
socket access boundary.

Expected systemd baseline:
