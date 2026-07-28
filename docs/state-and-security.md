# State and security

This is the only permanent engineering reference for state ownership, redaction,
daemon privilege boundaries, and privileged networking safety.

## State ownership

| State | Location | Owner |
| --- | --- | --- |
| User config/state/cache | `$XDG_CONFIG_HOME/podlaz`, `$XDG_STATE_HOME/podlaz`, `$XDG_CACHE_HOME/podlaz` | invoking user |
| Daemon runtime | `/run/podlaz` | `podlazd` via systemd `RuntimeDirectory=` |
| Daemon persistent state | `/var/lib/podlaz` | `podlazd` via systemd `StateDirectory=` |
| Transaction files | `/run/podlaz/transactions/*.json` | `podlazd` |
| Latest TUN diagnostic report | `/run/podlaz/diagnostics/tun-last.json` | `podlazd` |
| Generated runtime config | `/run/podlaz/generated/` | `podlazd` and the dedicated core child identity |
| TUN packet ingestion | `podlaz0` native Xray `tun` inbound | Xray child process, orchestrated by `podlazd` |

Rules:

- User profile/subscription state must not require root.
- Runtime config is generated output, not persistent source of truth.
- Transaction files must be atomic, private, versioned, and redaction-safe.
- The latest TUN diagnostic report is replacement-only, atomically written,
  bounded to 256 KiB, and mode `0600`; podlaz keeps no unbounded diagnostic
  history under `/run`.
- A diagnostic persistence failure must clear the public report location and
  produce `internal_diagnostic_error`; output and logs must not claim that a
  report exists when atomic replacement failed. This includes failures before
  replacement and directory-sync failures after rename. Save and load are
  serialized so a reader cannot accept a report from an unsuccessful attempt.
- Read-only commands may inspect state but must not clean it up.

## CLI and daemon boundary

- The CLI parses user intent and talks to the local daemon API.
- The CLI must not be SUID and must not directly mutate TUN devices, routes,
  policy rules, DNS, nftables, firewall state, or system resolver files.
- Privileged host changes belong to `podlazd` and must be transaction-backed.
- `proxy-only` must not mutate host networking.
- `tun` execution must record enough durable desired and applied state to recover
  after failure or daemon restart.
- The active TUN transaction records the original VPN server endpoint and TLS
  server name needed to distinguish hostname/SNI semantics from resolved bypass
  addresses in diagnostics.
- `doctor --tun` is daemon-backed because authoritative active-session,
  transaction, route, resolver, and ownership evidence belongs to the daemon.
  The diagnostic handler is read-only apart from atomically replacing the
  private latest-report file.

Packaged daemon access has two local socket boundaries. The filesystem socket is tried first. A transport-level permission failure may fall back to the packaged abstract socket, where the daemon can enforce peer-credential/polkit authorization without widening filesystem socket access. Once the daemon returns an HTTP, authorization, JSON, or schema error, that response is authoritative and must not be downgraded to a generic daemon-unavailable error.

## Networking safety

TUN mode may touch only podlaz-owned networking state around the Xray-owned TUN link:

- podlaz-owned routes and policy rules;
- podlaz-owned DNS link state;
- podlaz-owned nftables/firewall table, chains, and rules.

Xray owns `podlaz0` packet ingestion through the native `tun` inbound. `podlazd` may verify that `podlaz0` exists before applying host networking, but it must not record Xray-created `podlaz0` as a podlaz-owned TUN device rollback target. Stopping Xray is the release mechanism for the Xray-owned TUN link.

Apply/verify/rollback must be explicit. Normal in-process rollback must remove only what the active transaction recorded as applied. If the daemon stops inside the apply crash window before an applied category is persisted, recovery may reconstruct only missing entries in reserved podlaz namespaces from the durable structured `desired_plan`: routing table `51820`, policy priority `10000`, DNS link `podlaz0`, and nftables table `inet podlaz`. Desired-only main-table server bypass state must be inspected but never deleted by assumption; a present route or rule without durable applied ownership evidence keeps the transaction blocked for explicit inspection. Ambiguous host state must be skipped, not guessed.

