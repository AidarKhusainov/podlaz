# Daemon API

This document describes the local daemon API boundary used by the CLI and packaged daemon. It is not a remote API contract.

## Packaged daemon access and fallback sockets

Packaged clients first try the filesystem daemon socket. When that socket fails with a transport-level permission error, lifecycle/status clients may retry through the abstract socket used by the packaged daemon boundary. The retry is intentionally limited to transport errors: daemon HTTP responses, JSON decoding failures, schema validation failures, and authorization denials are returned as daemon/protocol errors rather than being reclassified as daemon unavailability.

If the abstract socket is attempted and reaches the daemon, the abstract socket result is surfaced to the user. For example, an authorization denial remains an authorization denial, and a headless authorization-unavailable response remains actionable guidance for server installations instead of being hidden behind the original filesystem socket permission error.

## Read-only daemon logs

`GET /v1/logs` streams the same redacted `podlazd.service` journal view exposed by `podlaz logs`. The daemon, not the login process, owns the journald read. This keeps ordinary users out of broad `systemd-journal` membership while preserving the existing product-owned log filters and redaction boundary.

The endpoint accepts only these query parameters:

- `since=<positive integer><s|m|h>` uses the same canonical grammar and 720-hour maximum as the CLI.
- `follow=1` keeps the response open and flushes new redacted log lines as they arrive.
- `core=1` applies the existing Xray/core log filter.

Without `since`, the daemon requests the existing bounded 200-line tail. Unknown parameters, duplicate values, invalid booleans, and invalid lookback windows are rejected before `journalctl` is started.

Log access is authorized independently from lifecycle mutation authorization. The daemon requires local peer credentials and permits only root or a peer whose effective or supplementary groups include the daemon access group (`podlaz` in the packaged service). Supplementary membership is validated against the peer process identity and start time; missing or inconsistent process evidence fails closed. The filesystem socket remains the normal CLI transport, and the handler repeats the group check so another local listener cannot bypass the log-specific authorization boundary.

`GET /v1/logs` is read-only. It does not grant connect, disconnect, recovery, journal-wide access, or arbitrary `journalctl` arguments/units.

## Error classification

Transport-level connection failures are daemon availability failures. HTTP status responses, daemon authorization responses, JSON decoding errors, and response schema validation errors are daemon response/protocol failures. The CLI must preserve this classification so automation can distinguish a stopped daemon from a reachable daemon that rejected or could not authorize the requested operation.
