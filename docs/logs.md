# Logs command

`tunwarden logs` is the implemented v0.1 read-only command for inspecting TunWarden daemon logs from journald.

Canonical CLI shape is owned by [CLI contract](./cli.md). This document describes the implemented behavior.

## Command shape

```bash
tunwarden logs
tunwarden logs --follow
tunwarden logs -f
tunwarden logs --daemon
tunwarden logs --since "1 hour ago"
tunwarden logs --since -1h
tunwarden logs --since=-30min
```

## Behavior

The command prints a stable header and then streams redacted `journalctl` output from the system journal for the `tunwardend.service` systemd unit:

```text
TunWarden daemon logs
Jun 03 12:00:00 host tunwardend[1234]: tunwardend: daemon started
Jun 03 12:00:01 host tunwardend[1234]: tunwardend: status request handled
```

Default mode shows recent daemon logs using a bounded `journalctl --system --lines` query. `--follow` and `-f` pass through to `journalctl --follow` for live inspection. `--daemon` is accepted as the explicit daemon-log source and is currently equivalent to the default source.

`--since <duration>` and `--since=<duration>` are passed to `journalctl --since <duration>` and can use journalctl-compatible values such as `1 hour ago`, `-1h`, or `-30min`.

## Access requirements

`tunwardend.service` is a systemd service unit, so `tunwarden logs` reads the system journal explicitly with `journalctl --system`.

Users must run the command as root or have distribution-specific permission to read the system journal, commonly through groups such as `systemd-journal`, `adm`, or `wheel`. Without that permission, `journalctl` may fail or show incomplete system-unit logs.

## Safety boundary

`logs` is read-only. It must not mutate daemon state, host networking, routes, DNS, nftables, firewall state, runtime files, or user configuration.

## Redaction

Every log line printed by `tunwarden logs` goes through the shared TunWarden human-output redaction helper before it reaches stdout. This keeps logs aligned with the documented redaction policy for status, doctor, logs, plan, and recover output.

Daemon lifecycle and local API request logs intentionally avoid request/response payloads and generated runtime configuration content.

## Failure behavior

If `journalctl` is missing, the command fails with an actionable error explaining that systemd journal tools or a systemd/journald host are required.

If `journalctl` exits non-zero, the command returns a runtime error with redacted journalctl stderr. A permission failure should be handled by running as root or granting the user distribution-specific system journal access.

## Deferred behavior

The following are not implemented in v0.1:

- `tunwarden logs --json`
- `tunwarden logs --core`
- Xray/core logs
- file-based log fallback
- log rotation management
- metrics or tracing