A low-level composition executor must not perform hidden cleanup after a child apply method reports that it mutated state. It returns the partial non-zero applied step together with the error. The transaction boundary persists that ownership and controls rollback timing: direct helpers perform one immediate bounded fail-safe rollback, while the production lifecycle persists diagnostics before invoking rollback. A zero step means no owned mutation was recorded and is not added to the rollback plan.

For `systemd-resolved`, apply first performs a scoped `resolvectl revert podlaz0` and then writes the planned DNS servers, `~.` route-only domain, and DNS default-route setting. An already-missing link is idempotent only when the command outcome matches the supported missing-link contract. If the kernel link exists before `systemd-resolved` registers it, only transient missing-link results from the `dns`, `domain`, and `default-route` commands are retried for a bounded interval of roughly two seconds. Verification checks the target link, planned DNS servers, `~.`, `+DefaultRoute`, and absence of a foreign active `~.` owner. `Current Scopes` is derived runtime state and must not be used as proof that the per-link configuration was or was not applied. The target `podlaz0` section must be unique; duplicate target sections are ambiguous and fail closed.

For native Xray TUN startup, durable rollback order is:

1. roll back podlaz-owned nftables, DNS, routes, and policy rules;
2. stop the Xray child process;
3. verify or surface stale `podlaz0` state through recovery/status diagnostics.

If `network-apply`, `network-verify`, or later connectivity verification fails,
`podlazd` first attempts a short bounded, cancellation-aware safe diagnostic
subset while the failing host state still exists. The report contains a stable
classification, lifecycle `failure_phase`, and `rollback_status`. Known network
apply and verification failures use `network_apply_failure` and
`network_verify_failure`; timeout, cancellation, and internal failures retain
their existing stable classifications. Overall report status such as `unhealthy`
is never substituted for the classification taxonomy.

Optional diagnostics must never delay or suppress cleanup. Normal rollback runs
with a separate daemon-owned bounded cleanup context derived without the
requesting HTTP client's cancellation, so a disconnected client or expired
request deadline cannot immediately cancel DNS, route, rule, or nftables cleanup.
The historical report is first persisted with rollback status `pending` and then
atomically finalized to `completed` or `failed`. The returned error and daemon
log expose the primary TUN classification and report location as separate fields
when persistence succeeded, and user guidance uses the canonical
`podlaz doctor --tun --verbose` command.

The full TUN diagnostic path may perform only read-only snapshot collection,
kernel route/rule lookups, bounded DNS/TCP/TLS/HTTPS/DoH probes, and private
latest-report replacement. It must never change interface MTU, routes, policy
rules, DNS, `systemd-resolved`, NetworkManager, `/etc/resolv.conf`,
nftables/firewall state, services, other VPNs, or browser state. TLS certificate
validation must remain enabled. Unit tests must use local protocol fixtures
rather than live internet endpoints.

DNS wire responses must match the original message id, echoed question name,
question type, and IN class. Address evidence accepts only the requested A or
AAAA type whose owner is the queried name or is reachable through a validated,
acyclic CNAME chain beginning at that name. Unrelated owners, mismatched address
types, conflicting aliases, and disconnected CNAME chains are diagnostic
failures for UDP, TCP, and DoH paths.

Ordinary HTTPS and DoH checks use independent provider paths. The Cloudflare
HTTPS path includes its TCP/443, TLS, and small-HTTPS probes; the Google small
HTTPS probe is an independent corroborating path. Provider aggregation creates a
separate `https_partial_failure` or `doh_partial_failure` result and never
rewrites the original probe classification.

A timeout is eligible for provider degradation only when the stable
`failure_phase` proves an endpoint transport phase: `tcp_connect`,
`tls_handshake`, `http_request`, `http_response`, or `http_body`, as applicable
to that probe. A timeout in `dns_resolution`, `route_lookup`, or another local
inspection phase remains `timeout` and unhealthy even when the independent
provider succeeds. `cancelled` and `internal_diagnostic_error` are never
suppressed by provider aggregation. The phase and original per-probe
classification remain available in both JSON and `doctor --tun --verbose`
output for root-cause debugging.

