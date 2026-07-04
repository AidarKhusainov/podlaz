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
- Human output must be stable without ANSI color. Commands that use symbolic status markers also support `--plain` where documented.

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

Packaged daemon commands may retry through the packaged abstract socket when the regular filesystem socket fails with a transport-level permission error. Errors returned by a reachable daemon keep their daemon/protocol classification: an authorization denial remains exit code `4`, while an unreachable daemon remains exit code `5`.

## Completion

```bash
podlaz completion bash|zsh|fish
plz completion bash|zsh|fish
```

Completion generation is read-only. It must not contact the daemon, start Xray,
mutate networking, or require root.

Generated scripts support both `podlaz` and `plz`. Interactive completion may
read local profile and subscription IDs. bash, zsh, and fish expose short
command/flag descriptions where the shell completion UI supports listing them.
Single inserted completions must not include description text.

## Commands

```bash
podlaz version
podlaz help [command]
```

Read-only.

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
podlaz profile validate <profile-id> [--mode proxy-only|tun] [--json] [--plain]
podlaz profile delete <profile-id> [--yes]
```

`list`, `show`, and `validate` are read-only. `add`, `import`, and `delete`
mutate user-owned profile state. `profile delete` requires confirmation in
non-interactive and JSON contexts unless `--yes` is passed. Validation failures
for an existing profile return exit code `3`.

`profile validate` prints a compact structured human result by default: profile
metadata, selected mode/backend/protocol, result, reason on failure, and next-step
guidance. `--plain` replaces Unicode status markers with ASCII status words.
`--json` preserves the existing machine-readable schema.

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

### VLESS xhttp Xray JSON profiles

Single-location VLESS profiles imported from Xray JSON subscriptions may use
`streamSettings.network: "xhttp"`. In `proxy-only` mode podlaz treats these as
renderable VLESS profiles, preserves the parsed `xhttpSettings.path` and
`xhttpSettings.host` fields, and generates a runtime Xray outbound containing
`streamSettings.network: "xhttp"` plus `xhttpSettings`. The daemon still owns
only the local SOCKS/HTTP listeners and must not mutate TUN, routes, DNS,
nftables, or firewall state for proxy-only connects.

`xhttp` is not enabled for `tun` mode yet. TUN validation and planning must fail
before host networking snapshots or mutations until the TUN bypass and routing
semantics are explicitly designed and tested.

### Grouped Remnawave/Xray JSON profiles

Some Remnawave subscriptions return one provider-owned Xray JSON object with
multiple `outbounds`, provider `routing`, and location selection/balancer rules
instead of independent single-location profile objects. podlaz imports such an
object as one subscription-owned `xray-json` grouped profile so duplicate
location/user identifiers do not collapse or overwrite each other.

Grouped `xray-json` support is intentionally mode-limited:

- `proxy-only` is supported. podlaz preserves provider-owned `outbounds`,
  `routing`, balancers, stream settings, and selection rules, then replaces
  provider `inbounds` with podlaz-owned local SOCKS/HTTP listeners at runtime.
- `tun` is not supported yet. `profile validate --mode tun`, `plan --mode tun`,
  and `connect --mode tun` must fail before mutation with a clear unsupported
  grouped-profile diagnostic, because podlaz cannot safely derive one VPN server
  bypass from provider-owned routing.
- CLI profile output must not print raw provider Xray JSON, UUIDs, user identity,
  or generated runtime config. Stored provider source JSON is treated as
  sensitive profile material and is redacted in human and JSON output.

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
podlaz plan --mode tun <profile-id> [--json] [--verbose|-v] [--plain]
```

Read-only dry-run. Must not start Xray, write runtime config, or mutate host
networking. Grouped `xray-json` profiles support `proxy-only` planning only;
`plan --mode tun` fails before collecting a host networking snapshot.

`plan --mode tun` prints a compact human summary by default: profile status,
planned high-level changes, blockers, warnings, safety notes, and next-step
guidance. It intentionally hides raw nftables rules, rollback keys, ownership
labels, and long command stderr in default human output. Use `--verbose` or `-v`
for the detailed TUN/route/policy-rule/DNS/nftables/snapshot/rollback dump.
`--plain` replaces Unicode status markers with ASCII status words. `--json`
preserves the existing automation schema and is not affected by `--verbose`.

```bash
podlaz connect [--mode proxy-only|tun] <profile-id>
podlaz disconnect
```

Requires daemon access. `connect` defaults to `proxy-only`. Proxy-only must not
mutate host networking. TUN mode is daemon-owned and transaction-backed.
`disconnect` is safe to repeat. `connect --json` and `disconnect --json` are
deferred.

```bash
podlaz check <profile-id> [--target <target-id>] [--timeout <duration>] [--json]
podlaz check --all [--target <target-id>] [--timeout <duration>] [--json]
```

Explicit bounded proxy-only profile diagnostics. The command validates profile
renderability first, measures direct server TCP reachability when the profile
exposes one server endpoint, uses daemon status to avoid disrupting an already
active connection, starts temporary proxy-only Xray only through `podlazd` when
the daemon is inactive, probes local SOCKS/HTTP egress through loopback
listeners, runs a small documented service target set, and disconnects only the
temporary proxy connection that the check started.

`check` never mutates TUN devices, routes, DNS, nftables, firewall rules, or host
resolver files. It does not replace or disconnect an existing active connection.
Every network probe is bounded by `--timeout` and the default target set is
conservative. `--all` runs profiles with deterministic output and a small default
concurrency limit. A non-`ok` check returns exit code `3`.

Supported target ids are `cloudflare`, `github`, `google`, `instagram`,
`telegram`, and `youtube`. Each target is a best-effort diagnostic probe with a
known hostname/URL, timeout, expected HTTP/TLS success condition, proxy-side DNS
resolution, and a privacy note in the target catalog. A successful probe means the
specific low-impact endpoint was reachable through the proxy path; it does not
guarantee that the full application behavior works.

`check --json` emits stable JSON with `schema_version`, `status`, `warnings`,
`errors`, profile metadata, validation result, daemon result, server TCP result,
proxy startup result, SOCKS/HTTP egress results, and per-service results. Human
and JSON output use the same redaction rules.

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
