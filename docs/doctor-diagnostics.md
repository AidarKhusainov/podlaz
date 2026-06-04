# Doctor diagnostics

This document defines the implemented v0.1 diagnostics for `tunwarden doctor`.

The command name, flags, exit codes, stdout/stderr rules, and JSON compatibility are owned by [CLI contract](./cli.md). This document owns the current read-only Linux diagnostic set, explicit local Xray binary validation, and their safety boundaries.

## Safety boundary

`tunwarden doctor` is strictly read-only.

It must not:

- require root privileges;
- create or delete TUN interfaces;
- add, remove, or replace routes;
- change DNS configuration;
- create, modify, or delete nftables state;
- stop, start, or signal system services;
- download or install Xray;
- start a long-running Xray process.

It may inspect host state through read-only commands and filesystem checks. `doctor --core --xray <path>` may run the short-lived `xray version` command for the explicitly provided local binary.

When `tunwardend` is reachable, the CLI requests the default report through the local daemon API. When the daemon is missing, inaccessible, times out, or returns an invalid response, the CLI falls back to the same local read-only diagnostic model and prints a daemon warning.

`doctor --core --xray <path>` is currently local-only in v0.1. It validates the provided Xray binary before proxy-only connection work starts using it.

## Human output contract

The default human report starts with:

```text
TunWarden doctor report
Source: daemon|local fallback
```

Daemon-backed reports include:

```text
[OK] daemon: running
```

Local fallback reports include a daemon warning before local checks, for example:

```text
[WARN] daemon: daemon socket /run/tunwarden/tunwardend.sock does not exist; start tunwardend
```

The core report starts with:

```text
TunWarden core diagnostics
[OK] xray: /usr/local/bin/xray is executable
[OK] xray-version: Xray ...
[WARN] config-test: not checked
```

The v0.1 local check order after any daemon reachability check is stable enough for tests:

1. `platform`
2. `iproute2`
3. `default-route`
4. `default-interface`
5. `networkmanager`
6. `systemd`
7. `resolved`
8. `nftables`
9. `stale-resources`

The v0.1 core check order is:

1. `xray`
2. `xray-version`
3. `config-test`

Each check uses one of these severities:

- `OK`: the check completed and did not find an unhealthy condition;
- `WARN`: the check completed with missing optional tooling, missing optional state, incomplete visibility, daemon fallback, or a deliberately deferred optional core check;
- `FAIL`: a required diagnostic command failed in a way that makes the result unreliable.

Missing host tools must produce `WARN` results instead of panics or crashes. Missing, non-executable, or unusable explicitly provided Xray binaries must produce `FAIL` results and diagnostic exit code `3`.

## JSON output

`doctor --core --xray <path> --json` is implemented with the common top-level shape:

```json
{
  "schema_version": "v1",
  "status": "ok|warn|fail",
  "warnings": [],
  "errors": [],
  "source": "local core",
  "checks": []
}
```

Human and JSON output both use the shared redaction policy. `doctor --json` without `--core` is still deferred and fails fast with exit code `2`.

## v0.1 checks

### `daemon`

Reports whether diagnostics came from the daemon-backed path.

When daemon diagnostics are available, this check is `OK` with message `running`.

When daemon diagnostics are unavailable, this check is `WARN` and includes an actionable connection, timeout, or daemon protocol fallback message. Local diagnostics continue after this warning.

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

Absent resources are healthy. Existing TunWarden-owned resources are reported as `WARN` until cleanup behavior is explicitly implemented in a later milestone. When diagnostics run through a live daemon, the daemon-owned runtime directory itself is not treated as stale merely because it exists.

### `xray`

Validates the explicit `--xray <path>` argument for `doctor --core`.

The check is `OK` when the path exists, is not a directory, and has at least one executable bit set. Missing, directory, non-executable, or uninspectable paths are `FAIL` with an actionable message.

### `xray-version`

Runs:

```bash
<xray-path> version
```

The first non-empty stdout or stderr line is reported. A non-zero exit or execution error is `FAIL`.

### `config-test`

Currently reports `WARN: not checked` in v0.1. Config validation/test-mode detection is intentionally deferred until generated runtime config validation is wired into the connection lifecycle.
