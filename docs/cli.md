# CLI reference

This is the canonical public CLI contract. Privileged ownership, recovery,
Privacy Envelope, restart, and package invariants are defined in
[ARCHITECTURE.md](../ARCHITECTURE.md).

## General rules

- `podlaz` is canonical. Packaged installs also provide the identical `plz` alias.
- The CLI runs as the invoking user and never directly mutates privileged Linux networking.
- Default output is human-readable and redacts credentials, endpoints where not needed for the user task, generated runtime configuration, transaction IDs, and ownership internals.
- Normal lifecycle commands do not prompt.
- `--yes` exists only for destructive profile/subscription deletion in non-interactive automation.
- `--json`, verbose evidence, and low-level recovery controls are exposed only under the advanced surface where they have a concrete diagnostic contract.

Exit codes:

| Code | Meaning |
| ---: | --- |
| `0` | Success. |
| `1` | Runtime/operation failure or an explicitly cancelled destructive action. |
| `2` | Invalid command, argument, or flag. |
| `3` | A diagnostic/status command found a confirmed unhealthy or cleanup-required condition. |
| `4` | Permission/authorization failure. |
| `5` | Required daemon access was unavailable. |

## Primary surface

```text
podlaz connect [profile]
podlaz disconnect
podlaz status

podlaz import <uri|url|file>
podlaz profile <list|show|use|delete>
podlaz subscription <list|show|update|delete>
podlaz autostart <enable|disable|status>
podlaz help
podlaz version
podlaz completion ...
```

Top-level help and completion expose this product surface plus the single
`debug` namespace. They do not advertise the internal validation/planning/check
or handoff policy machinery.

## Import

```bash
podlaz import <uri|url|file>
```

The one import entry point detects supported share URIs, local files, and
subscription sources and routes them through the existing validated/atomic
profile/subscription persistence paths.

Import never connects, starts Xray, contacts the daemon for lifecycle mutation,
or changes privileged host networking.

After import:

- if exactly one profile was produced and no valid profile is selected, its stable ID is selected persistently;
- if multiple profiles were produced, Podlaz never guesses which profile to select;
- an existing valid selection is preserved;
- output shows a concise count/name summary and the next ordinary action;
- subscription URLs, credentials, raw provider JSON, endpoints, UUIDs, and stable profile IDs are not printed merely to support onboarding.

Supported imported material includes VLESS/VMess/Trojan/Shadowsocks share URIs,
Base64 URI-list subscriptions, and native Xray JSON. Native Xray JSON is kept as
sensitive schema-opaque source material so Xray-owned fields survive persistence
and runtime composition; Podlaz does not expose the raw JSON in normal output.
Typed share-URI imports remain normalized profiles. Local and HTTP(S) imports also accept Clash/Mihomo YAML `proxies` lists with VLESS TCP, WebSocket, or gRPC transport and supported TLS/REALITY settings. Only the proxy definitions are imported: full Clash client configuration (rules, groups, DNS, mixed-port, unsupported proxy options and other protocols) is not translated. Unsupported semantics are reported, never silently applied or dropped. A recognized malformed JSON or YAML document does not fall back to URI/Base64 decoding. Connection support is stricter than import support. Native Xray JSON uses
schema-opaque runtime composition for canonical full-TUN: Podlaz replaces its
owned TUN inbound, preserves provider-owned egress/routing material, and applies
a collision-free Podlaz egress mark validated by the bundled Xray build before
unsafe host-network mutation. Explicit proxy-only remains a reduced-protection
alternative rather than an automatic fallback.

## Profile selection

```bash
podlaz profile list [--ids]
podlaz profile show <profile>
podlaz profile use <profile>
podlaz profile delete <profile> [--yes]
```

A `<profile>` selector resolves in this order:

1. exact stable profile ID;
2. otherwise an exact trimmed, case-insensitive display name that matches exactly one profile.

Ambiguous names fail without guessing. When a stable ID is needed for
disambiguation, `podlaz profile list --ids` explicitly reveals the exact IDs;
the default list continues to hide them. Display names are never ownership or
cleanup authority.

Selected-profile rules:

