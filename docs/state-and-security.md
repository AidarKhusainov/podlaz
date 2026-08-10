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
- Status keeps lifecycle state, runtime warnings, recovery-scan evidence, and
  inspection failures as separate concepts. A runtime warning does not become an
  inspection failure merely because it is transported through daemon status, and
  a clean active scan must not be described as an inactive lifecycle state.
- For an active TUN, durable transaction state and current network health are
  separate. `committed` is historical transaction evidence; current TUN health
  is generation-specific evidence and is published independently.

Packaged daemon access has two local socket boundaries. The filesystem socket is tried first. A transport-level permission failure may fall back to the packaged abstract socket, where the daemon can enforce peer-credential/polkit authorization without widening filesystem socket access. Once the daemon returns an HTTP, authorization, JSON, or schema error, that response is authoritative and must not be downgraded to a generic daemon-unavailable error.

## Networking safety

TUN mode may touch only podlaz-owned networking state around the Xray-owned TUN link:

- the exact daemon-owned IPv4 address `198.18.0.1/32` on the transaction-bound
  Xray-created `podlaz0` link;
- podlaz-owned routes and policy rules;
- podlaz-owned DNS link state;
- podlaz-owned nftables/firewall table, chains, and rules.

Xray owns `podlaz0` creation, lifetime, and packet ingestion through the native
`tun` inbound. `podlazd` may configure only the exact link that appeared after
the tracked Xray child started and whose name, ifindex, and TUN type still match
the durable transaction identity. It must not recreate the link or record it as
a podlaz-owned TUN device rollback target. podlazd owns only
`198.18.0.1/32` on that link. Stopping Xray is the release mechanism for the
Xray-owned link.

Apply/verify/rollback must be explicit. Normal in-process rollback must remove only what the active transaction recorded as applied. The composition executor reports every exact applied step to the transaction boundary immediately after that resource mutation and before invoking the next resource executor. The transaction boundary validates the fixed owner and target, atomically persists the matching applied step and rollback identity, and stops the apply sequence if persistence fails. Durable `desired_plan` content validates resource shape but never grants route, policy-rule, DNS, or nftables cleanup authority. `planned` contributes no network cleanup tuples. `applying` without durable applied/rollback proof is an ambiguous crash window and performs no such mutation. Later inactive recovery states use only exact durable ownership. The sole narrow syscall/persistence fallback is the daemon-owned TUN address in `applying`, using the pre-persisted name, ifindex, TUN kind, CIDR, and tracked-child appearance evidence and still requiring fail-closed host inspection. Desired-only main-table server bypass state must never be deleted by assumption. Ambiguous host state must be skipped, not guessed.

A low-level composition executor must not perform hidden cleanup after a child apply method reports that it mutated state. It returns the partial non-zero applied step together with the error and reports that step to the persistence sink before returning the error. The transaction boundary persists that ownership and controls rollback timing: direct helpers perform one immediate bounded fail-safe rollback, while the production lifecycle persists diagnostics before invoking rollback. A zero step means no owned mutation was recorded and is not added to the rollback plan. An owner or target that does not exactly match the validated plan is rejected and never grants cleanup authority.

Before active replacement, controlled handoff, transaction creation, or any
host-network mutation, an ownership-aware daemon check rejects unrelated overlap
and incomplete IPv4 address/route inventory. It may temporarily allow only exact
active podlaz state, one validated stale podlaz address routed to canonical
recovery, or state on the concrete NetworkManager device authorized by
`stop-known`. After recovery/handoff, a second authoritative check runs without
allowances and rejects any remaining host address or route that contains or
overlaps `198.18.0.1/32`, as well as foreign or ambiguous `podlaz0` address
state. The deterministic policy does not probe a
second candidate. Address apply uses explicit argv, persists partial ownership
immediately after mutation, and verifies one exact global IPv4 address on the
same UP link identity. Rollback removes only that exact address after identity
revalidation; inspection failure is not absence.

For `systemd-resolved`, apply first performs a scoped `resolvectl revert podlaz0` and then writes the planned DNS servers, `~.` route-only domain, and DNS default-route setting. An already-missing link is idempotent only when the command outcome matches the supported missing-link contract. If the kernel link exists before `systemd-resolved` registers it, only transient missing-link results from the `dns`, `domain`, and `default-route` commands are retried for a bounded interval of roughly two seconds. Verification requires the target link to have exactly the planned DNS-server set, exactly the planned route-only domain set (`~.`), `+DefaultRoute`, and no foreign active `~.` owner. Extra DNS servers or extra domains on the podlaz-owned link are ambiguous owned state and fail closed. `Current Scopes` is derived runtime state and must not be used as proof that the per-link configuration was or was not applied. The target `podlaz0` section must be unique; duplicate target sections are
ambiguous and fail closed. Static configuration is not DNS readiness. Before
commit the daemon revalidates the exact address/link, performs an uncached IPv4
query bound to the persisted numeric link index, revalidates that same identity
after the query, performs a separate normal system resolution, and checks a
bounded, deduplicated set of returned IPv4 addresses until at least one is
proven to route through the planned TUN path.

