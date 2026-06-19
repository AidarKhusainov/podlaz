# Local import formats

This document describes the file formats accepted by the first-run convenience command:

```bash
podlaz import <local-path>
```

The canonical command shape, exit codes, and CLI safety semantics remain owned by [CLI contract](./cli.md). Profile validation, redaction, and state layout remain owned by [State and security requirements](./state-and-security.md) and [Subscriptions and profiles](./subscriptions-and-profiles.md).

## Safety boundary

Local import is user-owned profile state mutation only.

The command must not:

- start `podlazd`;
- start Xray;
- require root;
- create TUN devices;
- mutate routes, DNS, nftables, or firewall state;
- store raw Xray JSON as runtime configuration or persistent source of truth;
- print full share URIs, identities, passwords, private keys, or generated core configuration.

Each supported entry is normalized into `profile.Profile`, validated, and written to the profile store. Generated Xray configs remain runtime output created later by planning or connection code.

## Detection order

For ordinary local paths without a URI scheme, `podlaz import` reads the file once with a bounded size limit and applies this detection order:

1. Xray JSON object when the first non-space byte is `{`.
2. Plain URI-list when at least one line is a supported share URI.
3. Base64 URI-list when the file decodes as Base64 and the decoded content contains at least one supported share URI.

Malformed JSON objects fail as Xray JSON errors and do not fall back to URI-list parsing. Valid JSON values whose top-level type is not an object fail clearly as unsupported local import JSON.

`file://`, `http://`, and `https://` inputs are subscription sources, not local one-shot file imports.

## Xray JSON

Supported initial scope:

- top-level Xray JSON object with an `outbounds` array;
- VLESS outbounds using `settings.vnext[].address`, `settings.vnext[].port`, and `settings.vnext[].users[]`;
- VLESS `id`, `encryption`, and `flow` user fields;
- stream settings for `tcp`/`raw`, `ws`, `grpc`, `httpupgrade`, and `xhttp` where they map to the existing normalized profile model;
- TLS and Reality metadata that already exist in the profile model.

Unsupported JSON outbounds are skipped when at least one supported profile is imported. If no supported profile can be imported, the command fails and leaves the profile store unchanged.

JSON outbound import for VMess, Trojan, and Shadowsocks is intentionally deferred until it can be mapped and tested without weakening validation or runtime-generation safety.

## URI-list files

Plain URI-list files contain one entry per non-empty line. Supported share URI schemes are the same schemes accepted by `profile import`:

```text
vless://
vmess://
trojan://
ss://
```

Unsupported lines are reported as skipped entries when at least one supported profile is imported. Duplicate profile IDs inside one local import batch are fatal, and the profile store is not modified.

## Base64 URI-list files

Base64 URI-list files decode to the same plain URI-list format described above. Standard, raw standard, URL-safe, and raw URL-safe Base64 encodings are accepted.

## Persistence

Imported local profiles use:

```text
source: imported_file
engine: xray
```

Profile store writes are atomic. Validation, duplicate detection, parse failures, or existing-profile ID collisions happen before the atomic replacement, so failed local imports must not leave partial profile state behind.