- `profile use` is the only explicit command that changes persistent selection;
- selection stores the stable profile ID, not the display name;
- `connect <profile>` is a one-shot choice and does not change selection;
- a stale selected ID is cleared rather than retargeted by name;
- if there is no valid selection and exactly one profile exists, Podlaz selects it persistently;
- zero or multiple profiles with no selection produce an actionable error;
- deleting the selected profile clears selection in the same atomic profile-store update;
- subscription refresh/removal that removes the selected stable ID clears selection and never retargets by display-name resemblance.

`profile list` is human-oriented. It marks the selected profile and omits
server endpoints, credentials, raw provider configuration, and stable IDs by
default. `profile list --ids` is the explicit disambiguation/automation view
that adds exact stable IDs; those IDs can be opaque and may encode provider
address material, so they are not shown by the normal list. `profile show` may
show the stable ID and redacted technical metadata, but not credentials or raw
provider JSON.

Profile deletion is destructive persisted user-data removal. Interactive
confirmation defaults to **No**; empty input and EOF never authorize deletion.
Non-interactive deletion requires `--yes`.

## Connect

```bash
podlaz connect [profile]
```

Canonical `connect` means full VPN/TUN protection.

For the explicit or selected profile, the normal connection path performs the
existing safe lifecycle internally: profile validation, TUN capability
validation, authoritative recovery/reconciliation, planning, protected connect,
and publishable health verification. Users do not run separate validate, plan,
check, doctor, or recover prerequisites.

Behavior:

- the same already-satisfied healthy full-VPN intent returns success without rebuilding the session;
- another exact Podlaz-owned protected TUN session may be replaced automatically through the existing protected replacement authority;
- TUN-to-TUN replacement preserves the Privacy Envelope and must not create an unprotected handoff gap;
- ambiguous or incomplete ownership blocks replacement;
- foreign VPN/network state is not stopped, adopted, or cleaned up to make connect succeed;
- canonical connect never silently falls back to Proxy-only;
- a profile that is renderable only as Proxy-only fails before privileged mutation and points to the explicit `podlaz debug proxy <profile>` action.

The old public `--mode` and `--handoff` policy matrices do not exist.

Successful default output is product-oriented:

```text
Connected
Profile: Work
Protection: Active
```

It does not expose provider endpoint identity, generated runtime configuration,
transaction IDs, route/DNS/firewall evidence, or reconciliation internals.

## Disconnect

```bash
podlaz disconnect
```

Disconnect expresses desired inactive state. Repeating it while Podlaz is
conclusively inactive succeeds. Cleanup remains exact-ownership-driven and never
uses observation or historical resemblance as authority.

Successful output:

```text
Disconnected
```

## Status

```bash
podlaz status
```

Default status is deliberately small and uses the product states:

```text
Status: Connected
Status: Connecting
Status: Reconnecting
Status: Disconnected
Status: Unknown
```

Where applicable it also shows:

```text
Profile: Work
Protection: Active
Autostart: Enabled for next boot
```

`Protection: Proxy only` is shown when an explicitly requested advanced
Proxy-only session is active. TUN is not printed as an implementation term in the
normal product view.

`Disconnected` requires conclusive inactivity. Unavailable or incomplete
inspection is `Unknown`, not an optimistic disconnect claim. Confirmed unhealthy
or cleanup-required status returns exit code `3`.

Detailed lifecycle, ownership, routing, DNS, firewall, transaction, and recovery
evidence belongs under `debug`.

## Subscription management

```bash
podlaz subscription list
podlaz subscription show <subscription-id>
podlaz subscription update <subscription-id>
podlaz subscription delete <subscription-id> [--yes] [--keep-profiles]
```

Normal onboarding uses `podlaz import`; there is no separate public
subscription-add flow.

Remote HTTP(S) subscriptions use `User-Agent: podlaz` and the existing stable
private `x-hwid` client identity. Fetch/parse/persistence failures preserve the
last committed subscription/profile state.

`subscription show` does not print the subscription URL. Deletion defaults to
removing profiles owned by that subscription; `--keep-profiles` keeps them.
Destructive confirmation defaults to **No**, and non-interactive deletion
requires `--yes`.

If an update/delete removes the selected stable profile ID, selection is cleared
rather than retargeted.

