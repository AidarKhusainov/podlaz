# CLI reference

Canonical reference for command names, arguments, flags, modes, exit codes, and
JSON support. Keep details out unless they affect users or scripts.

## Rules

- `podlaz` is canonical. `plz` is a packaged symlink alias with identical behavior.
- Default output is human-readable. Errors go to stderr.
- `--json` is stable only where implemented. Deferred JSON returns exit code `2`.
- Read-only commands do not require root.
- The CLI must not be SUID and must not directly mutate privileged Linux networking.
- Output must redact secrets and generated runtime configuration.

## Global

```bash
podlaz --help
podlaz help [command]
podlaz <command> --help
plz --help
```

| Flag | Meaning |
| --- | --- |
| `--json` | Stable JSON where implemented. |
| `--verbose` | Extra diagnostics where supported. |
| `--quiet` | Essential output where supported. |
| `--yes` | Confirm destructive or recovery execution. Long-only. |

| Mode | Meaning |
| --- | --- |
| `proxy-only` | Local proxy lifecycle. Default mode. |
| `tun` | Full-tunnel lifecycle through daemon-owned privileged state. |

| Exit | Meaning |
| ---: | --- |
| `0` | Success. |
| `1` | Runtime or operation failure. |
| `2` | Invalid usage, flags, arguments, or deferred JSON. |
| `3` | Diagnostic command found unhealthy state. |
| `4` | Permission or authorization failure. |
| `5` | Required daemon access was unavailable. |

## Commands

```bash
podlaz version
podlaz help [command]
podlaz completion bash|zsh|fish
```

Read-only. Completion generation prints static scripts and must not read user
state, contact the daemon, start Xray, mutate networking, or require root.

```bash
podlaz import <share-uri|local-path|file-or-http-url>
```

Imports supported profile or subscription input. Mutates user-owned podlaz state
only. Does not connect, start Xray, contact the daemon, require root, or mutate
host networking. `import --json` is deferred.

```bash
podlaz profile add --name <name> --server <host> --port <port> --protocol <vless|vmess|trojan|shadowsocks>
podlaz profile import <share-uri>
podlaz profile list [--json]
podlaz profile show <profile-id> [--json]
podlaz profile validate <profile-id> [--mode proxy-only|tun] [--json]
podlaz profile delete <profile-id> [--yes]
```

`list`, `show`, and `validate` are read-only. `add`, `import`, and `delete`
mutate user-owned profile state. `profile delete` requires confirmation in
non-interactive and JSON contexts unless `--yes` is passed. Validation failures
for an existing profile return exit code `3`.

```bash
podlaz subscription add --name <name> --url <url>
podlaz subscription update <subscription-id>
podlaz subscription list [--json]
podlaz subscription show <subscription-id> [--json]
podlaz subscription delete <subscription-id> [--yes] [--keep-profiles]
```

Supported source schemes: `file`, `http`, `https`. Supported response formats:
Base64 URI list and Xray JSON. `list` and `show` are read-only. `add`, `update`,
and `delete` mutate user-owned subscription/profile state. Failed update/delete
must preserve existing state. `delete --keep-profiles` keeps imported profiles.
`subscription update --json` and `subscription delete --json` are deferred.

```bash
podlaz status
```

Read-only. Uses daemon status when available and local fallback otherwise.
`status --json` is deferred.

```bash
podlaz doctor [--json]
podlaz doctor --core --xray <path> [--json]
podlaz doctor --network|--dns|--routes|--firewall [--json]
```

Read-only diagnostics. `doctor --core --xray <path>` is local-only and may emit
stable JSON. Other JSON/scoped forms are deferred unless implemented.

```bash
podlaz logs [--follow|-f] [--daemon] [--core] [--since <duration>]
```

Read-only journal output. `--daemon` selects daemon logs. `--core` selects Xray
lifecycle and forwarded stdout/stderr lines. `logs --json` is deferred.

```bash
podlaz plan --mode proxy-only <profile-id> [--json]
podlaz plan --mode tun <profile-id> [--json]
```

Read-only dry-run. Must not start Xray, write runtime config, or mutate host
networking.

```bash
podlaz connect [--mode proxy-only|tun] <profile-id>
podlaz disconnect
```

Requires daemon access. `connect` defaults to `proxy-only`. Proxy-only must not
mutate host networking. TUN mode is daemon-owned and transaction-backed.
`disconnect` is safe to repeat. `connect --json` and `disconnect --json` are
deferred.

```bash
podlaz recover
podlaz recover --execute --yes [--json]
```

`recover` is read-only. `recover --execute --yes` sends cleanup intent to the
daemon. The CLI must not perform privileged host cleanup directly. Ambiguous
resources are skipped. Non-interactive execution requires `--yes`.

## Files

- User state: `$XDG_CONFIG_HOME/podlaz`, `$XDG_STATE_HOME/podlaz`, `$XDG_CACHE_HOME/podlaz`.
- Daemon runtime: `/run/podlaz`, `/run/podlaz/podlazd.sock`, `/run/podlaz/transactions`.
- Generated runtime config is not persistent source of truth and must not be logged in full.

## See also

- [State and security](./state-and-security.md)
- [Debian package](./debian-package.md)
