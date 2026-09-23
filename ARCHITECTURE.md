# Architecture

This is the permanent engineering-invariant reference for Podlaz. Public command syntax and user-visible output belong in `docs/cli.md`; contributor routing belongs in `AGENTS.md`. Code and executable tests remain the source of truth for implementation detail.

## Component boundaries

- `cmd/podlaz`: unprivileged CLI. Parses user intent, owns user-scoped profile/subscription/config state, and talks to the local daemon API.
- `cmd/podlazd` / `internal/daemon`: privileged local service and composition root.
- `internal/core`: Xray process/runtime integration.
- networking/recovery packages under `internal/**`: transaction-backed host-network orchestration and recovery.
- `scripts/**`, `.github/workflows/**`, and `packaging/**`: executable package/release/E2E contract.

The CLI must not be SUID and must not directly mutate TUN devices, routes, policy rules, DNS, nftables/firewall state, or resolver files. `proxy-only` must not mutate host networking. Privileged mutations belong to `podlazd` and require exact durable ownership authority.

## Local daemon API boundary

The daemon API is local, not a remote public API. Packaged clients try the filesystem socket first. A transport-level permission failure may fall back to the packaged abstract socket. The fallback is limited to transport failures: HTTP responses, authorization responses, JSON decoding failures, and response-schema failures are authoritative daemon/protocol results and must not be reclassified as daemon unavailability.

This distinction is part of automation compatibility: a stopped daemon, an authorization denial, and an invalid daemon response are different failure classes.

## State ownership

User-owned state lives under the invoking user's XDG config/state/cache directories. Daemon runtime state lives under `/run/podlaz`; daemon persistent state lives under `/var/lib/podlaz`. Runtime-generated Xray configuration is generated output, not persistent source of truth.

Important durable authorities are intentionally separate:

- transaction files authorize cleanup/recovery of replaceable TUN data-plane resources;
- the current-boot Network Session record carries reconnect intent and exact session-scoped Privacy Envelope cleanup authority;
- the Boot Autostart Manifest is persistent policy for a future boot, not a profile database or a Network Session;
- the current-boot boot-autostart-attempt record pins one logical attempt so daemon replacement can continue it;
- product terminal outcome is typed user-facing outcome evidence, not cleanup/no-retry authority;
- the latest TUN diagnostic report is bounded, private, replacement-only diagnostic evidence.

State carrying authority is strict/versioned, bounded where appropriate, atomically replaced, private (`0600` where applicable), and redaction-safe. Current-boot records are boot-ID scoped. Concurrent Network Session transitions use one serialized load-transition-validate-save boundary so stale snapshots cannot overwrite newer intent/protection state.

Read-only commands may inspect state but must not clean it up.

## Ownership and fail-closed networking

Observation is not ownership. A matching address, route, rule, table, comment, numeric identifier, generated-looking name, or historical Podlaz value does not authorize deletion. Historical routing identifiers are allocation preferences/diagnostic hints only.

Before the first mutation, Podlaz collects authoritative read-only evidence and derives one concrete collision-free session allocation. The selected identities are persisted before apply and remain fixed for that transaction. Apply, verification, status/revalidation, rollback, disconnect, and recovery use those exact identities; they do not silently reallocate after mutation begins.

Every successful mutation is reported to the transaction boundary immediately and durably before the next executor runs. Rollback removes only resources with exact persisted ownership proof and revalidates identity before deletion. Ambiguous inspection fails closed; incomplete evidence is never converted into assumed absence or assumed ownership.

Xray owns creation/lifetime of the native `podlaz0` TUN link. The daemon may configure only the exact link identity that appeared for the tracked child and owns only its exact allocated session address and surrounding Podlaz resources. Stopping the tracked Xray process is the release mechanism for the Xray-owned link.

`systemd-resolved` and nftables verification are composition checks, not presence checks. Extra/foreign state inside a Podlaz-owned resource is ambiguous and fails closed rather than being normalized away.

## Network Session and Privacy Envelope

A TUN Network Session separates the replaceable Data Plane Generation from a session-scoped Privacy Envelope. The envelope keeps ordinary egress fail-closed while a data-plane generation is replaced, rolled back, or recovered after daemon failure/restart.

The exact envelope family/table identity and composition metadata are persisted before mutation. That persisted authority is the only authority to replace/remove the envelope. Occupied look-alike candidates are skipped, never adopted.

A protected connection is publishable as connected only after the data plane verifies, envelope authority is durable, the envelope applies/verifies, protection becomes armed, and critical connectivity is proven with the barrier active. If data-plane cleanup is incomplete, the envelope remains armed and its cleanup authority remains durable.

Replacement is a generation transition within one Network Session. Endpoint changes use durable replacement authority so protection is widened before destructive handoff and narrowed only after the new generation is proven. Cleanup ordering must never create an unprotected gap.

## Startup, recovery, and lifecycle ordering

Daemon startup first reconciles current-boot Network Session/Privacy Envelope authority. Exact transaction recovery and reconnect intent are then converged through one intent-aware lifecycle. Conflicting lifecycle mutations remain blocked while recovery is incomplete.

Fresh boot autostart is considered only after continuation/recovery has conclusively converged and no competing session authority remains. A startup invocation that observed an existing Network Session must not later admit fresh autostart merely because that startup cleaned the old session.

An `in_progress` boot-autostart attempt is restart-continuable. Daemon replacement/cancellation must not consume it as terminal merely because the old daemon context was cancelled. Terminal cleanup requires conclusive convergence, not merely disappearance of one continuation record.

