# Status command

This document defines the implemented behavior for `podlaz status`.

The command name, arguments, exit codes, stdout and stderr rules, JSON compatibility, and milestone boundaries are owned by [CLI contract](./cli.md). The daemon transport is owned by [Daemon local API](./daemon-api.md). Daemon socket fallback classification is owned by [Status daemon socket classification](./status-daemon-socket.md). This document owns the current read-only status behavior and its safety boundary.

## Safety boundary

`podlaz status` is strictly read-only.

It may query the local daemon Unix socket. If the daemon is not reachable, it may inspect local podlaz runtime path metadata as a conservative fallback.

It must not mutate host networking, daemon process lifecycle, core process lifecycle, user profiles, subscriptions, runtime file contents, TUN devices, routes, DNS settings, nftables objects, or firewall rules.

## Human output contract

The default human report starts with `podlaz status`.

When the daemon is reachable, daemon-backed output reports:

- `Daemon: running` from the daemon process itself;
- `Service: manual` when `podlazd` is run directly, or `Service: systemd` when it is started by the repository systemd unit;
- current connection, active mode, proxy, TUN, route, DNS, firewall, transaction, and startup recovery scan state from the daemon;
- `Runtime directory: present` because the daemon owns `/run/podlaz/` while running;
- `Stale state: none` when the daemon reports no recovery candidates or warnings.

When the daemon is not reachable, the command prints actionable daemon guidance and clearly states that local fallback is being used.

The local fallback reports:

- `Daemon: not reachable (...) ; using local fallback` when daemon access failed;
- `Service: none` because no daemon supervisor is reachable in the fallback snapshot;
- `Daemon socket: missing` when the socket path does not exist;
- `Daemon socket: present but inaccessible (permission denied; check podlaz group membership)` when the socket exists but the daemon API could not be reached because the caller does not have socket access;
- `Daemon socket: present as non-socket path (stale)` when the socket path exists as an unexpected filesystem object;
- `Connection: inactive` when no stale runtime state is found;
- `Connection: inactive (stale state detected)` when local runtime recovery candidates exist;
- `Connection: unknown (inspection incomplete)` when local runtime state cannot be inspected reliably or may belong to an inaccessible live daemon;
- `Proxy: inactive` because the local fallback does not own daemon process state;
- `TUN: not managed in this build` when the daemon is not available to report managed TUN state.

Daemon unavailability alone is not an unhealthy local status when the fallback can prove a clean inactive state. Stale runtime state or incomplete fallback visibility still returns diagnostic exit code `3`.

## Local runtime inspection

The local fallback inspects only documented podlaz-owned runtime paths:

```text
/run/podlaz/podlazd.sock
/run/podlaz/generated
/run/podlaz/transactions
/run/podlaz
```

The command does not read generated config contents.

When the daemon socket is missing, status treats `/run/podlaz`, `/run/podlaz/generated`, and stale transaction state as local recovery candidates where applicable.

When the daemon socket exists but daemon API access failed with permission denied, status does not classify `/run/podlaz`, generated runtime configs, or transaction files as stale fallback candidates. They may belong to a live daemon that the caller cannot inspect. Instead, status reports incomplete visibility and guides the user to fix packaged `podlaz` group membership or socket ownership/mode.

When the daemon socket path exists but is not a Unix socket, status reports that socket path as a stale recovery candidate.

If a read-only inspection cannot complete, the command reports incomplete visibility instead of claiming a clean inactive host.

Warnings mean the status snapshot had incomplete visibility. Warning-only output must not claim that stale state is absent.

## Manual systemd verification

After installing the daemon binary and `packaging/systemd/podlazd.service` as described by [Daemon local API](./daemon-api.md), verify daemon-backed status with:

```bash
sudo systemctl start podlazd
systemctl status podlazd --no-pager
podlaz status
journalctl -u podlazd -n 50 --no-pager
```

A successful daemon-backed service run should include:

```text
podlaz status
Daemon: running
Service: systemd
Connection: inactive
Runtime directory: present
Proxy: inactive
TUN: disabled
Stale state: none
```

The user running `podlaz status` must have access to `/run/podlaz/podlazd.sock`, normally by being a member of the `podlaz` group. Without socket access, the command must keep the conservative local fallback behavior instead of requiring root, but it must not report potentially live daemon-owned runtime state as stale-only cleanup.

## Redaction

Status output must use the shared podlaz redaction policy from [State and security requirements](./state-and-security.md).

## Deferred behavior

`status --json` is intentionally not implemented in this PR and currently fails as invalid usage.
