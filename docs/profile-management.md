# Profile management

`tunwarden profile` is the implemented v0.1 command group for managing manual profiles before any connection is attempted.

Canonical CLI shape is owned by [CLI contract](./cli.md). Broader profile and subscription normalization requirements are owned by [Subscriptions and profiles](./subscriptions-and-profiles.md). This document describes the implemented manual-profile behavior.

## Command shape

```bash
tunwarden profile add --name test --server example.com --port 443 --protocol vless
tunwarden profile list
tunwarden profile list --json
tunwarden profile show test
tunwarden profile show test --json
tunwarden profile delete test --yes
```

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

`profile list` prints a stable table:

```text
ID        NAME   PROTOCOL  SERVER       PORT
test      test   vless     example.com  443
```

`profile show <profile-id>` prints one normalized profile in human-readable form.

`profile delete <profile-id> --yes` removes the profile from local user state. The explicit `--yes` is required in the current v0.1 non-interactive CLI path because profile deletion removes persistent user state.

## JSON output

`profile list --json` and `profile show --json` are implemented and include `schema_version: "v1"`.

`profile add --json` and `profile delete --json` are not implemented in v0.1.

## Storage

Profiles are stored in the documented user state location:

```text
$XDG_STATE_HOME/tunwarden/profiles.json
```

When `XDG_STATE_HOME` is unset, the fallback is:

```text
~/.local/state/tunwarden/profiles.json
```

The profile store is user-owned state and must not require root. Writes use an atomic temporary-file-and-rename flow and store files with restrictive permissions.

## Validation and failure behavior

Manual profile input must include a valid name, protocol, server, and port. Invalid input fails clearly with exit code `2`.

Duplicate profile IDs fail without overwriting the existing profile.

Corrupt, unreadable, unsupported, or internally invalid profile storage fails safely with a clear error instead of silently discarding or rewriting user state.

## Safety boundary

Profile management mutates persistent local TunWarden user state only. It must not start Xray, contact a server, start network processes, or mutate TUN, routes, DNS, nftables, firewall, daemon runtime state, or system networking state.

## Deferred behavior

The following are not implemented in v0.1:

- `profile import <share-uri>`
- VLESS URI import
- subscription parsing
- Xray config generation
- connect/disconnect behavior
- TUN, route, DNS, nftables, or firewall mutation
