# Recovery dry-run and execute

This document defines the implemented recovery behavior for `podlaz recover`.

The command name, arguments, exit codes, stdout/stderr rules, JSON compatibility, and milestone boundaries are owned by [CLI contract](./cli.md). This document owns the implemented recovery candidate detection set, startup recovery scan visibility, and the cleanup safety boundary.

## Safety boundary

`podlaz recover` without `--execute` is strictly read-only.

It must not:

- require root privileges;
- create or delete TUN interfaces;
- add, remove, or replace routes;
- change DNS configuration;
- create, modify, flush, or delete nftables state;
- remove runtime files or generated configs;
- stop, start, or signal processes or services.

`podlaz recover --execute --yes` is an explicit cleanup command. The CLI still does not perform privileged host mutations itself: it sends the cleanup intent to `podlazd` through the local daemon API, and the daemon-owned recovery executor performs the scoped cleanup.

Execution must be safe to repeat. Missing resources are treated as already recovered when absence can be identified clearly.

Execution must not touch resources that are not clearly podlaz-owned. Ambiguous resources are reported as `skipped` and left unchanged.

The daemon recovery executor intentionally does not remove the runtime root `/run/podlaz`. It may remove documented stale children such as generated runtime configs and transaction files when they are clearly inside the podlaz runtime layout.

The daemon recovery executor intentionally does not stop a process based only on a stale PID from rollback metadata. PID reuse makes that ambiguous. Such child-process rollback entries are reported as skipped until a future daemon-supervised identity check exists.

## Daemon startup recovery scan

On startup, `podlazd` first establishes the single-owner daemon boundary by acquiring the daemon lock. Only after the lock is acquired does it run the same read-only recovery scanner and expose the local daemon API. A second daemon process that cannot acquire the lock must fail before scanning or logging another daemon's runtime state.

The startup scan result is captured in memory and exposed through daemon-backed `podlaz status`, `podlaz doctor`, and `podlaz recover` dry-run output.

The startup scan is observational only. It must not mutate host networking, generated files, process state, transaction files, TUN devices, routes, policy rules, DNS settings, or nftables state.

Daemon status includes a redacted `startup_scan` object in JSON API responses. Human status renders it as:

```text
Startup recovery scan: stale state found
Pending transaction: tx-startup
Suggested action: podlaz recover
```

Daemon doctor includes a `startup-recovery-scan` check. Clean startup is reported as `OK`; stale candidates or incomplete inspection are reported as `WARN` with an actionable suggested command.

Plain `podlaz recover` merges the daemon startup scan with the current local read-only scan when the daemon is reachable. This preserves restart-time evidence while the evidence is still current. Duplicate candidates are collapsed by kind, target, and transaction id.

After `podlaz recover --execute --yes` completes, `podlazd` refreshes the cached startup scan from the current read-only recovery plan before serving later status, doctor, or recover dry-run requests. Already recovered resources must not remain user-visible as executable cleanup candidates.

## Implemented host inspections

```bash
ip link show dev podlaz0
nft list table inet podlaz
```

Implemented filesystem metadata checks:

```text
/run/podlaz/generated
/run/podlaz/transactions/*.json
```

The command does not read generated config contents because generated core configs may contain sensitive material.

## Human dry-run output contract

The default human report starts with:

```text
podlaz recovery dry-run
```

When recovery candidates are found, each non-transaction candidate is rendered as:

```text
Would recover <resource kind>: <owned target>
```

Transaction candidates are rendered with structured state details:

```text
Transaction: pending apply
Rollback available: yes
State path: /run/podlaz/transactions/tx-apply.json
```

Example:

```text
podlaz recovery dry-run
Would recover TUN interface: podlaz0
Would recover nftables table: inet podlaz
Would recover generated runtime configs: /run/podlaz/generated
Transaction: pending apply
Rollback available: yes
State path: /run/podlaz/transactions/tx-apply.json
No changes were applied.
```

When all implemented inspections complete and no recovery candidates are found, the command prints:

```text
podlaz recovery dry-run
No podlaz-owned recovery candidates found.
No changes were applied.
```

If a read-only inspection cannot complete, the command appends a warning without mutating the host:

```text
Warning: could not inspect <target>: <reason>
```

Warnings mean the dry-run had incomplete visibility. They are not cleanup actions, and warning-only output must not claim that no podlaz-owned recovery candidates were found.

## Execute output contract

Execute mode starts with:

```text
podlaz recovery
Mode: execute
```

Each attempted cleanup action is reported as one of:

```text
Recovered <resource kind>: <target>
Skipped <resource kind>: <target> (<reason>)
Failed to recover <resource kind>: <target>: <reason>
```

The report ends with:

```text
No non-podlaz resources were touched.
```

`--json` execute output uses the common JSON shape plus `mode: "execute"` and a `recovery` array of redacted cleanup results.

## Recovery candidates

The scan is intentionally narrow. It reports only clearly podlaz-owned resources.

| Resource | Detection | Dry-run output |
| --- | --- | --- |
| TUN interface | `ip link show dev podlaz0` succeeds | `Would recover TUN interface: podlaz0` |
| nftables table | `nft list table inet podlaz` succeeds | `Would recover nftables table: inet podlaz` |
| Generated runtime configs | `/run/podlaz/generated` exists | `Would recover generated runtime configs: /run/podlaz/generated` |
| Transaction state | stale transaction under `/run/podlaz/transactions/` | structured transaction details |

Absent resources are treated as healthy only when the corresponding inspection completes successfully enough to distinguish absence from incomplete visibility.

The scan must not infer ownership from vague patterns. It must not scan arbitrary interfaces, nftables tables, routes, DNS settings, user home directories, or non-documented paths. Future recovery candidates need documented podlaz ownership markers before they are added.

## Confirmation behavior

`recover --execute` follows the global confirmation contract:

- interactive TTY: prompts for `yes` unless `--yes` is passed;
- non-interactive mode: fails unless `--yes` is passed;
- `--json` execute mode: fails unless `--yes` is passed.

Plain `podlaz recover` remains dry-run and never prompts.
