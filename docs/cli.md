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
| `tun` | Full-tunnel lifecycle through daemon-owned privileged state and native Xray TUN packet ingestion. |

| Exit | Meaning |
| ---: | --- |
| `0` | Success. For active TUN status, current health must not contain a confirmed unhealthy/cleanup-required condition. |
| `1` | Runtime or operation failure. |
| `2` | Invalid usage, flags, arguments, or deferred JSON. |
| `3` | Diagnostic command found confirmed unhealthy state, such as `degraded` or `cleanup-required`; `Connecting`/`Reconnecting` alone do not imply exit `3`. |
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
Runtime lifecycle warnings are rendered separately from recovery/inspection
failures and do not by themselves make an otherwise healthy active session
unhealthy. A clean startup recovery scan is described relative to the current
lifecycle state, so an active TUN session is never labelled as a clean inactive
state. `status --json` is deferred.

Default human `status` is a product view, not an operator dump. It renders one of:

```text
Status: Connected
Status: Connecting
Status: Reconnecting
Status: Disconnected
Status: Unknown
```

`Disconnected` is used only when the lifecycle is conclusively `inactive`.
Unavailable daemon access, inaccessible socket state, stale/incomplete local
inspection, or any other state without evidence of inactivity is `Unknown`, not
`Disconnected`. `Connecting` is an admitted explicit or boot connect that has not
yet established the product session. `Reconnecting` is an established protected
session while current evidence is being revalidated/rebuilt. These transient
product states do not by themselves imply diagnostic exit `3`.

The default human view may additionally show `Profile`, `Mode`, the persistent
autostart policy, and a short stable `Reason` after a conclusively terminal
lifecycle. It intentionally omits service/runtime configuration, proxy listener
details, routes, DNS, firewall, transaction identifiers, recovery candidates,
and other operator evidence. Those details remain available through daemon status
internals, `doctor`, `doctor --tun`, and `recover`.

For an active TUN session, durable transaction state and current health are
separate contracts. `committed` means the transaction completed successfully for
the generation verified at that time; it is not permanent proof that the current
host network is still usable. Daemon status therefore exposes `tun_health` with:

- `state`: `verified`, `revalidating`, `degraded`, or `cleanup-required`;
- positive `network_generation`;
- a stable classification while health is not `verified`, including
  `uplink_revalidating`, `uplink_changed`, `uplink_fingerprint_unavailable`,
  `ownership_invalid`, `owned_state_invalid`, `connectivity_failed`,
  `revalidation_timeout`, and `revalidation_interrupted`.

`revalidating` and `degraded` can be transient active publications while the
current generation is being proved or a terminal verification outcome is being
handed off. A proved verification failure or revalidation deadline is not a
stable active `degraded` state. The daemon keeps the old health proof invalid,
persists a bounded redacted TUN diagnostic report before cleanup, releases
revalidation authority, and automatically invokes the normal bounded lifecycle
`Disconnect`. Successful cleanup converges to inactive status. Failed rollback
keeps the surviving owned state fail-closed as `cleanup-required` for recovery.
Cancellation caused by an explicit user disconnect, recovery, or daemon shutdown
is not treated as another terminal verification failure and does not schedule a
second automatic disconnect.

Generation 1 becomes `verified` only after a fresh post-commit observation has
passed the canonical composition verifier and connectivity verifier for that
exact observation. `resume` and event-source resubscription force a same-generation
reproof even when the underlying fingerprint is unchanged. Ordinary duplicate
link/address/route hints with an unchanged already-verified fingerprint are
coalesced and do not run redundant probes.

Lifecycle mutation has priority over revalidation without losing evidence. An
event consumed while connect, disconnect, or recovery is pending waits for the
mutation queue to become idle. An in-flight probe interrupted by mutation is
requeued. The post-mutation attempt always starts with a fresh authoritative
snapshot and then applies the normal fingerprint/generation decision. The
verification phase itself is read-only and does not repair networking or expand
cleanup authority. Only a terminal verification failure/deadline can hand off to
the existing exact transaction-backed lifecycle disconnect described above;
ambiguous observation or foreign ownership never gains cleanup authority.

A confirmed active `degraded` or `cleanup-required` condition returns status exit
code `3`. `Connecting`/`Reconnecting` are lifecycle phases and do not themselves
make status unhealthy. A successful automatic fail-safe disconnect publishes the
normal `Disconnected` state with a stable typed high-level reason when the
terminal outcome is still the latest relevant lifecycle. Current-health failure
does not rewrite historical commit evidence.

```bash
podlaz doctor
podlaz doctor --tun [--verbose|-v|--json]
podlaz doctor --core --xray <path> [--json]
podlaz doctor --network|--dns|--routes|--firewall [--json]
```