Timing evidence is phase-specific. `handshake_ms` measures only the TLS
handshake after a TCP connection has been established; it does not include DNS
resolution or TCP connect time. HTTP `header_ms` ends at the first response byte,
and `body_ms` measures only the bounded response-body read. A DoH HTTP response
is marked accepted only after its status and content type satisfy the endpoint
contract; DNS payload parsing remains a separate subsequent validation step.

PMTU classification requires small HTTPS success plus two independent larger
transfers that accepted a permitted HTTP response and then stalled or failed in
the response body transport phase. DNS, route, TCP, TLS, redirect, status-code,
request, and short-body failures are not PMTU evidence. Probe deadline and
cancellation causes override layer-specific adapter errors so timeout and
cancelled remain stable machine-readable classifications without erasing the
failure phase.

IPv6 evidence includes global-unicast address filtering, `ip -6 rule show`, a
bounded AAAA selection, `ip -6 route get`, and TCP/443 connectivity. Link-local,
loopback, and non-address tokens are not reported as usable IPv6 addresses.

## Recovery

- `podlaz recover` is read-only.
- `podlaz recover --execute --yes` sends explicit cleanup intent to `podlazd`.
- The CLI must not perform privileged cleanup directly.
- Recovery may clean only clearly podlaz-owned volatile state.
- `/run/podlaz` must not be deleted wholesale.
- Stale PID metadata alone is not enough to signal a process.
- Generated configs must be recorded in transaction rollback metadata before they are written, including Xray TUN preflight configs.
- For non-interactive `connect --mode tun`, the connect request itself authorizes daemon-owned cleanup of unambiguous stale podlaz state. The daemon must recover, recollect the snapshot, and proceed only when owned state is clean. It must not stop foreign VPNs or remove ambiguous resources under the default `block` policy. `--handoff=ask` performs no automatic cleanup.
- A stale `systemd-resolved` record that cannot be removed while `podlaz0` is absent must not trigger a global resolver restart. Connect may defer only that exact persistent `dns-link` result until Xray has recreated `podlaz0`, then run `resolvectl revert podlaz0` immediately before writing podlaz DNS state. Any other skipped or failed recovery result remains a blocker.
- Missing-link cleanup is idempotent only for the validated podlaz-owned target and an exact bounded `resolvectl` process result: normal exit status `1`, empty raw stdout, and the supported `No such device` raw stderr followed by exactly one `LF` or one `CRLF`. Unterminated stderr, embedded or additional line endings, caller cancellation or deadline, process launch failure, signal termination, permission denial, another exit code, unrelated exit status `1`, unexpected stdout, or unbounded/different stderr remains a cleanup failure. The same rule applies to direct stale-link cleanup, persisted transaction DNS rollback, and the installed-package acceptance gate; trimming is permitted only for human-readable error rendering.
- Unexpected cleanup errors, foreign ownership, invalid transaction files, incomplete transaction recovery, and unrecorded existing main-table bypass state remain blockers.
- The daemon recovery scan is refreshed after every connect attempt, after
  disconnect, and after recovery execution, including failed operations. The
  stored scan remains the raw scanner result; active-session filtering never
  overwrites it.
- A successful rollback or recovery may retain terminal transaction history, but
  `rolled_back` or otherwise cleanup-complete records must be non-blocking. An
  immediate subsequent TUN connect must not be rejected by a cleanup-required
  transaction or a stale startup-scan observation.
- An unexpected TUN core exit schedules an eager read-only refresh with a
  daemon-owned five-second deadline. In addition, status and doctor perform the
  same bounded refresh synchronously before publishing a stable
  `error (core exited)` TUN state. The synchronous publication refresh inherits
  request cancellation and any earlier request deadline, publishes the exact
  refresh result including coalesced-waiter warnings, and rereads lifecycle
  status after the scan before applying active-resource filtering.
