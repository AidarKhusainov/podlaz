# Daemon local API

This document defines the implemented local daemon API transport, access model, lifecycle endpoints, and daemon-side authorization boundary.

The command names, user-visible output, and exit codes are owned by [CLI contract](./cli.md). Runtime paths and daemon state ownership are owned by [State and security requirements](./state-and-security.md). Proxy-only process lifecycle behavior is owned by [Proxy-only lifecycle](./proxy-only-lifecycle.md). Optional polkit behavior is owned by [Polkit authorization](./polkit-authorization.md).

## Transport

The daemon API uses HTTP/JSON over a Unix domain socket:

```text
/run/tunwarden/tunwardend.sock
```

The runtime directory can be overridden for tests and local development with:

```bash
TUNWARDEN_RUNTIME_DIR=/tmp/tunwarden-dev
```

The API remains local-only and intentionally small.

## Access model

The daemon socket is created with mode `0660`. Packaged deployments expose only `/run/tunwarden/tunwardend.sock` through the dedicated `tunwarden` group. Generated configs, transaction files, locks, and daemon persistent state remain daemon-private.

Packaged systemd deployments create `/run/tunwarden` with `RuntimeDirectory=tunwarden` and `RuntimeDirectoryMode=0710`, reserve `/var/lib/tunwarden` with `StateDirectory=tunwarden` and `StateDirectoryMode=0700`, and run the default service as `tunwarden:tunwarden`.

This keeps normal CLI commands non-root while avoiding a world-writable daemon socket. If the user does not have socket access, daemon-backed `status` and `doctor` may be unavailable and the CLI keeps the documented conservative local fallback behavior. Daemon-required lifecycle commands such as `connect`, `disconnect`, and recovery execution fail clearly when the daemon is unavailable or inaccessible.

## Optional polkit authorization

Polkit support is optional and daemon-side. Socket access remains the non-polkit fallback unless `TUNWARDEN_POLKIT_AUTHORIZATION` explicitly enables checks.

When enabled, `tunwardend` authorizes the local Unix peer process before executing operation-specific privileged operations:

| Operation | Polkit action |
| --- | --- |
| `connect --mode proxy-only` | `io.github.aidarkhusainov.tunwarden.connect-proxy-only` |
| `connect --mode tun` | `io.github.aidarkhusainov.tunwarden.connect-tun` |
| `disconnect` | `io.github.aidarkhusainov.tunwarden.disconnect` |
| `recover --execute --yes` | `io.github.aidarkhusainov.tunwarden.recover-execute` |

Read-only daemon-backed status and doctor requests are not polkit-gated after socket access is already available.

Authorization decisions use fixed operation identifiers and local peer credentials. The daemon must not pass user profile payloads or generated runtime configs through polkit.

Denied or unavailable authorization fails before lifecycle or recovery execution starts. It must not start Xray, stop Xray, apply TUN/networking state, roll back transactions, delete generated configs, or mutate recovery state.

## Implemented endpoints

### `GET /v1/status`

Returns the current daemon-backed status snapshot. This endpoint is read-only and is not polkit-gated.

### `GET /v1/doctor`

Returns the current daemon-backed diagnostics report. This endpoint is read-only and is not polkit-gated.

### `POST /v1/connect`

Starts a stored profile through daemon-managed lifecycle. Request body:

```json
{
  "mode": "proxy-only|tun",
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

The profile payload is a normalized snapshot supplied by the CLI from user-owned profile state. The daemon validates the snapshot before planning, writing runtime config, or starting Xray. When polkit is enabled, authorization happens before runtime mutation.

### `POST /v1/disconnect`

Stops daemon-managed runtime if it is running. The operation is idempotent. When polkit is enabled, authorization happens before stopping Xray, rolling back TUN transactions, removing generated config, or changing active state.

### `POST /v1/recover`

Executes explicit daemon-owned recovery cleanup for `tunwarden recover --execute --yes`. Recovery dry-run inspection is a CLI/local read-only flow and is not this endpoint. When polkit is enabled, authorization happens before scanning and executing cleanup.

## Runtime lifecycle

On startup, `tunwardend` creates or uses the runtime directory, creates a lock file, removes only a stale Unix socket at the socket path, fails when the socket path exists but is not a Unix socket, listens on the Unix socket, applies socket mode, records local Unix peer credentials where supported, and serves the local endpoints.

On graceful shutdown, `tunwardend` stops any active Xray child process, shuts down the HTTP server, closes the Unix socket listener, removes the socket path, and removes the lock file.
