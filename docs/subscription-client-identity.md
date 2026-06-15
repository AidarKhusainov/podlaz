# Subscription client identity

This document owns the privacy and evidence contract for subscription request identity/HWID behavior.

## Current request behavior

HTTP(S) subscription fetches send the explicit TunWarden product `User-Agent` only.

TunWarden must not add provider-specific HWID, device-id, or client-identity query parameters or headers until the exact wire contract is confirmed from sanitized, reproducible evidence.

The current subscription fetch path must not generate, persist, read, reset, or send a client identity. Existing Base64 URI-list subscription import and update behavior remains unchanged.

## Evidence gate

Provider-specific subscription identity support requires confirmed evidence for all of these details:

- whether the identity is sent as a query parameter, HTTP header, or another request field;
- exact field name and casing expected by compatible servers;
- exact value format and length constraints;
- whether the value must be stable per installation, OS user, machine, or provider account;
- whether the same request behavior applies to Base64 URI-list, Xray JSON, and other subscription response types;
- server behavior when the value is missing, malformed, changed, or reset.

Acceptable evidence includes sanitized Happ request captures, controlled Remnawave server logs, upstream source or documentation, or reproducible sanitized curl transcripts. Real subscription URLs, provider tokens, HWIDs, user identities, generated core configs, and raw logs containing secrets must not be committed.

## Privacy contract for future identity support

When identity support is implemented, the default TunWarden identity must be privacy-safe.

Allowed default:

```text
$XDG_STATE_HOME/tunwarden/client-id
fallback: ~/.local/state/tunwarden/client-id
```

The preferred value is a random TunWarden client ID generated on first use and persisted in user-owned state. The state directory should be created with private permissions where practical, for example `0700`, and the identity file should be private to the user, for example `0600`.

The implementation must not send raw host identifiers, including:

- `/etc/machine-id`;
- MAC addresses;
- hostname;
- DMI serials;
- disk serials;
- CPU identifiers;
- other raw hardware or installation identifiers.

If a machine-derived identity is chosen instead of a random per-user TunWarden identity, it must use an application-specific irreversible derivation from the machine identity and the pull request must document why that is better for users than a random persisted identity.

The identity must be redacted from human output, JSON output, logs, errors, tests, fixtures, issue comments, and pull request examples. Redaction must cover UUID-shaped, hex, and URL-safe text formats because the confirmed provider contract may use any of those shapes.

## Reset behavior

Until identity support exists, there is no generated client identity to reset.

A future reset path must be explicit and must warn that resetting the identity can consume a new provider device slot or break provider-side device binding. If the reset path is file removal, the documented command must remove only the TunWarden client identity file and must not remove profile, subscription, daemon, runtime, or networking state.

## Safety boundary

Subscription client identity handling is user-owned state only.

It must not:

- require root;
- start `tunwardend`;
- start Xray;
- create TUN devices;
- mutate routes;
- mutate DNS;
- mutate nftables or firewall state;
- write generated runtime core configuration;
- store the identity in `/etc`, the repository, daemon-private state, or generated runtime directories.

When provider-specific identity injection is implemented, it must live in the shared HTTP(S) subscription request-construction path used by both `tunwarden import <http-url>` and `tunwarden subscription update`. Command handlers must not duplicate request identity logic.