Read-only diagnostics. `doctor --core --xray <path>` is local-only and may emit
stable JSON. The `--network`, `--dns`, `--routes`, and `--firewall` scopes remain
deferred.

Daemon-backed base diagnostics interpret managed-looking resources through typed
lifecycle authority instead of treating mere presence as stale state. During a
clean committed active TUN session, the exact transaction-owned `podlaz0` link
and `inet podlaz` table are expected. Missing resources, link identity mismatch,
missing/incomplete transaction authority, cleanup-required state, and ambiguous
ownership remain warnings. Local fallback has no daemon lifecycle authority and
therefore stays conservative: managed-looking resources are not assumed owned.
The check is read-only and never repairs or removes networking state.

`doctor --tun` is daemon-backed and requires an active podlaz TUN session or a
saved latest TUN report. It runs a bounded dependency-aware sequence for active
session/ownership metadata, the VPN server bypass, IPv4 policy routing,
`systemd-resolved` link ownership, DNS over UDP and TCP, positive system
resolution, reserved `.invalid` NXDOMAIN integrity, TCP/443, TLS, small HTTPS,
two independent RFC 8484 DoH providers, IPv6 state/leak detection, and guarded
PMTU evidence. The command must not mutate routes, policy rules, DNS, MTU,
nftables, services, other VPNs, or browser state.

Compact human output identifies the failed layer, primary classification, latest
report path, and next step. `--verbose` adds bounded route, DNS, TLS, HTTP, IPv6,
command, and timing evidence. `--json` emits the same centrally redacted model
with `schema_version: 1`. Historical failed-connect and terminal-revalidation
reports expose stable `failure_phase` and `rollback_status`. A report with status
`unhealthy` or `unavailable` returns exit code `3`.

Stable classifications include session and ownership inconsistencies;
`network_apply_failure` and `network_verify_failure`; server bypass, route, and
policy-rule failures; DNS apply/conflict/UDP/TCP/resolution/hijack failures; TCP,
TLS, HTTPS, DoH partial/full failures; IPv6 absent, unusable, or leak states;
guarded `likely_pmtu_blackhole`; timeout, cancellation, and internal diagnostic
failures. One DoH provider failure is degraded. PMTU is reported only when small
HTTPS succeeds, two independent bounded 16 KiB transfers fail, and no lower-layer
failure explains the symptom.

The endpoint catalog is source-controlled and documents a stable target id,
timeout, response-size bound, required/best-effort status, bootstrap addresses
where applicable, and privacy note. Unit tests use local fixtures and do not
contact live endpoints.

```bash
podlaz logs [--follow|-f] [--daemon] [--core] [--since <duration>]
```

Read-only journal output. `--daemon` selects daemon logs. `--core` selects
structural Xray lifecycle and child-output-observed events. `--since` accepts
exactly one positive decimal integer followed by one unit `s`, `m`, or `h`, for
example `30s`, `15m`, `2h`, or `36h`, with a maximum of `720h`. Zero, signed,
fractional, compound, unsupported-unit, date-like, and journalctl-native values
are invalid usage and return exit code `2` before `journalctl` is started. Podlaz
translates a valid duration to one relative journal argument such as
`--since -36h`; the same normalization is used for daemon/core and follow modes.
Raw Xray stdout/stderr payloads, profile identifiers, endpoints, UUIDs,
runtime-config paths, and other opaque child text are not persisted to journald.
`logs --json` is deferred.

```bash
podlaz plan --mode proxy-only <profile-id> [--json]
podlaz plan --mode tun <profile-id> [--json] [--verbose|-v] [--plain]
```

Read-only dry-run. Must not start Xray, write runtime config, or mutate host
networking. Grouped `xray-json` profiles support `proxy-only` planning only;
`plan --mode tun` fails before collecting a host networking snapshot.

`plan --mode tun` prints a compact human summary by default: profile status,
the collision-free daemon-owned TUN IPv4 address selected from the current
read-only host snapshot, planned high-level changes, blockers, warnings, safety
notes, and next-step guidance. The historical `198.18.0.1/32` address, routing
table `51820`, and priorities `9999`/`10000` are preferred candidates only. If a
candidate is already occupied by unrelated host state, planning selects another
verified-free session identity. If authoritative allocation evidence is
incomplete or the bounded candidate space has no safe allocation, planning fails
closed and renders no misleading applicable plan. It intentionally hides raw
nftables rules, rollback keys, ownership labels, and long command stderr in
default human output. Use `--verbose` or `-v` for the detailed
TUN/route/policy-rule/DNS/nftables/snapshot/rollback dump. `--plain` replaces
Unicode status markers with ASCII status words. `--json` preserves the existing
automation schema and is not affected by `--verbose`.

