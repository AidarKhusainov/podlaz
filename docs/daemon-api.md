# Daemon local API

This document defines the implemented v0.1 local daemon API transport and its safety boundary.

The command names, user-visible output, and exit codes are owned by [CLI contract](./cli.md). Runtime paths and daemon state ownership are owned by [State and security requirements](./state-and-security.md). Proxy-only process lifecycle behavior is owned by [Proxy-only lifecycle](./proxy-only-lifecycle.md).

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

For local development and manual testing, run both `tunwardend` and `tunwarden` as the same non-root user and set `TUNWARDEN_RUNTIME_DIR` to a user-owned directory, for example:

```bash
TUNWARDEN_RUNTIME_DIR=/tmp/tunwarden-dev go run ./cmd/tunwardend
TUNWARDEN_RUNTIME_DIR=/tmp/tunwarden-dev go run ./cmd/tunwarden status
TUNWARDEN_RUNTIME_DIR=/tmp/tunwarden-dev go run ./cmd/tunwarden doctor
```

For proxy-only lifecycle testing, the daemon must also be able to resolve an Xray executable through `TUNWARDEN_XRAY_PATH` or `PATH`:

```bash
TUNWARDEN_RUNTIME_DIR=/tmp/tunwarden-dev TUNWARDEN_XRAY_PATH=/usr/local/bin/xray go run ./cmd/tunwardend
TUNWARDEN_RUNTIME_DIR=/tmp/tunwarden-dev go run ./cmd/tunwarden connect --mode proxy-only <profile-id>
TUNWARDEN_RUNTIME_DIR=/tmp/tunwarden-dev go run ./cmd/tunwarden disconnect
```

For the manual systemd service in `packaging/systemd/tunwardend.service`, the packaged access model is:

- systemd creates `/run/tunwarden` with `RuntimeDirectory=tunwarden` and `RuntimeDirectoryMode=0750`;
- `packaging/sysusers.d/tunwarden.conf` declares the unprivileged `tunwarden` service identity for packaged installs;
- `tunwardend` runs as `tunwarden:tunwarden` because v0.1 proxy-only lifecycle does not require root;
- Xray child processes inherit the same unprivileged service identity;
- the current unit grants no ambient or bounding capabilities;
- the daemon creates `/run/tunwarden/tunwardend.sock` and applies socket mode `0660`;
- users that should run CLI commands against the daemon need access through the `tunwarden` group.

This keeps CLI commands non-root while avoiding a world-writable daemon socket. If the user does not have socket access, daemon-backed `status` and `doctor` may be unavailable and the CLI keeps the documented conservative local fallback behavior. Daemon-required lifecycle commands such as `connect` and `disconnect` fail clearly when the daemon is unavailable or inaccessible.

Read-only CLI commands must not require root. The packaged daemon access model preserves that rule by granting socket access through the dedicated `tunwarden` group instead of requiring elevated CLI execution.

## Why D-Bus and polkit are deferred

D-Bus and polkit are intentionally not implemented in this issue.

Reasons:

- v0.1 daemon API is local-only and intentionally narrow;
- proxy-only lifecycle starts and stops only a daemon-owned Xray child process and generated runtime config;
- there are still no privileged route, DNS, nftables, firewall, or TUN mutations;
- Unix sockets are enough for a local daemon health/status/diagnostics/lifecycle API;
- HTTP/JSON over Unix sockets is simple to unit test without a system bus;
- polkit decisions should be introduced together with real privileged networking operations and documented authorization rules.

D-Bus and polkit remain valid future options for packaged desktop integration and authorization, but adding them before daemon-owned network mutations would increase complexity without improving the current user-visible behavior.

## Implemented endpoints

### `GET /v1/status`

Returns the current daemon-backed status snapshot.

Current v0.1 response shape when inactive:

```json
{
  "daemon": "running",
  "service": "manual|systemd",
  "connection": "inactive",
  "runtime_directory": "present",
  "proxy": "inactive",
  "tun": "disabled",
  "routes": "not modified",
  "dns": "not modified",
  "firewall": "not modified"
}
```

Current v0.1 response shape when proxy-only Xray is active:

```json
{
  "daemon": "running",
  "service": "manual|systemd",
  "connection": "active",
  "mode": "proxy-only",
  "runtime_directory": "present",
  "runtime_config_path": "/run/tunwarden/generated/xray.json",
  "proxy": "listening on 127.0.0.1:1080 (SOCKS), 127.0.0.1:8080 (HTTP)",
  "tun": "disabled",
  "routes": "not modified",
  "dns": "not modified",
  "firewall": "not modified"
}
```

Fields:

| Field | Meaning |
| --- | --- |
| `daemon` | Daemon availability from the daemon's own process. |
| `service` | Daemon supervisor model. `manual` is used for direct execution. `systemd` is used by the repository systemd unit. |
| `connection` | Current connection state: `inactive`, `active`, or `error (core exited)` for an unexpected Xray exit. |
| `mode` | Active connection mode. Present for active proxy-only lifecycle state. |
| `runtime_directory` | Daemon runtime directory visibility. |
| `runtime_config_path` | Generated runtime Xray config path when an active or failed proxy-only lifecycle has one. |
| `proxy` | Proxy lifecycle state and local listeners. |
| `tun` | TUN mode state. v0.1 reports `disabled`. |
| `routes` | Route mutation state. v0.1 reports `not modified`. |
| `dns` | DNS mutation state. v0.1 reports `not modified`. |
| `firewall` | nftables/firewall mutation state. v0.1 reports `not modified`. |
| `warnings` | Optional daemon-side warnings such as unexpected Xray process exit. |