The canonical nftables verifier proves the whole podlaz-owned table composition,
not merely the presence of expected rules. It requires exact chain cardinality,
name, type, hook, numeric priority, policy, and exact ordered rule cardinality
and content. An extra rule, extra chain, foreign rule inside the owned table, or
base-chain metadata drift is ambiguous owned state and fails closed. Connect-time
verification and current-health revalidation use this same canonical verifier;
there is no weaker revalidation-specific verifier.

Recovery observation of the same `systemd-resolved` state is tri-state and
configuration-aware. Missing-link absence is accepted only through explicit,
bounded command envelopes. Existing supported nonzero missing-link status
results remain byte-exact. On Ubuntu 24.04/systemd 255, the read-only
`resolvectl status podlaz0 --no-pager` lookup may instead complete normally with
exit status `0`, empty raw stdout, and raw stderr equal to
`Failed to resolve interface "podlaz0", ignoring: No such device` followed by
exactly one `LF` or one `CRLF`; that dedicated status-only envelope is also
`absent`. It is not a generic successful-status rule: any other successful
status carrying stderr is `unknown`. A successful status outside that dedicated
missing-link envelope is authoritative only with completely empty stderr and a
strict, unique `podlaz0` target-section parse. It is `present` only when that
section proves the concrete podlaz per-link DNS shape: a DNS server, route-only
`~.`, and explicit `+DefaultRoute` without `-DefaultRoute`. A transient link
record is `absent` only when `Current Scopes` is exactly `none`,
current/server/domain DNS state is empty, and `Protocols` contains explicit
`-DefaultRoute` without `+DefaultRoute`. Missing DefaultRoute polarity or
simultaneous `+DefaultRoute -DefaultRoute` is `unknown`, as are unexpected
stderr, malformed or partial output, duplicate target sections, operational
command failure, and concrete DNS state that does not prove the podlaz shape.
`Current Scopes` is not ownership evidence for `present`; it participates only
in proving the narrow empty-transient `absent` state. This same classifier is
used for the read-only scanner and post-`revert` cleanup verification so a
successful cleanup cannot immediately republish an empty transient link record
as stale state. The read-only exit-`0` status envelope does not broaden the
mutating `resolvectl revert podlaz0` contract; mutation idempotence remains the
separate exit-`1` cleanup contract defined below.

Desired network intent validates target shape but never grants route, policy-rule, DNS, or nftables cleanup authority. Planned transactions mutate no host networking. Applying transactions without durable applied/rollback ownership are preserved as ambiguous and perform no cleanup mutation; only the bound-address syscall/persistence window has a narrow identity-checked fallback. Persisted committed state is a restart-recovery candidate, but an exact committed transaction proven to be the current live lifecycle transaction is filtered from recovery/status/doctor and cannot be mutated by `recover --execute`.

## Current TUN health and network generations

A committed TUN transaction is immutable history that apply, exact canonical
composition verification, connectivity verification, and commit succeeded for a
particular observed network state. It is not permanent evidence that the same
host networking remains valid after suspend/resume, DHCP renewal, Wi-Fi roam,
Ethernet changes, or route replacement.

For an active committed TUN session the daemon therefore publishes separate
current health:

- `verified`: the exact current generation has passed canonical composition and
  layered connectivity verification;
- `revalidating`: current evidence is being reproved;
- `degraded`: current evidence is incomplete, or a proved verification
  failure/deadline is transiently published while terminal disposition is being
  handed off; it is not a stable active end state for such terminal failures;
- `cleanup-required`: normal fail-safe lifecycle cleanup failed or ownership is
  otherwise unsafe, so durable exact ownership must remain available for
  recovery.

The daemon publishes a positive `network_generation`. Generation 1 is not
published as `verified` merely because connect committed. After commit the daemon
collects one fresh authoritative observation and runs the canonical verifier and
layered connectivity verifier against that exact observation. Only successful
verification publishes generation 1 as `verified`; failure is fail-closed. This
closes the window where the uplink can change between connect-time verification
and current health initialization.

When fresh inspection has proved the active committed session and required
verification fails or reaches its bounded deadline, old health evidence remains
invalid and the daemon returns a typed terminal outcome. The read-only verifier
finishes and releases revalidation authority before any mutation. The daemon then
runs the existing bounded redacted pre-rollback diagnostic pipeline and attempts
to persist the report with `rollback_status=pending` while the failed layer is
still observable. Only after that diagnostic boundary does it invoke the normal
bounded transaction-backed lifecycle `Disconnect`. Successful rollback and Xray
quiescence converge to inactive state and finalize the report as `completed`.
Cleanup failure finalizes the report as `failed`, preserves durable exact
ownership, and leaves current health `cleanup-required` for recovery.