- `active_transaction_id` is published only when both lifecycle state and the
  selected status provider describe the same stable active TUN session. It is
  omitted for inactive, verifying, stopping, and error states, including custom
  `Server.Status` responses.
- Active-resource filtering requires that exact active transaction id. Exactly
  one matching committed transaction summary must exist and its durable owner,
  profile, and runtime-config metadata must agree. A missing id, duplicate match,
  load failure, or metadata mismatch leaves every candidate visible and adds an
  inspection warning.
- Only resources with matching durable podlaz ownership records are omitted from
  active status. Foreign resources and mixed generated-config directories remain
  visible.

## Redaction

Human and JSON output must redact secrets and generated runtime configuration.
This applies to `status`, `doctor`, `logs`, `plan`, `recover`, validation output,
and all JSON responses.

The generic secret redactor is not a sufficient privacy boundary for TUN
diagnostics. Before persistence and before human or JSON rendering, the complete
report passes through one fail-closed public diagnostic privacy projection.
That projection must remove the original values of profile names/identifiers,
transaction identifiers, SSIDs, physical host interface names, gateways, local
addresses, DNS servers, VPN endpoints, hostnames, TLS server names, resolved
addresses, DoH/provider URLs, certificate subjects/issuers, HTTP locations,
route tables, complete routes, policy/nftables rules, arbitrary notes, errors,
command lines, and command stdout/stderr. Typed placeholders may retain schema
shape and cardinality, but they must not encode or permit reconstruction of the
original value. The managed constant `podlaz0`, stable classifications, statuses,
phases, timings, MTUs, response/status codes, exit codes, booleans, and other
non-identifying structural evidence may remain.

The same projection applies to `/run/podlaz/diagnostics/tun-last.json`, a report
loaded from that file, `podlaz doctor --tun` human output, and `--json` output.
Regression tests must inject and independently search for each profile name/ID,
transaction ID, domain, endpoint, IPv4/IPv6 address, DNS server, SSID, host
interface, route/rule token, and command-output marker. Checking only a complete
URI, UUID, or generic secret pattern is insufficient.

The latest report remains bounded and must not store generated Xray
configuration, authentication material, or unbounded command/protocol output.
Required collection fields such as `probes`, `warnings`, and `errors` remain JSON
arrays, including when empty; they must never change type to `null`.

Self-hosted evidence follows the same rule. Raw public/local addresses, gateways, host interface names, complete routes, resolver output, provider identifiers, and generated configuration must stay outside the upload directory. Package E2E stores only normalized verdicts, bounded classifications/events, commit identity, and cryptographic hashes. The workflow scans candidate artifacts against configured secrets and current host network values and must not upload them unless both the teardown assertions and scan succeed.

JSON output must include `schema_version`. Existing JSON field meanings must not
change without an explicit compatibility note.

## Confirmation

Commands that remove user state or explicitly execute recovery cleanup must
require confirmation:

- interactive TTY: prompt unless `--yes` is passed;
- non-interactive mode: fail unless `--yes` is passed;
- JSON mode: fail unless `--yes` is passed.

A TUN connect is already an explicit privileged networking mutation request. It may therefore perform the narrowly scoped automatic podlaz-owned recovery described above without a second confirmation prompt. This exception does not authorize foreign VPN handoff, ambiguous cleanup, global `systemd-resolved` restart, or deletion of persistent user state.

High-impact flags such as `--execute` and `--yes` are long-only.

## Packaged service baseline

The packaged daemon runs as `root:podlaz` because TUN mode and recovery need
privileged networking operations. The CLI remains unprivileged and uses the
socket access boundary.

Expected systemd baseline:

- `User=root`
- `Group=podlaz`
- `RuntimeDirectory=podlaz`
- `StateDirectory=podlaz`
- `RuntimeDirectoryMode=0750`
- `StateDirectoryMode=0750`
- `UMask=0027`
- explicit systemd hardening that does not remove the networking privileges TUN
  execution requires.
