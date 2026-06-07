# Logs command

`tunwarden logs` is the implemented v0.1 read-only command for inspecting TunWarden daemon and Xray core logs from journald.

Canonical CLI shape is owned by [CLI contract](./cli.md). This document describes the implemented behavior.

## Command shape

```bash
tunwarden logs
tunwarden logs --follow
tunwarden logs -f
tunwarden logs --daemon
tunwarden logs --core
tunwarden logs --follow --core
tunwarden logs --since "1 hour ago"
tunwarden logs --since -1h
tunwarden logs --since=-30min
```

## Behavior

The command prints a stable header and then streams redacted `journalctl` output from the system journal for the `tunwardend.service` systemd unit.

Daemon log view:

```text
TunWarden daemon logs
Jun 03 12:00:00 host tunwardend[1234]: tunwardend: daemon started
Jun 03 12:00:01 host tunwardend[1234]: tunwardend: status request handled
```

Core log view:

```text
TunWarden core logs
Jun 03 12:00:00 host tunwardend[1234]: tunwardend: core xray started pid=5678 profile=my-vless-profile
Jun 03 12:00:01 host tunwardend[1234]: tunwardend: core xray stderr pid=5678 profile=my-vless-profile: ...
```

Default mode shows recent daemon logs using a bounded `journalctl --system --lines` query. `--follow` and `-f` pass through to `journalctl --follow` for live inspection. `--daemon` is accepted as the explicit daemon-log source and is currently equivalent to the default source.

`--core` reads the same `tunwardend.service` journal but prints only Xray/core lifecycle lines and forwarded Xray stdout/stderr lines marked by the daemon. Xray output is forwarded through the daemon instead of writing separate log files, so packaged proxy-only troubleshooting keeps using journald as the primary logging destination.

If `tunwarden logs --core` finds no recent matching core lines in non-follow mode, it prints a clear guidance line explaining that Xray may be inactive, may have crashed before logging was configured, or journal access may be incomplete. Use `tunwarden status` for daemon state and `tunwarden logs --daemon` for broader lifecycle logs.

`--since <duration>` and `--since=<duration>` are passed to `journalctl --since <duration>` and can use journalctl-compatible values such as `1 hour ago`, `-1h`, or `-30min`.

## Access requirements

`tunwardend.service` is a systemd service unit, so `tunwarden logs` reads the system journal explicitly with `journalctl --system`.

Users must run the command as root or have distribution-specific permission to read the system journal, commonly through groups such as `systemd-journal`, `adm`, or `wheel`. Without that permission, `journalctl` may fail or show incomplete system-unit logs.

## Safety boundary

`logs` is read-only. It must not mutate daemon state, host networking, routes, DNS, nftables, firewall state, runtime files, or user configuration.

## Redaction

Every log line printed by `tunwarden logs` goes through the shared TunWarden human-output redaction helper before it reaches stdout. This keeps logs aligned with the documented redaction policy for status, doctor, logs, plan, and recover output.

Daemon lifecycle and local API request logs intentionally avoid request/response payloads and generated runtime configuration content. Forwarded core stdout/stderr is also redacted before it is written to the daemon journal and again before CLI output.

## Failure behavior

If `journalctl` is missing, the command fails with an actionable error explaining that systemd journal tools or a systemd/journald host are required.

If `journalctl` exits non-zero, the command returns a runtime error with redacted journalctl stderr. A permission failure should be handled by running as root or granting the user distribution-specific system journal access.

## Deferred behavior

The following are not implemented in v0.1:

- `tunwarden logs --json`
- file-based log fallback
- log rotation management
- metrics or tracing
