# Daemon API

This document describes the local daemon API boundary used by the CLI and packaged daemon. It is not a remote API contract.

## Packaged daemon access and fallback sockets

Packaged clients first try the filesystem daemon socket. When that socket fails with a transport-level permission error, the client may retry through the abstract socket used by the packaged daemon boundary. The retry is intentionally limited to transport errors: daemon HTTP responses, JSON decoding failures, schema validation failures, and authorization denials are returned as daemon/protocol errors rather than being reclassified as daemon unavailability.

If the abstract socket is attempted and reaches the daemon, the abstract socket result is surfaced to the user. For example, an authorization denial remains an authorization denial, and a headless authorization-unavailable response remains actionable guidance for server installations instead of being hidden behind the original filesystem socket permission error.

## Error classification

Transport-level connection failures are daemon availability failures. HTTP status responses, daemon authorization responses, JSON decoding errors, and response schema validation errors are daemon response/protocol failures. The CLI must preserve this classification so automation can distinguish a stopped daemon from a reachable daemon that rejected or could not authorize the requested operation.