```bash
podlaz connect [--mode proxy-only|tun] [--handoff=block|ask|stop-known|replace-podlaz] <profile-id>
podlaz disconnect
```

Requires daemon access. `connect` defaults to `proxy-only`. Proxy-only must not
mutate host networking. TUN mode is daemon-owned and transaction-backed. Xray
owns `podlaz0` creation, lifetime, and packet ingestion through its native
`tun` inbound. Before the first host-network mutation, podlazd selects a
collision-free Network Session allocation from the authoritative host baseline
and persists the exact TUN IPv4 `/32`, routing table, and policy priorities in
transaction desired state. It then rolls back only exact resources that acquire
durable applied/rollback ownership evidence. Before handoff or host changes,
`connect --mode tun` checks that the packaged Xray helper accepts a minimal
pinned-schema native TUN config. The profile-generated Xray runtime config is
written later after the TUN transaction starts.

For non-interactive TUN connects, the daemon automatically recovers only exact
durable podlaz transaction state that requires cleanup, then recollects the host
snapshot and allocates the new session independently around unrelated host
networking. Foreign TUN devices, policy routing, route-only DNS owners,
NetworkManager VPN connections, and unrelated firewall state are baseline rather
than blockers merely because they exist. The daemon does not stop or rewrite
such foreign state to make the host look clean. A connect is blocked only when
recovery remains incomplete, authoritative allocation evidence is insufficient,
the bounded candidate space is exhausted, or a concrete safe server bootstrap /
data-plane plan cannot be built without colliding with or mutating unowned state.
If a foreign object races into an already selected session identity before apply,
apply fails instead of adopting that object as Podlaz-owned.

`connect --mode tun` accepts explicit handoff policies. The default `block`
policy still permits exact Podlaz self-recovery described above but never stops a
foreign VPN or removes ambiguous state. `ask` is rejected in daemon/non-interactive
connect paths and performs no recovery or handoff mutation. `stop-known` remains
accepted for CLI compatibility but does not broaden new-session authority to stop
foreign NetworkManager VPN connections; coexistence allocation treats them as
baseline. `replace-podlaz` may disconnect the exact active Podlaz TUN session
before starting a new allocation. Unsupported handoff values fail before network
mutation. `disconnect` is safe to repeat. `connect --json` and `disconnect --json`
are deferred.

Successful human lifecycle output is intentionally concise:

```text
Connected
Profile: Example VPN
Mode: TUN
```

and:

```text
Disconnected
```

For failures during `network-apply`, `network-verify`, later connect-time
connectivity verification, or a proved post-commit revalidation failure/deadline,
podlazd runs bounded redacted diagnostics while the relevant failed state still
exists and atomically saves the report before the first rollback command. The
report records a stable classification, `failure_phase`, and `rollback_status`;
rollback finalizes the historical status as `completed` or `failed`. Diagnostic
collection remains best-effort and cannot suppress cleanup. Post-commit terminal
revalidation cleanup is the same normal exact transaction-backed `Disconnect`
path; it is started only after revalidation authority is released. Explicit
user/shutdown cancellation owns its own lifecycle cleanup and therefore does not
schedule a duplicate automatic disconnect.

Before commit, static resolved ownership is followed by an uncached IPv4
`resolvectl` query bound to the exact `podlaz0` link, a separate normal system
resolver lookup, and route verification for at least one returned IPv4 address.
`Current Scopes` remains diagnostic evidence only. The returned error includes
the stable classification and safe report path when available and directs the
user to `podlaz doctor --tun --verbose`.

```bash
podlaz autostart enable [--mode proxy-only|tun] <profile-id>
podlaz autostart disable
podlaz autostart status
```

`autostart enable` reads and validates the selected user-owned profile exactly as
normal `connect`, then submits a minimal canonical snapshot to the daemon-owned
persistent Boot Autostart Manifest. It does not connect immediately. Configuration
written in boot A is eligible only on a later boot, so restarting `podlazd` in
boot A cannot turn `enable` into an immediate connect.

`autostart disable` removes only future-boot policy. It does not disconnect an
active session, cancel an already-admitted current-boot attempt, or reset the
one-attempt/no-retry authority. `autostart status` is read-only. Human output is:

```text
Autostart: Enabled for next boot
```

or:

```text
Autostart: Disabled
```

When enabled, profile name and mode may also be shown. `autostart --json` is not
a public schema yet and returns deferred-JSON usage behavior.

At daemon startup, current-boot Network Session continuation/recovery always has
priority over fresh autostart. With no continuation, the daemon may admit at most
one logical autostart attempt for the current boot. It first performs bounded
fresh uplink-readiness observation inside that admitted attempt, then enters the
same canonical `Connect` lifecycle as an explicit request. Daemon replacement
continues the exact pinned attempt. `succeeded` or conclusively `terminal` consumes
automatic-connect authority for the remainder of the boot; explicit disconnect
or a later runtime terminal failure never causes a same-boot autostart retry.

