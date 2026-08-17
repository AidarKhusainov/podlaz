# Daemon API

This document describes the local daemon API boundary used by the CLI and packaged daemon. It is not a remote API contract.

## Packaged daemon access and fallback sockets

Packaged clients first try the filesystem daemon socket. When that socket fails with a transport-level permission error, ordinary-user clients may retry through the abstract socket used by the packaged daemon boundary. The retry is intentionally limited to transport errors: daemon HTTP responses, JSON decoding failures, schema validation failures, and authorization failures are returned as daemon/protocol errors rather than being reclassified as daemon unavailability.

If the abstract socket is attempted and reaches the daemon, the abstract socket result is surfaced to the user. For example, an authorization denial remains an authorization denial, and a headless authorization-unavailable response remains actionable guidance for server installations instead of being hidden behind the original filesystem socket permission error.

## Read-only daemon logs

`GET /v1/logs` streams the same redacted `podlazd.service` journal view exposed by `podlaz logs`. The daemon, not the login process, owns the journald read. This preserves the documented ordinary-user package path without requiring membership in `podlaz`, `systemd-journal`, `adm`, or another service/journal group.

The endpoint accepts only these query parameters:

- `since=<positive integer><s|m|h>` uses the same canonical grammar and 720-hour maximum as the CLI.
- `follow=1` keeps the response open and flushes new redacted log lines as they arrive.
- `core=1` applies the existing Xray/core log filter.

Without `since`, the daemon requests the existing bounded 200-line tail. Unknown parameters, duplicate values, invalid booleans, and invalid lookback windows are rejected before `journalctl` is started.

The log endpoint is local-only and requires kernel-provided Unix peer credentials. It deliberately follows the existing read-only status/doctor diagnostics boundary rather than lifecycle mutation authorization: any authenticated local peer may request the centrally redacted product log view, while connect, disconnect and recovery execution remain separately polkit-gated. The filesystem socket remains the internal/admin transport; when an ordinary packaged user cannot open it, the CLI retries the packaged abstract Unix socket exactly for that transport permission failure.

`GET /v1/logs` is read-only. It does not grant connect, disconnect, recovery execution, journal-wide access, arbitrary units, or arbitrary `journalctl` arguments. Detailed `journalctl` stderr stays inside the daemon. If the backend fails after a streaming response has already started, the daemon reports only a generic HTTP trailer so the CLI still exits with a runtime failure instead of treating a truncated stream as success.

## Error classification

Transport-level connection failures are daemon availability failures. HTTP status responses, daemon authorization responses, JSON decoding errors, and response schema validation errors are daemon response/protocol failures. The CLI must preserve this classification so automation can distinguish a stopped daemon from a reachable daemon that rejected or could not authorize the requested operation.
