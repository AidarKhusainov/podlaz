# Recovery dry-run

This document defines the implemented v0.1 local dry-run scan for `tunwarden recover`.

The command name, arguments, exit codes, stdout/stderr rules, JSON compatibility, and milestone boundaries are owned by [CLI contract](./cli.md). This document owns the implemented read-only recovery candidate detection set and its safety boundary.

## Safety boundary

`tunwarden recover` is strictly read-only in v0.1.

It must not:

- require root privileges;
- create or delete TUN interfaces;
- add, remove, or replace routes;
- change DNS configuration;
- create, modify, flush, or delete nftables state;
- remove runtime files or generated configs;
- stop, start, or signal processes or services.

It may inspect clearly TunWarden-owned resource names and paths through read-only commands and filesystem metadata checks.

Implemented host inspections:

```bash
ip link show dev tunwarden0
nft list table inet tunwarden
```

Implemented filesystem metadata checks:

```text
/run/tunwarden/generated
/run/tunwarden
```

The command does not read generated config contents because generated core configs may contain sensitive material.

## Human output contract

The default human report starts with:

```text
TunWarden recovery dry-run
```

When recovery candidates are found, each candidate is rendered as:

```text
Would recover <resource kind>: <owned target>
```

Example:

```text
TunWarden recovery dry-run
Would recover TUN interface: tunwarden0
Would recover nftables table: inet tunwarden
Would recover generated runtime configs: /run/tunwarden/generated
Would recover runtime directory: /run/tunwarden
No changes were applied.
```

When all implemented inspections complete and no recovery candidates are found, the command prints:

```text
TunWarden recovery dry-run
No TunWarden-owned recovery candidates found.
No changes were applied.
```

If a read-only inspection cannot complete, the command appends a warning without mutating the host:

```text
Warning: could not inspect <target>: <reason>
```

Warnings mean the dry-run had incomplete visibility. They are not cleanup actions, and warning-only output must not claim that no TunWarden-owned recovery candidates were found.

## v0.1 recovery candidates

The v0.1 scan is intentionally narrow. It reports only clearly TunWarden-owned resources.

| Resource | Detection | Candidate output |
| --- | --- | --- |
| TUN interface | `ip link show dev tunwarden0` succeeds | `Would recover TUN interface: tunwarden0` |
| nftables table | `nft list table inet tunwarden` succeeds | `Would recover nftables table: inet tunwarden` |
| Generated runtime configs | `/run/tunwarden/generated` exists | `Would recover generated runtime configs: /run/tunwarden/generated` |
| Runtime directory | `/run/tunwarden` exists | `Would recover runtime directory: /run/tunwarden` |

Absent resources are treated as healthy only when the corresponding inspection completes successfully enough to distinguish absence from incomplete visibility.

The scan must not infer ownership from vague patterns. It must not scan arbitrary interfaces, nftables tables, routes, DNS settings, user home directories, or non-documented paths. Future recovery candidates need documented TunWarden ownership markers before they are added.

## Deferred execution mode

`recover --execute --yes` is intentionally not implemented in v0.1 and must fail as invalid usage.

Actual cleanup remains deferred until the safe TUN milestone, where cleanup can be implemented behind the documented daemon-owned transaction and recovery model.