This terminal disposition is not a general repair or cleanup authorization.
Observation ambiguity, incomplete ownership proof, or foreign resources remain
fail-closed and are not mutated by revalidation. Cancellation caused by an
explicit user disconnect, recovery, or daemon shutdown is also non-terminal for
this policy: the higher-priority lifecycle operation owns cleanup, so no second
automatic disconnect is scheduled.

Underlying network events are only scheduling hints. The authoritative
fingerprint is derived from a fresh snapshot and contains the underlying default
route interface and kernel ifindex, gateway, relevant global IPv4 addresses, the
active NetworkManager connection identity when NetworkManager is present, and the
server-bypass interface/gateway. Podlaz-owned `podlaz0`, policy routing,
`systemd-resolved` link state, and nftables state are excluded so the daemon's own
mutations cannot advance the generation.

NetworkManager evidence is explicitly two-stage. Detection of NetworkManager
does not imply successful active-connection inspection. If `nmcli ... general`
succeeds but `nmcli ... connection show --active` fails or is otherwise unknown,
active-connection identity is unknown and fingerprint derivation fails closed.
An empty NetworkManager identity is authoritative only when the active-connection
inventory itself was successfully inspected and contains no activated connection
for the underlying uplink. This prevents a failed inventory read from becoming
false proof that a managed connection is absent.

A material fingerprint change advances the generation and sets the in-flight
classification to `uplink_changed`. Ordinary duplicate link/address/route hints
on an already `verified` unchanged fingerprint are coalesced and discarded after
fresh observation. `PrepareForSleep(false)` and event-source resubscription are
stronger freshness hints: they reprove the same generation even when the
fingerprint is unchanged.

The logind and rtnetlink watchers use subscribe-then-resync semantics. A watcher
first establishes the D-Bus signal subscription or rtnetlink multicast bind and
then queues one coalesced `source-resync` hint. The same sequence runs after a
watcher failure and reconnect, so edges that occurred during the outage are
covered by a fresh authoritative snapshot rather than relying on a future random
event.

Lifecycle mutation has priority without dropping evidence. Connect, disconnect,
and recovery declare mutation intent before cancelling an in-flight revalidation.
A trigger already consumed while mutation is pending waits for the complete
mutation queue to become idle. If a mutation interrupts an in-flight probe, its
trigger is merged back into the bounded pending queue before cancellation. After
mutation, one fresh authoritative observation is taken and normal fingerprint/
generation logic decides whether verification is needed. Daemon shutdown is
terminal cancellation for event processing and intentionally does not requeue
work; its cancellation is not converted into a second automatic disconnect.

The verification phase of current-health revalidation is read-only. It may
reconstruct the complete durable `desired_plan` to verify state that was required
but pre-existed connect, but this never converts desired intent into rollback
authority. It does not apply, repair, flush route cache, change NetworkManager,
widen firewall policy, change DNS, recreate TUN state, or mutate foreign
resources. The only post-verification mutation described by this contract is the
normal exact transaction-backed lifecycle disconnect after a terminal proved
verification failure/deadline and after revalidation authority has been released.
Any future repair remains a separate evidence-gated design.

For native Xray TUN startup, durable rollback order is:

1. roll back podlaz-owned nftables and systemd-resolved state;
2. roll back exact policy rules and routes;
3. remove the exact daemon-owned TUN address after link-identity revalidation;
4. stop the transaction-owned Xray child process;
5. finalize generated config and transaction state, then refresh recovery/status publication.

Rollback is complete only after both host-state rollback and supervised Xray
process quiescence succeed. The transaction stop performs one bounded wait after
TERM, escalates to KILL when necessary, and performs a second bounded wait for
the supervisor completion signal after KILL. Successful signal delivery alone
is never proof of exit. If the completion signal does not arrive within either
bound, rollback returns a quiescence error and keeps `rollback_status=failed`.
Generated runtime config and transaction ownership metadata must not be removed
while process absence is unproven; a failed convergence remains cleanup-required
for recovery.

If `network-apply`, `network-verify`, connect-time connectivity verification, or
a proved post-commit revalidation fails or reaches its terminal deadline,
`podlazd` first attempts a short bounded, cancellation-aware safe diagnostic
subset while the relevant failed state still exists. The report contains a
stable classification, lifecycle `failure_phase`, and `rollback_status`. Known
network apply and verification failures use `network_apply_failure` and
`network_verify_failure`; terminal revalidation retains its failed layered phase
and current-health classification, including `revalidation_timeout` for the
bounded deadline. Overall report status such as `unhealthy` is never substituted
for the classification taxonomy.

