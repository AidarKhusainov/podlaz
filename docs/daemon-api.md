# Daemon local API

This document defines the implemented v0.1 local daemon API transport and its safety boundary.

The command names, user-visible output, and exit codes are owned by [CLI contract](./cli.md). Runtime paths and daemon state ownership are owned by [State and security requirements](./state-and-security.md).

## MVP transport decision

The v0.1 daemon API uses HTTP/JSON over a Unix domain socket:

```text
/run/tunwarden/tunwardend.sock
```

The runtime directory can be overridden for tests and local development with:

```bash
TUNWARDEN_RUNTIME_DIR=/tmp/tunwarden-dev
```

This keeps the first daemon API small, local-only, and testable with Go's standard library.

## Access model

The v0.1 server sets the daemon socket mode to `0660` and fails startup if permissions cannot be applied.

For local development and manual testing, run both `tunwardend` and `tunwarden status` as the same user and set `TUNWARDEN_RUNTIME_DIR` to a user-owned directory, for example:

```bash
TUNWARDEN_RUNTIME_DIR=/tmp/tunwarden-dev go run ./cmd/tunwardend
TUNWARDEN_RUNTIME_DIR=/tmp/tunwarden-dev go run ./cmd/tunwarden status
```

The same local development model applies to daemon-backed diagnostics:

```bash
TUNWARDEN_RUNTIME_DIR=/tmp/tunwarden-dev go run ./cmd/tunwardend
TUNWARDEN_RUNTIME_DIR=/tmp/tunwarden-dev go run ./cmd/tunwarden doctor
```

For the manual systemd service in `packaging/systemd/tunwardend.service`, the packaged access model is:

- systemd creates `/run/tunwarden` with `RuntimeDirectory=tunwarden` and `RuntimeDirectoryMode=0750`;
- `tunwardend` runs as `root:tunwarden`;
- the daemon creates `/run/tunwarden/tunwardend.sock` and applies socket mode `0660`;
- users that should run read-only CLI commands against the daemon need access through the `tunwarden` group.

This keeps read-only CLI commands non-root while avoiding a world-writable daemon socket. If the user does not have socket access, daemon-backed `status` and `doctor` may be unavailable and the CLI keeps the documented conservative local fallback behavior.

Read-only CLI commands must not require root. The packaged daemon access model preserves that rule by granting socket access through the dedicated `tunwarden` group instead of requiring elevated CLI execution.

## Why D-Bus and polkit are deferred

D-Bus and polkit are intentionally not implemented in this issue.

Reasons:

- v0.1 status and diagnostics are read-only and do not require an authorization policy engine;
- there are no privileged route, DNS, nftables, firewall, TUN, or core process mutations in this issue;
- Unix sockets are enough for a local daemon health/status/diagnostics API;
- HTTP/JSON over Unix sockets is simple to unit test without a system bus;
- polkit decisions should be introduced together with real privileged operations and documented authorization rules.

D-Bus and polkit remain valid future options for packaged desktop integration and authorization, but adding them before daemon-owned mutations would increase complexity without improving the current user-visible behavior.

## Implemented endpoints

### `GET /v1/status`

Returns the current daemon-backed status snapshot.

Current v0.1 response shape:

```json
{
  "daemon": "running",
  "service": "manual|systemd",
  "connection": "inactive",
  "runtime_directory": "present",
  "proxy": "inactive",
  "tun": "disabled"
}
```

Fields:

| Field | Meaning |
| --- | --- |
| `daemon` | Daemon availability from the daemon's own process. |
| `service` | Daemon supervisor model. `manual` is used for direct execution. `systemd` is used by the repository systemd unit. |
| `connection` | Current connection state. v0.1 reports `inactive`. |
| `runtime_directory` | Daemon runtime directory visibility. |
| `proxy` | Proxy lifecycle state. v0.1 reports `inactive`. |
| `tun` | TUN mode state. v0.1 reports `disabled`. |
| `warnings` | Optional daemon-side visibility warnings. |

All listed fields except `warnings` are required in daemon responses. The CLI treats missing required fields, invalid `service` values, or otherwise invalid responses as daemon protocol errors and uses the existing warning/fallback path instead of rendering a healthy daemon-backed status.

This is an internal local API contract, not the public `status --json` CLI contract. `tunwarden status --json` remains deferred until the CLI JSON schema is implemented.

### `GET /v1/doctor`

Returns the current daemon-backed read-only diagnostics report.

Current v0.1 response shape:

```json
{
  "source": "daemon",
  "checks": [
    {
      "name": "daemon",
      "severity": "OK",
      "message": "running"
    }
  ]
}
```

Fields:

| Field | Meaning |
| --- | --- |
| `source` | Report source. Daemon responses use `daemon`. |
| `checks` | Ordered diagnostic checks rendered by `tunwarden doctor`. |
| `checks[].name` | Stable check name. |
| `checks[].severity` | One of `OK`, `WARN`, or `FAIL`. |
| `checks[].message` | Human-readable diagnostic detail. |

All listed fields are required. The CLI treats missing fields, invalid severities, invalid JSON, or unexpected HTTP status as daemon protocol errors and falls back to local read-only diagnostics with a daemon warning.

This is an internal local API contract, not the public `doctor --json` CLI contract. `tunwarden doctor --json` remains deferred until the CLI JSON schema is implemented.

## Runtime lifecycle

On startup, `tunwardend`:

1. creates the runtime directory if needed;
2. creates a daemon lock file;
3. removes only a stale Unix socket at the socket path if present;
4. fails explicitly when the socket path exists but is not a Unix socket;
5. listens on the Unix socket;
6. applies the socket mode;
7. serves the read-only status and doctor endpoints.

When started by the repository systemd unit, systemd creates the runtime directory before daemon startup and captures daemon stdout/stderr in journald.

On graceful shutdown, `tunwardend`:

1. shuts down the HTTP server;
2. closes the Unix socket listener;
3. removes the socket path;
4. removes the lock file.

If another daemon appears to be running or a previous shutdown left an unclean lock file, startup fails explicitly instead of silently taking over daemon-owned state.

## Manual systemd verification

Manual service verification on a supported systemd Linux host:

```bash
go build -o ./bin/tunwarden ./cmd/tunwarden
go build -o ./bin/tunwardend ./cmd/tunwardend
sudo install -m 0755 ./bin/tunwarden /usr/local/bin/tunwarden
sudo install -m 0755 ./bin/tunwardend /usr/local/bin/tunwardend
sudo install -m 0644 packaging/systemd/tunwardend.service /etc/systemd/system/tunwardend.service
sudo systemd-analyze verify /etc/systemd/system/tunwardend.service
sudo systemctl daemon-reload
sudo systemctl start tunwardend
systemctl status tunwardend --no-pager
tunwarden status
journalctl -u tunwardend -n 50 --no-pager
sudo systemctl stop tunwardend
```

Expected daemon-backed status includes:

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

## Safety boundary

The v0.1 daemon API is local-only and read-only.

It must not:

- create or delete TUN interfaces;
- add, remove, or replace routes;
- change DNS configuration;
- create, modify, flush, or delete nftables or firewall state;
- start, stop, or supervise Xray;
- mutate user profiles or subscriptions.

The current endpoints only report daemon availability, conservative inactive runtime state, and read-only host diagnostics.
