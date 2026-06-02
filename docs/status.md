# Status command

This document defines the implemented v0.1 behavior for `tunwarden status`.

The command name, arguments, exit codes, stdout and stderr rules, JSON compatibility, and milestone boundaries are owned by [CLI contract](./cli.md). The daemon transport is owned by [Daemon local API](./daemon-api.md). This document owns the current read-only status behavior and its safety boundary.

## Safety boundary

`tunwarden status` is strictly read-only in v0.1.

It may query the local daemon Unix socket. If the daemon is not reachable, it may inspect local TunWarden runtime path metadata as a conservative fallback.

It must not mutate host networking, daemon process lifecycle, core process lifecycle, user profiles, subscriptions, or runtime file contents.

## Human output contract

The default human report starts with `TunWarden status`.

When the daemon is reachable, v0.1 daemon-backed output reports:

- `Daemon: running` from the daemon process itself;
- `Service: manual` when `tunwardend` is run directly, or `Service: systemd` when it is started by the repository systemd unit;
- `Connection: inactive` because connection lifecycle is not implemented yet;
- `Runtime directory: present` because the daemon owns `/run/tunwarden/` while running;
- `Proxy: inactive` because no proxy process lifecycle exists yet;
- `TUN: disabled` because TUN mode is outside the current milestone;
- `Stale state: none` for the current conservative daemon snapshot.

When the daemon is not reachable, the command prints actionable daemon guidance and clearly states that local fallback is being used.

The local fallback reports:

- `Daemon: not reachable (...) ; using local fallback` when daemon access failed;
- `Service: none` because no daemon supervisor is reachable in the fallback snapshot;
- `Connection: inactive` when no stale runtime state is found;
- `Connection: inactive (stale state detected)` when local runtime recovery candidates exist;
- `Connection: unknown (inspection incomplete)` when local runtime state cannot be inspected reliably;
- `Proxy: inactive` because no proxy process lifecycle exists yet;
- `TUN: not managed in this build` because TUN mode is outside the current milestone.

Daemon unavailability alone is not an unhealthy local status when the fallback can prove a clean inactive state. Stale runtime state or incomplete fallback visibility still returns diagnostic exit code `3`.

## v0.1 local runtime inspection

The v0.1 local fallback inspects only documented TunWarden-owned runtime paths:

```text
/run/tunwarden/generated
/run/tunwarden
```

The command does not read generated config contents.

Status treats `/run/tunwarden` or `/run/tunwarden/generated` as stale local runtime state and prints recovery guidance.

If a read-only inspection cannot complete, the command reports incomplete visibility instead of claiming a clean inactive host.

Warnings mean the status snapshot had incomplete visibility. Warning-only output must not claim that stale state is absent.

## Manual systemd verification

After installing the daemon binary and `packaging/systemd/tunwardend.service` as described by [Daemon local API](./daemon-api.md), verify daemon-backed status with:

```bash
sudo systemctl start tunwardend
systemctl status tunwardend --no-pager
tunwarden status
journalctl -u tunwardend -n 50 --no-pager
```

A successful daemon-backed service run should include:

```text
TunWarden status
Daemon: running
Service: systemd
Connection: inactive
Runtime directory: present
Proxy: inactive
TUN: disabled
Stale state: none
```

The user running `tunwarden status` must have access to `/run/tunwarden/tunwardend.sock`, normally by being a member of the `tunwarden` group. Without socket access, the command must keep the conservative local fallback behavior instead of requiring root.

## Redaction

Status output must use the shared TunWarden redaction policy from [State and security requirements](./state-and-security.md).

## Deferred behavior

`status --json` is intentionally not implemented in this PR and currently fails as invalid usage.

Daemon-backed active profile, active mode, proxy listener, core process, and connection health status remain deferred until process lifecycle work exists.
