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

## Why D-Bus and polkit are deferred

D-Bus and polkit are intentionally not implemented in this issue.

Reasons:

- v0.1 status is read-only and does not require an authorization policy engine;
- there are no privileged route, DNS, nftables, firewall, TUN, or core process mutations in this issue;
- Unix sockets are enough for a local daemon health/status API;
- HTTP/JSON over Unix sockets is simple to unit test without a system bus;
- polkit decisions should be introduced together with real privileged operations and documented authorization rules.

D-Bus and polkit remain valid future options for packaged desktop integration and authorization, but adding them before daemon-owned mutations would increase complexity without improving the current user-visible behavior.

## Implemented endpoint

### `GET /v1/status`

Returns the current daemon-backed status snapshot.

Current v0.1 response shape:

```json
{
  "daemon": "running",
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
| `connection` | Current connection state. v0.1 reports `inactive`. |
| `runtime_directory` | Daemon runtime directory visibility. |
| `proxy` | Proxy lifecycle state. v0.1 reports `inactive`. |
| `tun` | TUN mode state. v0.1 reports `disabled`. |
| `warnings` | Optional daemon-side visibility warnings. |

This is an internal local API contract, not the public `status --json` CLI contract. `tunwarden status --json` remains deferred until the CLI JSON schema is implemented.

## Runtime lifecycle

On startup, `tunwardend`:

1. creates the runtime directory if needed;
2. creates a daemon lock file;
3. removes a stale socket path if present;
4. listens on the Unix socket;
5. serves the read-only status endpoint.

On graceful shutdown, `tunwardend`:

1. shuts down the HTTP server;
2. closes the Unix socket listener;
3. removes the socket path;
4. removes the lock file.

If another daemon appears to be running or a previous shutdown left an unclean lock file, startup fails explicitly instead of silently taking over daemon-owned state.

## Safety boundary

The v0.1 daemon API is local-only and read-only.

It must not:

- create or delete TUN interfaces;
- add, remove, or replace routes;
- change DNS configuration;
- create, modify, flush, or delete nftables or firewall state;
- start, stop, or supervise Xray;
- mutate user profiles or subscriptions.

The current endpoint only reports daemon availability and conservative inactive runtime state.
