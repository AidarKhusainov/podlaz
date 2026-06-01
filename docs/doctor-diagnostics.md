# Doctor diagnostics

This document defines the implemented v0.1 local diagnostics for `tunwarden doctor`.

The command name, flags, exit codes, stdout/stderr rules, and JSON compatibility are owned by [CLI contract](./cli.md). This document owns the current read-only Linux diagnostic set and its safety boundaries.

## Safety boundary

`tunwarden doctor` is strictly read-only.

It must not:

- require root privileges;
- create or delete TUN interfaces;
- add, remove, or replace routes;
- change DNS configuration;
- create, modify, or delete nftables state;
- stop, start, or signal system services.

It may inspect host state through read-only commands and filesystem checks.

## Human output contract

The default human report starts with:

```text
TunWarden doctor report
```

The v0.1 check order is stable enough for tests:

1. `platform`
2. `iproute2`
3. `default-route`
4. `default-interface`
5. `networkmanager`
6. `systemd`
7. `resolved`
8. `nftables`
9. `stale-resources`

Each check uses one of these severities:

- `OK`: the check completed and did not find an unhealthy condition;
- `WARN`: the check completed with missing optional tooling, missing optional state, or incomplete visibility;
- `FAIL`: a required diagnostic command failed in a way that makes the result unreliable.

Missing host tools must produce `WARN` results instead of panics or crashes.

## v0.1 checks

### `platform`

Reports the current Go platform as `GOOS/GOARCH`.

Linux is `OK`. Other platforms are `WARN` because TunWarden is Linux-first.

### `iproute2`

Detects `ip` with the command runner's path lookup.

When `ip` is missing, route, default-interface, and TUN stale-resource checks degrade to `WARN`.

### `default-route`

Runs:

```bash
ip route show default
```

The first non-empty line is reported as the default route.

A command execution failure is represented as a `FAIL` diagnostic result.

### `default-interface`

Parses the `dev <interface>` field from the reported default route.

If a default route exists but no `dev` field can be parsed, the route check remains `OK` and `default-interface` becomes `WARN`.

### `networkmanager`

Detects `nmcli` availability.

NetworkManager is part of the Tier 1 target environment, but missing `nmcli` is a `WARN`, not a crash.

### `systemd`

Detects `systemctl` availability.

The check does not start, stop, reload, or query service state in v0.1.

### `resolved`

Detects `resolvectl` availability.

The check does not change DNS configuration.

### `nftables`

Detects `nft` availability.

The check may inspect TunWarden-owned nftables table presence but must not create, flush, or delete nftables state.

### `stale-resources`

Detects known TunWarden-owned resource names:

- interface `tunwarden0` through `ip link show dev tunwarden0`;
- nftables table `inet tunwarden` through `nft list table inet tunwarden`;
- runtime path `/run/tunwarden` through filesystem metadata.

Absent resources are healthy. Existing TunWarden-owned resources are reported as `WARN` until cleanup behavior is explicitly implemented in a later milestone.