All listed fields except `mode`, `runtime_config_path`, `routes`, `dns`, `firewall`, and `warnings` are required in daemon responses. The CLI treats missing required fields, invalid `service` values, or otherwise invalid responses as daemon protocol errors and uses the existing warning/fallback path instead of rendering a healthy daemon-backed status.

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

### `POST /v1/connect`

Starts a stored profile in proxy-only mode through daemon-managed Xray lifecycle.

Request shape:

```json
{
  "mode": "proxy-only",
  "profile": {
    "id": "profile-id",
    "name": "profile name",
    "source": "manual|subscription|imported_file|imported_uri",
    "engine": "xray",
    "server": "example.com",
    "port": 443,
    "protocol": "vless"
  }
}
```

The profile payload is a normalized snapshot supplied by the CLI from user-owned profile state. The daemon validates the snapshot before planning, writing runtime config, or starting Xray.

Successful response shape:

```json
{
  "connection": "active",
  "mode": "proxy-only",
  "proxy": "listening on 127.0.0.1:1080 (SOCKS), 127.0.0.1:8080 (HTTP)",
  "tun": "disabled",
  "routes": "not modified",
  "dns": "not modified",
  "firewall": "not modified",
  "runtime_config_path": "/run/tunwarden/generated/xray.json"
}
```

### `POST /v1/disconnect`

Stops daemon-managed Xray if it is running. The operation is idempotent.

Successful response shape:

```json
{
  "connection": "inactive",
  "proxy": "inactive",
  "tun": "disabled",
  "routes": "not modified",
  "dns": "not modified",
  "firewall": "not modified"
}
```

## Runtime lifecycle

On startup, `tunwardend`:

1. creates the runtime directory if needed;
2. creates a daemon lock file;
3. removes only a stale Unix socket at the socket path if present;
4. fails explicitly when the socket path exists but is not a Unix socket;
5. listens on the Unix socket;
6. applies the socket mode;
7. serves status, doctor, connect, and disconnect endpoints.

When started by the repository systemd unit, systemd creates the runtime directory before daemon startup, owns it for `tunwarden:tunwarden`, and captures daemon stdout/stderr in journald.

During proxy-only connect, `tunwardend`:

1. refuses to start Xray if the daemon process is running as root;
2. validates the requested mode and profile snapshot;
3. builds the proxy-only plan and generated Xray config using the existing planner/engine config generator;
4. writes generated runtime config under `/run/tunwarden/generated/` using restrictive permissions and atomic replacement;
5. starts Xray as a supervised child process under the same unprivileged service identity;
6. records active state for daemon-backed status.

On graceful daemon shutdown, `tunwardend`:

1. stops any active Xray child process;
2. shuts down the HTTP server;
3. closes the Unix socket listener;
4. removes the socket path;
5. removes the lock file.

On disconnect, `tunwardend`:

1. sends a graceful termination signal to active Xray;
2. force-stops Xray if it does not exit before the timeout;
3. removes the generated runtime config path;
4. reports inactive connection and proxy state.

If Xray exits unexpectedly, the daemon keeps the generated config for inspection/recovery and reports `connection: error (core exited)` with a warning in daemon-backed status.

If another daemon appears to be running or a previous shutdown left an unclean lock file, startup fails explicitly instead of silently taking over daemon-owned state.

## Manual systemd verification

Manual service verification on a supported systemd Linux host:

```bash
go build -o ./bin/tunwarden ./cmd/tunwarden
go build -o ./bin/tunwardend ./cmd/tunwardend
sudo install -m 0755 ./bin/tunwarden /usr/local/bin/tunwarden
sudo install -m 0755 ./bin/tunwardend /usr/local/bin/tunwardend

sudo install -m 0644 packaging/sysusers.d/tunwarden.conf /usr/lib/sysusers.d/tunwarden.conf
sudo systemd-sysusers /usr/lib/sysusers.d/tunwarden.conf
sudo usermod -aG tunwarden "$USER"
# Start a new login session before running tunwarden status so group membership is active.

sudo install -m 0644 packaging/systemd/tunwardend.service /etc/systemd/system/tunwardend.service
sudo systemd-analyze verify /etc/systemd/system/tunwardend.service
sudo systemctl daemon-reload
sudo systemctl start tunwardend
systemctl status tunwardend --no-pager
tunwarden status
journalctl -u tunwardend -n 50 --no-pager
sudo systemctl stop tunwardend
```

If `systemd-sysusers` is unavailable during manual testing, create the system user explicitly before starting the service:

```bash
sudo useradd --system --home-dir /var/lib/tunwarden --shell /usr/sbin/nologin --user-group tunwarden
```

Expected daemon-backed inactive status includes:

```text
TunWarden status
Daemon: running
Service: systemd
Connection: inactive
Runtime directory: present
Proxy: inactive
TUN: disabled
Routes: not modified
DNS: not modified
Firewall: not modified
Stale state: none
```

## Safety boundary

The v0.1 daemon API is local-only. It may mutate only daemon-owned proxy-only Xray process state and volatile TunWarden runtime config files for `connect` and `disconnect`.

It must not:

- create or delete TUN interfaces;
- add, remove, or replace routes;
- change DNS configuration;
- create, modify, flush, or delete nftables or firewall state;
- mutate user profiles or subscriptions.

The current lifecycle endpoints only start/stop supervised Xray in proxy-only mode, report daemon status/diagnostics, and keep system networking unchanged.