## Autostart

```bash
podlaz autostart enable [profile]
podlaz autostart disable
podlaz autostart status
```

`autostart enable` snapshots the explicit profile, or otherwise the selected
profile, with canonical full-VPN intent. It validates the same connection
material as canonical connect but does not connect immediately.

The daemon-owned Boot Autostart Manifest remains durable boot policy. A later
`profile use` does not silently rewrite an already-enabled manifest; users must
explicitly re-enable policy to bind a different profile.

`autostart status` shows the bound profile in human terms and shows
`Protection: Proxy only` only for pre-existing durable Proxy-only policy that
must remain interpretable for runtime safety. The redesigned CLI does not create
new Proxy-only autostart policy.

## Debug surface

```bash
podlaz debug --help
podlaz debug doctor ...
podlaz debug logs ...
podlaz debug recover ...
podlaz debug proxy <profile>
```

The top-level help does not enumerate diagnostic flags. Explicitly entering
`debug` reveals them.

### Diagnostics

```bash
podlaz debug doctor
podlaz debug doctor --tun [--verbose|-v|--json]
podlaz debug doctor --core --xray <path> [--json]
```

Diagnostics are read-only. TUN diagnostics inspect authoritative daemon/session,
route, resolver, connectivity, IPv6, and bounded PMTU evidence without repairing
networking or expanding cleanup authority.

### Logs

```bash
podlaz debug logs [--follow|-f] [--daemon] [--core] [--since <duration>]
```

`--since` accepts one positive decimal integer plus `s`, `m`, or `h`
(maximum `720h`). Output uses the normal redaction boundary.

### Recovery

```bash
podlaz debug recover [--json]
podlaz debug recover --execute [--json]
```

Recovery inspection/execution is retained because it is a distinct support and
fault-qualification capability. Execution uses only existing exact durable
Podlaz ownership authority. It does not prompt and has no `--yes` flag:
ambiguous/unowned state remains fail-closed and untouched, so confirmation cannot
broaden authority.

JSON retains the existing redacted diagnostic recovery model for support and
automated qualification.

### Explicit Proxy-only operation

```bash
podlaz debug proxy <profile>
```

This is the single advanced reduced-protection connection path. It is not a mode
matrix and is never an automatic fallback from canonical connect.

Proxy-only does not mutate TUN, routes, DNS, nftables, or firewall state.
Native Xray JSON (including grouped provider profiles) can use canonical
full-TUN when the bundled Xray build accepts the composed runtime config.
Proxy-only remains available as an explicit reduced-protection path, including
for provider material that conflicts with Podlaz-owned full-TUN requirements or
otherwise cannot participate in safe TUN planning.

Default success output makes the protection reduction explicit:

```text
Connected with reduced protection
Profile: Work
Protection: Proxy only
```

## Completion

```bash
podlaz completion bash
podlaz completion zsh
podlaz completion fish
```

Generated completion supports both `podlaz` and `plz`. Dynamic profile
completion prefers human-readable names and descriptions. When normalized names
are ambiguous, completion exposes stable IDs so automation remains deterministic.

Completion is read-only and does not contact the daemon or mutate networking.

## Removed public/operator surface

The redesign is an intentional clean break. The following are not public
commands/flags and have no compatibility aliases:

- top-level `plan`, `check`, `doctor`, `logs`, or `recover`;
- `profile add`, `profile import`, or `profile validate`;
- `subscription add`;
- public `--mode`, `--handoff`, `--plain`, or lifecycle `--yes`;
- primary list/show JSON schemas that had no durable post-redesign consumer.

The underlying safety-critical daemon/state machinery is not removed by this CLI
cleanup.

## User and daemon files

- User state: `$XDG_CONFIG_HOME/podlaz`, `$XDG_STATE_HOME/podlaz`, `$XDG_CACHE_HOME/podlaz`.
- Profiles and selected stable profile ID live in the atomic user-owned profile store.
- Daemon runtime: `/run/podlaz`.
- Persistent boot policy: `/var/lib/podlaz/boot-autostart-manifest.json`.
- Current-boot Network Session and recovery authority remain daemon-owned state described in [ARCHITECTURE.md](../ARCHITECTURE.md).