Shutdown ordering must make teardown the last network mutation: stop admitting work, let in-flight handlers finish/serialize, then perform final lifecycle cleanup under the same operation ownership rules.

## Status, diagnostics, and health

Status is a projection over typed evidence. Lifecycle state, runtime warnings, recovery-scan evidence, current TUN health, and inspection failures remain separate concepts.

`Disconnected` requires conclusively inactive lifecycle evidence. Incomplete/unavailable inspection projects to `Unknown`. `Connecting`/`Reconnecting` are normal progress states. A committed transaction is historical transaction evidence, not proof of current TUN health.

`doctor --tun` is daemon-backed because authoritative active-session, transaction, route, resolver, and ownership evidence belongs to the daemon. Diagnostic persistence failure must not leave or advertise a report as successfully written.

## Package/runtime contract

The Debian package provides the CLI, privileged daemon, service/policy integration, runtime directories, and Xray/runtime assets required by packaged execution. Packaging is part of the product contract: maintainer scripts and service transitions must remain restart-safe and must not invent network cleanup authority.

The exact build, package, and acceptance commands are executable in `scripts/**`, `packaging/**`, and `.github/workflows/**`; avoid duplicating those procedures in prose. `README.md` contains only the stable entry commands.

## Automated qualification and release policy

Automated evidence has explicit roles instead of one undifferentiated acceptance checklist.

- Ordinary merge-ready evidence is produced by deterministic CI/package contracts and the permanent hosted synthetic-TUN, recovery/fault, historical-package, VM lifecycle, and simulated Wi-Fi qualifications on pull requests where those workflows apply. These scenarios protect their domain invariants continuously; they are not rebuilt again merely to create a release checkbox.
- The full resource soak remains a scheduled/manual-dispatch diagnostic because its multi-hour measurement budget is intentionally much larger than normal merge/release latency.
- Trusted real-provider full-TUN remains a distinct scheduled/manual-dispatch pre-release compatibility signal. It is not interchangeable with secret-free synthetic TUN evidence and is not a publication dependency without additional stability/cost evidence.
- Required release publication depends on exact-package installed runtime checks, secret-free synthetic full-TUN qualification, and the trusted real-provider proxy check. Missing/unavailable required evidence fails publication; an infrastructure, fixture, capability, provider, or product failure remains classified as such rather than being converted into PASS.
- Capability-only/manual reproducers are informational. They do not satisfy a runtime acceptance owner and do not gate publication.

Release provenance is build-once/promote-exactly. The release job builds the amd64/arm64 artifact set once, creates `SHA256SUMS`, and exports the checksum-manifest digest. Every required qualifier downloads that same workflow artifact set and verifies both the manifest digest and every asset checksum before exercising it. The publisher performs the same verification, attests those downloaded files, and publishes those exact files. A downstream retry may reuse the immutable uploaded candidate; it must not silently substitute a rebuild.

The deepest runtime qualification is amd64 because that is the hosted environment proven for the isolated guest/VM scenarios. arm64 is still built and package-validated as a release artifact; lack of an equivalent hosted virtualization path is not classified as a Podlaz product failure. Real-provider proxy and real-provider TUN signals remain distinct from synthetic evidence.

No physical HIL runner is part of the current release contract because no retained invariant requires one. `scripts/acceptance/release-laptop.sh` is retained only as an optional developer diagnostic/reproducer; release acceptance, merge readiness, and publication do not require a developer laptop, DUT, manual Wi-Fi action, manual suspend/reboot, manual soak, or manual provider execution.

## E2E architecture

Hosted tests validate pure/unit/contract behavior without direct Podlaz-owned mutation of the outer runner. Dedicated package/E2E scenarios validate installed-package, daemon, authorization, lifecycle, recovery, networking, and data-plane behavior. Scenario names describe the invariant they protect, not the issue that originally introduced them.

Shared E2E infrastructure belongs in `scripts/e2e/lib/**`: readiness, package provenance, execution wrappers, bounded polling, evidence capture, and cleanup primitives should be reused. Scenario-specific predicates and resource cleanup remain local when their semantics differ.

Podlaz-owned destructive networking never mutates the outer hosted runner. Hosted qualification may create narrowly scoped infrastructure-owned guest plumbing on that runner when the scenario proves exact baseline restoration and cleanup. Direct destructive product networking on a host remains dedicated-only and explicitly gated to the dedicated runner.

## Security and privacy rules

- Never log/store credentials, subscription contents, private endpoint data, or raw authority-bearing state beyond its defined private storage.
- Documentation, fixtures, Issues, PRs, and tests use reserved/example addresses and domains only.
- Authorization and local-socket errors preserve their real classification instead of degrading to generic availability errors.
- No code path may broaden ownership because cleanup would otherwise be inconvenient.
- Recovery favors leaving ambiguous state intact and actionable over deleting unproven host state.

## Dependency direction and change discipline

Keep domain behavior below transport/composition boundaries. `internal/daemon/server.go` is the composition root: it wires collaborators and ordering but should delegate coherent setup/startup/HTTP/serve/shutdown phases to private helpers rather than accumulating domain logic.

Behavioral changes require tests that express the invariant. Structural cleanup must preserve public CLI/API/state schema, package/service behavior, network ownership semantics, failure ordering, and recovery semantics unless a separate issue explicitly changes them.
