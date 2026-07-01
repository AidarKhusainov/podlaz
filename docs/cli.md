# CLI reference

This document is the canonical reference for `podlaz` command names,
arguments, flags, exit codes, and safety invariants. It intentionally stays
short; detailed behavior belongs in the feature-specific docs linked from
`docs/README.md`.

## Invariants

- `podlaz` is the canonical command. Packaged installs may also expose `plz` as
  a symlink alias with identical behavior.
- Default output is human-readable. Errors go to stderr.
- Commands that support stable automation output use `--json`.
- Commands with deferred JSON support fail fast with exit code `2`.
- Read-only commands must not require root.
- The CLI must not be SUID and must not directly mutate privileged Linux
  networking state.
- `proxy-only` must not mutate routes, DNS, TUN devices, nftables, or firewall
  state.
- `tun` execution is daemon-owned and must use transaction/rollback metadata.
- Output must redact share URIs, subscription URLs, credentials, provider
  tokens, generated core configs, authorization headers, private keys, and
  secret-looking values.

## Global behavior

```bash
podlaz --help
podlaz help [command]
podlaz <command> --help
plz --help
```

Common flags:

| Flag | Meaning |
| --- | --- |
| `--json` | Print stable JSON where implemented. |
| `--verbose` | Print extra diagnostic detail where supported. |
| `--quiet` | Print only essential output where supported. |
| `--yes` | Confirm destructive or recovery execution. Long-only. |

Connection modes:

| Mode | Meaning |
| --- | --- |
| `proxy-only` | Local proxy lifecycle. Default mode. |
| `tun` | Full-tunnel lifecycle through daemon-owned privileged state. |

Exit codes:

| Code | Meaning |
| ---: | --- |
| `0` | Success; diagnostic commands found no unhealthy state. |
| `1` | Runtime or operation failure. |
| `2` | Invalid usage, flags, arguments, or deferred JSON. |
| `3` | Diagnostic command completed but found unhealthy state. |
| `4` | Permission or authorization failure. |
| `5` | Required daemon access was unavailable. |

## Commands

### Version and help

```bash
podlaz version
plz version
podlaz help [command]
```

Read-only. Does not require daemon access.

### Shell completion

```bash
podlaz completion bash|zsh|fish
plz completion bash|zsh|fish
```

Prints static completion scripts to stdout. Completion generation must not read
user state, contact `podlazd`, start Xray, mutate networking, or require root.
Generated scripts must support both `podlaz` and `plz`.

### Import

```bash
podlaz import <share-uri|local-path|file-or-http-url>
```

Imports supported profile or subscription input without connecting. Local files
may be Xray JSON, plain URI lists, or Base64 URI lists. `file://`, `http://`,
and `https://` inputs create subscription sources and import supported profiles.

Safety: import mutates only user-owned podlaz state. It must not start the
daemon, start Xray, require root, create TUN devices, or mutate host networking.
`import --json` is deferred and returns exit code `2`.

### Profile

```bash
podlaz profile add --name <name> --server <host> --port <port> --protocol <vless|vmess|trojan|shadowsocks>
podlaz profile import <share-uri>
podlaz profile list [--json]
podlaz profile show <profile-id> [--json]
podlaz profile validate <profile-id> [--mode proxy-only|tun] [--json]
podlaz profile delete <profile-id> [--yes]
```

`list`, `show`, and `validate` are read-only. `add`, `import`, and `delete`
mutate user-owned profile state only. `profile delete` requires confirmation in
non-interactive and JSON contexts unless `--yes` is passed.

`profile validate` checks renderability for the selected mode without starting
Xray, contacting the daemon, writing runtime config, requiring root, or mutating
networking. Validation failures for an existing profile return exit code `3`.

### Subscription

```bash
podlaz subscription add --name <name> --url <url>
podlaz subscription update <subscription-id>
podlaz subscription list [--json]
podlaz subscription show <subscription-id> [--json]
podlaz subscription delete <subscription-id> [--yes] [--keep-profiles]
```

Supported source schemes are `file`, `http`, and `https`. Supported response
formats are Base64 URI lists and Xray JSON objects/arrays.

`list` and `show` are read-only. `add`, `update`, and `delete` mutate
user-owned subscription/profile state only. Failed update or delete must preserve
existing state. `delete --keep-profiles` removes subscription metadata while
leaving imported profiles in place.

`subscription update --json` and `subscription delete --json` are deferred and
return exit code `2`.

### Status

```bash
podlaz status
plz status
```

Read-only. Uses daemon-backed status when available and a conservative local
fallback otherwise. `status --json` is deferred and returns exit code `2`.

### Doctor

```bash
podlaz doctor [--json]
podlaz doctor --core --xray <path> [--json]
podlaz doctor --network|--dns|--routes|--firewall [--json]
```

Read-only diagnostics. The default command uses daemon-backed diagnostics when
available and local checks otherwise. `doctor --core --xray <path>` is local-only
and may emit stable JSON. Other JSON/scoped forms are deferred unless explicitly
implemented and return exit code `2`.

### Logs

```bash
podlaz logs [--follow|-f] [--daemon] [--core] [--since <duration>]
```

Read-only journal output. `--daemon` selects daemon logs. `--core` selects
Xray lifecycle and forwarded stdout/stderr lines. `--json` is deferred and
returns exit code `2`.

### Plan

```bash
podlaz plan --mode proxy-only <profile-id> [--json]
podlaz plan --mode tun <profile-id> [--json]
```

Read-only dry-run. `proxy-only` shows generated config paths and local proxy
listeners. `tun` shows intended TUN, route, policy-rule, DNS, firewall, and
rollback state before mutation. A plan must not start Xray, write runtime config,
or mutate host networking.

### Connect and disconnect

```bash
podlaz connect [--mode proxy-only|tun] <profile-id>
podlaz disconnect
```

Requires the local daemon API. `connect` defaults to `proxy-only`. Proxy-only
mode starts daemon-managed Xray runtime state without host-network mutation. TUN
mode is daemon-owned and must apply/verify/rollback through transaction state.
`disconnect` stops the active daemon-managed lifecycle and is safe to repeat.

`connect --json` and `disconnect --json` are deferred and return exit code `2`.

### Recovery

```bash
podlaz recover
podlaz recover --execute --yes [--json]
```

`recover` is a read-only dry-run. `recover --execute --yes` sends explicit
cleanup intent to the daemon. The CLI must not directly perform privileged host
cleanup. Ambiguous resources are skipped. Non-interactive execution requires
`--yes`.

## Files and state

User-owned profile/subscription state follows the XDG base directories. Packaged
daemon runtime state uses `/run/podlaz/`, including `/run/podlaz/podlazd.sock`
and transaction/runtime config subdirectories. Generated runtime config is not
persistent source of truth and must not be logged in full.

## See also

- [State and security requirements](./state-and-security.md)
- [Daemon API](./daemon-api.md)
- [Package boundaries](./package-boundaries.md)
- [Debian package contract](./debian-package.md)
- [Networking reliability](./networking-reliability.md)