A stable terminal reason belongs to the latest relevant product lifecycle, not to
the permanent current-boot no-retry authority. A newer admitted explicit
lifecycle supersedes an older reason. If that new explicit connect itself reaches
a conclusively clean terminal failure, it records a new typed reason. A request
rejected before lifecycle admission leaves the previous valid reason unchanged.

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

`recover` is a read-only inspection of the same recovery model used by
execution. When daemon startup evidence is available, dry-run projects the
current startup scan plus the bounded `network_session` recovery state; otherwise
it falls back to conservative local inspection and does not invent daemon
authority. `recover --execute --yes` sends cleanup intent to the daemon and then
runs the same Network Session follow-up lifecycle when startup recovery remains
blocked. The CLI must not perform privileged host cleanup directly. Ambiguous or
unowned resources are skipped, and non-interactive execution requires `--yes`.

The stable `network_session` projection contains only semantic recovery evidence:
`authority`, `intent`, `startup_gate`, optional `resume_stage`,
`last_resume_outcome`, optional `last_tun_failure_phase`, optional
`rollback_status`, `transaction_present`, `legacy_migration`,
`cleanup_authority`, and `next_action`. It deliberately excludes profile/server
identity, Network Session and transaction identifiers, generated config, and raw
child output. `resume_stage` can identify state load, legacy migration, Privacy
Envelope reconciliation, exact transaction recovery, generic recovery, connect
replay, or terminal teardown. `last_resume_outcome` is one of `not-attempted`,
`failed`, `incomplete`, or `succeeded`; `next_action` is `retry-resume`,
`continue-teardown`, `manual-diagnosis`, or `none`.

Execution is complete only when ordinary cleanup has no failed/incomplete result
and Network Session recovery has an open startup gate with `next_action: none`.
A blocked gate or any remaining next action is incomplete and makes execute return
exit code `1`; cleanup failure also returns `1`. JSON execute output reports
`status: warn` for incomplete convergence and `status: fail` for cleanup failure.
Dry-run JSON reports `status: warn` when cleanup candidates, Network Session
authority requiring convergence, or incomplete inspection are present. In human
output, `No podlaz-owned recovery candidates found.` is shown only when there are
no ordinary cleanup candidates and no retained Network Session recovery plan; a
retained Network Session plan is rendered instead of that empty-state message.

For the validated podlaz-owned `podlaz0` target, only an exact `resolvectl` exit code `1`
with one exact supported bounded `No such device` result is accepted as
idempotent success. A successful `resolvectl status` is accepted as a clean
transient record only when it has no stderr, its unique target section passes
strict parsing, `Current Scopes` is exactly `none`, current/server/domain DNS
state is empty, and `Protocols` contains explicit `-DefaultRoute` without
`+DefaultRoute`. A stale `dns-link` candidate requires concrete podlaz per-link
DNS configuration. Missing or conflicting DefaultRoute polarity, unexpected
stderr, malformed or partial output, duplicate target sections, operational
failure, or concrete non-podlaz DNS state remains unknown and fail-closed. The
supported Ubuntu 24.04 missing-link form is `Failed to resolve interface
"podlaz0", ignoring: No such device`; the older exact form without `, ignoring`
remains supported. Timeout, cancellation, signals, launch or permission errors,
other exit codes, extra output, and unrelated exit `1` results remain failures.
A successful daemon scan is authoritative over older local evidence; a failed
refresh is reported as incomplete rather than reusing stale candidates or
top-level `ok`.

## Files

- User state: `$XDG_CONFIG_HOME/podlaz`, `$XDG_STATE_HOME/podlaz`, `$XDG_CACHE_HOME/podlaz`.
- Daemon runtime: `/run/podlaz`, `/run/podlaz/podlazd.sock`, `/run/podlaz/transactions`.
- Persistent boot policy: `/var/lib/podlaz/boot-autostart-manifest.json` under systemd `StateDirectory=podlaz`.
- Current-boot autostart authority: `/run/podlaz/boot-autostart-attempt.json`.
- Current-boot product terminal outcome: `/run/podlaz/product-terminal-reason.json`.
- Latest TUN diagnostic report: `/run/podlaz/diagnostics/tun-last.json` (daemon-owned, replacement-only, mode `0600`, bounded to 256 KiB).
- Generated runtime config is not persistent source of truth and must not be logged in full.

## Related project documentation

- [Project overview](../README.md)
- [Architecture](../ARCHITECTURE.md)
- [Contributor workflow](../AGENTS.md)
