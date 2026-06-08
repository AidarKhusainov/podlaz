# Profile management

`tunwarden profile` is the implemented v0.1 command group for managing profiles before any connection is attempted.

Canonical CLI shape is owned by [CLI contract](./cli.md). Broader profile and subscription normalization requirements are owned by [Subscriptions and profiles](./subscriptions-and-profiles.md). This document describes the implemented profile behavior.

## Command shape

```bash
tunwarden profile add --name test --server example.com --port 443 --protocol vless
tunwarden profile import '<share-uri>'
tunwarden profile list
tunwarden profile list --json
tunwarden profile show test
tunwarden profile show test --json
tunwarden profile delete test --yes
```

Supported share URI schemes for `profile import` are:

- `vless://`
- `vmess://`
- `trojan://`
- `ss://`

## Behavior

`profile add` creates a normalized manual profile in user-owned local state. The current v0.1 manual fields are:

- `name`
- deterministic local `id` derived from the name
- `source: manual`
- `engine: xray`
- `protocol`
- `server`
- `port`

The supported manual protocols in this foundation implementation are `vless`, `vmess`, `trojan`, and `shadowsocks`.

A successful add prints:

```text
Profile added: test
```

`profile import <share-uri>` imports one supported share URI into the same local profile store. It normalizes common endpoint metadata for VLESS, VMess, Trojan, and Shadowsocks share URIs:

- server host and port
- display name from the URI fragment or provider name field, or a deterministic fallback name
- deterministic local `id` from the display name plus a hash of stable connection fields
- `source: imported_uri`
- `engine: xray`
- protocol-specific identity or authentication material in `user_identity`
- transport, security, encryption, TLS/SNI/ALPN/fingerprint, path, host header, service name, and Reality metadata where the source format provides them

A successful import prints the deterministic profile ID and any parser warnings for unsupported or risky options:

```text
Imported profile: test-a1b2c3d4e5
Warnings: 1
- unsupported Trojan option "foo" ignored
```

Supported VLESS options such as `flow` are preserved for proxy-only Xray config planning and must not produce warnings. Unsupported query options are reported as warnings and ignored when safe. Unsupported URI schemes, malformed percent-encoding, unsupported transport/security values, incompatible VLESS transport/security combinations, missing required identity/authentication fields, unsupported Shadowsocks plugins, and malformed VMess Base64 JSON fail clearly with exit code `2`.

`profile list` prints a stable table:

```text
ID               NAME   PROTOCOL  SERVER       PORT
test-a1b2c3d4e5  test   vless     example.com  443
```

`profile show <profile-id>` prints one normalized profile in human-readable form. Imported sensitive identity fields are redacted in human and JSON output according to the shared output redaction policy, while the local profile store keeps the complete normalized profile needed for future Xray config generation.

`profile delete <profile-id> --yes` removes the profile from local user state. The explicit `--yes` is required in the current v0.1 non-interactive CLI path because profile deletion removes persistent user state.

## JSON output

`profile list --json` and `profile show --json` are implemented with the common top-level JSON contract:

```json
{
  "schema_version": "v1",
  "status": "ok",
  "warnings": [],
  "errors": []
}
```

`profile list --json` also includes `profiles`.

`profile show --json` also includes `profile`.

`profile add --json`, `profile import --json`, and `profile delete --json` are not implemented in v0.1.

## Storage

Profiles are stored in the documented user state location:

```text
$XDG_STATE_HOME/tunwarden/profiles.json
```

When `XDG_STATE_HOME` is unset or relative, the fallback is:

```text
~/.local/state/tunwarden/profiles.json
```

The profile store is user-owned state and must not require root. Writes use an atomic temporary-file-and-rename flow and store files with restrictive permissions.

## Validation and failure behavior

Manual profile input must include a valid name, protocol, server, and port. Invalid input fails clearly with exit code `2`.

Imported VLESS and VMess profiles must include a UUID user identity, a valid server host, and a valid port. Imported Trojan profiles must include a password in the URI user component. Imported Shadowsocks profiles must include method/password credentials and a valid server endpoint. Unsupported transport or security values fail clearly instead of being silently normalized. The current v0.1 importer intentionally does not support VLESS custom string IDs; only UUID user identities are accepted.

Duplicate profile IDs fail without overwriting the existing profile. Imported profile IDs include a deterministic hash suffix so distinct imported profiles with the same display name can coexist while re-importing the same URI remains stable.

Corrupt, unreadable, unsupported, or internally invalid profile storage fails safely with a clear error instead of silently discarding or rewriting user state.

## Safety boundary

Profile management mutates persistent local TunWarden user state only. It must not start Xray, contact a server, start network processes, or mutate TUN, routes, DNS, nftables, firewall, daemon runtime state, or system networking state.

## Deferred behavior

The following are not implemented in v0.1:

- VLESS custom string IDs
- `profile import --json`
- Xray config generation for non-VLESS imported profiles
- connect/disconnect behavior
- TUN, route, DNS, nftables, or firewall mutation
