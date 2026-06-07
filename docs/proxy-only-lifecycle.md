# Proxy-only lifecycle

This document defines the implemented v0.1 proxy-only `connect`, `disconnect`, and daemon-managed Xray process lifecycle behavior.

## Scope

The v0.1 lifecycle implementation is intentionally limited to daemon-owned Xray process state and volatile runtime files.

Implemented behavior:

- `tunwarden connect [--mode proxy-only] <profile-id>` starts a stored profile through `tunwardend`.
- `tunwarden disconnect` stops the daemon-managed Xray process.
- The daemon writes generated Xray config under `/run/tunwarden/generated/xray.json` or the configured runtime directory equivalent.
- Generated config is runtime output, not persistent source of truth.
- Xray is started with local SOCKS and HTTP listeners from the proxy-only plan defaults.
- Disconnect first asks Xray to stop gracefully and then uses a forced-stop fallback.
- Repeated disconnects are safe and leave the connection inactive.
- Unexpected Xray exit is reflected in daemon-backed status warnings.

## Safety boundary

Proxy-only lifecycle must not mutate host networking.

The implementation must leave all of the following unchanged:

- TUN devices;
- routes and policy rules;
- DNS configuration;
- nftables/firewall state.

Status output must make that explicit by reporting:

```text
TUN: disabled
Routes: not modified
DNS: not modified
Firewall: not modified
```

## Daemon requirement

`connect` and `disconnect` require the local daemon API. If `tunwardend` is unavailable, the CLI must fail clearly with exit code `5`.

## Xray binary resolution

The daemon resolves Xray in this order:

1. explicit `TUNWARDEN_XRAY_PATH` environment variable;
2. `xray` from `PATH`.

TunWarden does not download or update Xray automatically in v0.1.

## Runtime config handling

The daemon writes generated Xray config with restrictive permissions and an atomic replacement pattern before starting Xray.

The generated config must not be logged in full by default because it can contain credentials or connection metadata.

On normal disconnect, the generated config is removed. If the daemon or host crashes, stale generated runtime files are recovery candidates for `tunwarden recover`.

## Status behavior

During an active proxy-only connection, daemon-backed `tunwarden status` reports active process state and local proxy listeners, for example:

```text
TunWarden status
Daemon: running
Connection: active
Mode: proxy-only
Proxy: listening on 127.0.0.1:1080 (SOCKS), 127.0.0.1:8080 (HTTP)
TUN: disabled
Routes: not modified
DNS: not modified
Firewall: not modified
```

After disconnect:

```text
Connection: inactive
Proxy: inactive
```

If Xray exits unexpectedly, status must surface the crash through connection state and warnings instead of silently reporting a healthy active connection.

## Deferred behavior

The following remain out of scope for v0.1 proxy-only lifecycle:

- TUN mode;
- route, DNS, nftables, or firewall mutation;
- reconnect state machine;
- automatic Xray download or update;
- lifecycle JSON output;
- full active profile metadata in public status output.