Optional diagnostics must never delay or suppress cleanup. Normal rollback runs
with a separate daemon-owned bounded cleanup context derived without the
requesting HTTP client's cancellation, so a disconnected client or expired
request deadline cannot immediately cancel DNS, route, rule, nftables, or process
cleanup. For terminal post-commit revalidation, this cleanup begins only after the
read-only revalidation authority is released. The historical report is first
persisted with rollback status `pending` and is finalized to `completed` only
after host rollback and Xray quiescence are both proven; otherwise it is finalized
to `failed`. Explicit user/recovery/shutdown cancellation bypasses this terminal
auto-cleanup handoff because the lifecycle operation that caused cancellation
already owns cleanup. The returned error and daemon log expose the primary TUN
classification and report location as separate fields when persistence succeeds,
and user guidance uses the canonical `podlaz doctor --tun --verbose` command.

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
- Stale PID metadata alone is not enough to signal a process. The current
  transaction schema does not persist the executable identity and process start
  time required for identity-safe orphan signalling, so daemon recovery never
  sends TERM or KILL solely from a recorded PID/config reference.
- For a recorded child PID greater than one, recovery uses a tri-state check:
  absent `/proc/<pid>` means the original child is already absent and the child
  result is recovered; an existing PID without sufficient durable identity is
  skipped; an operational `/proc` inspection error is failed. Both skipped and
  failed results preserve generated config and transaction metadata. A later
  recovery run can complete after the process disappears.
- Generated configs must be recorded in transaction rollback metadata before they are written, including Xray TUN preflight configs.
- For non-interactive `connect --mode tun`, the connect request itself authorizes daemon-owned cleanup of unambiguous stale podlaz state. The daemon must recover, recollect the snapshot, and proceed only when owned state is clean. It must not stop foreign VPNs or remove ambiguous resources under the default `block` policy. `--handoff=ask` performs no automatic cleanup.
- A stale `systemd-resolved` record that cannot be removed while `podlaz0` is absent must not trigger a global resolver restart. Connect may defer only that exact persistent `dns-link` result until Xray has recreated `podlaz0`, then run `resolvectl revert podlaz0` immediately before writing podlaz DNS state. Any other skipped or failed recovery result remains a blocker.
- Missing-link cleanup is idempotent only for the validated podlaz-owned target and an exact bounded `resolvectl` process result: normal exit status `1`, empty raw stdout, and the supported `No such device` raw stderr followed by exactly one `LF` or one `CRLF`. Unterminated stderr, embedded or additional line endings, caller cancellation or deadline, process launch failure, signal termination, permission denial, another exit code, unrelated exit status `1`, unexpected stdout, or unbounded/different stderr remains a cleanup failure. The same rule applies to direct stale-link cleanup, persisted transaction DNS rollback, and the installed-package acceptance gate; trimming is permitted only for human-readable error rendering.
- A successful post-`revert` transient `systemd-resolved` record is converged
  only when it satisfies the same proven-empty contract: clean stderr,
  `Current Scopes: none`, no current/server/domain DNS state, and explicit
  `-DefaultRoute` without `+DefaultRoute`. Missing or conflicting DefaultRoute
  polarity, unexpected stderr, concrete non-podlaz DNS configuration, and
  malformed, partial, or duplicate observations remain unknown and must not be
  cleaned by assumption.
- Unexpected cleanup errors, foreign ownership, invalid transaction files, incomplete transaction recovery, and unrecorded existing main-table bypass state remain blockers.
- The daemon recovery scan is refreshed after every connect attempt, after
  disconnect, and after recovery execution, including failed operations. A
  successful newer daemon scan is authoritative and replaces older candidates;
  a timeout or failed refresh publishes incomplete/unknown evidence and never a
  stale union or top-level success. The
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

The daemon journal has a stricter child-process boundary: raw Xray stdout/stderr
bytes are untrusted sensitive input and are never persisted. Core journal events
may record only low-cardinality structural facts such as lifecycle event, stream
name, and child PID. They must not include profile IDs/names, endpoints, domains,
IP addresses, UUIDs, runtime-config paths, raw child payload, or raw child error
text. User-visible diagnostics may retain separately redacted runtime warnings;
that does not authorize copying their raw source payload into journald.

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

A proved active-session revalidation failure or deadline is also a fail-safe
lifecycle condition rather than a new user cleanup request. After bounded
redacted diagnostics and release of revalidation authority, podlazd may invoke
the normal exact transaction-backed `Disconnect` automatically to avoid leaving
an unproved active TUN behind the kill-switch. This does not waive ownership
checks, does not authorize repair/foreign cleanup, and does not apply when the
revalidation was cancelled by an explicit user lifecycle operation or daemon
shutdown that already owns cleanup.

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
