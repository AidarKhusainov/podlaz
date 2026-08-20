# Evidence-Driven Network Reconciliation Design

Status: approved design for Issue #262.

This document refines the reconciliation and evidence parts of `2026-08-18-resilient-network-session-design.md` on top of the lifecycle continuity from #259, collision-free exact ownership from #260, and the session-scoped Privacy Envelope from #261.

## Goal

Replace committed-TUN terminal-on-first-verification-failure behavior with one bounded evidence-driven supervisor that owns the disposition decision for current-session health:

```text
observe
   |
   v
classify fresh evidence
   |
   +-- sufficient safe evidence ----------------------> CONNECTED
   |
   +-- transient / incomplete / changing evidence
   |        |
   |        v
   |   reconcile Podlaz-owned state if justified
   |        |
   |        v
   |      observe again
   |        |
   |        v
   |      verify again
   |        |
   |        +-- restored ------------------------------> CONNECTED
   |        |
   |        +-- still converging ----------------------> bounded next round
   |        |
   |        +-- stable unrecoverable evidence ---------> terminal decision
   |
   +-- confirmed hard unsafe invariant ----------------> terminal decision
```

A verifier error is evidence. It is not, by itself, authority to disconnect.

Only the reconciliation supervisor may convert current-session health evidence into an automatic terminal disposition. The existing terminal teardown from #261 remains the only terminal cleanup mechanism.

## Architectural boundary

The existing `tunRevalidationCoordinator` remains the event/coalescing and publication-fencing mechanism. It already provides valuable properties that #262 should preserve rather than rewrite:

- one active revalidation run at a time;
- bounded one-element wakeup/coalescing behavior;
- trigger merging;
- publication revisions that prevent stale runs from becoming authoritative;
- cancellation/requeue when an explicit lifecycle mutation takes precedence;
- serialization with connect, disconnect, recovery, and shutdown through `lifecycleOperationLock`.

The problem is the current disposition path after a fresh observation. Conceptually it is too short:

```text
observe -> verify -> error -> terminal outcome
```

#262 changes that part only:

```text
coordinator
   |
   v
reconciliation supervisor
   |
   +-- observe fresh authoritative state
   +-- classify evidence
   +-- optionally reconcile exact Podlaz-owned state
   +-- observe again
   +-- verify using independent evidence
   +-- publish verified / continue bounded reconciliation / terminal
```

The coordinator schedules work. The supervisor decides what the evidence means. The lifecycle layer performs privileged mutation. Those responsibilities must stay separate.

## Core components

### Reconciliation supervisor

Introduce a `tunReconciliationSupervisor`-style component with narrow dependencies rather than expanding `tunRevalidationRuntime` into a large policy object.

Its responsibilities are:

1. start one bounded reconciliation cycle for the current coordinator publication revision;
2. collect a fresh observation before every decision round;
3. obtain structured verification evidence rather than only one aggregate error;
4. classify hard invariants, soft evidence, repairable Podlaz drift, and network convergence;
5. track progress across rounds;
6. request a narrowly defined Podlaz-owned reconciliation action when justified;
7. re-observe after every mutation before publishing health;
8. produce exactly one disposition: verified, superseded/cancelled, or terminal.

The supervisor does not directly own Linux commands, transaction persistence, privacy-envelope mutation, or lifecycle locking.

### Observation

A reconciliation observation is immutable evidence for one round. It is derived from fresh current state, not from the event payload that caused the run.

At minimum it contains or references:

- the current exact active Podlaz Network Session / committed transaction identity;
- the current supervised core identity;
- the current Privacy Envelope identity/composition state;
- the fresh underlying-uplink fingerprint;
- the current safe VPN-server bootstrap path evidence;
- the current Podlaz-owned routing, policy-rule, DNS, firewall, and TUN composition required for verification;
- enough structured local state to distinguish missing/drifted Podlaz-owned resources from unknown or foreign state.

Event payloads remain hints only. A route-delete event, NetworkManager transition, resume notification, or netlink burst may schedule work, but it cannot itself prove that a route is absent, an uplink changed, or repair is safe.

### Structured evidence

Replace the policy significance of one aggregate `Verify() error` with structured evidence grouped by authority and independence.

The implementation may retain lower-level verifier functions, but the supervisor must be able to distinguish the following categories without parsing human error text.

#### Hard local invariants

Confirmed violations of these invariants may justify immediate or bounded terminal handling because continuing the protected session is unsafe:

- the Privacy Envelope cannot be kept effective or a protected-traffic leak is positively confirmed;
- an unrecoverable routing loop is confirmed;
- the supervised core/TUN cannot be made consistent with the exact Network Session lifecycle;
- a safe VPN bootstrap path cannot be established without mutating unowned state after bounded convergence;
- exact Podlaz-owned state cannot be reconciled safely;
- durable ownership needed for a mutation cannot be proven.

A hard condition still does not create cleanup authority. Cleanup authority continues to come only from the exact persisted transaction / Network Session ownership established by #259-#261.

If exact ownership required for safe automatic mutation is missing or ambiguous, the result remains fail-closed (`cleanup-required`/actionable recovery state as appropriate) rather than deleting state by resemblance.

#### Authoritative local health evidence

These signals describe the actual local protected data plane and surrounding path. They may require reconciliation but are not automatically terminal:

- current uplink/default-route/bootstrap-path state;
- current NetworkManager identity when authoritative inspection succeeds;
- exact Podlaz route/rule/TUN/DNS/firewall composition;
- systemd-resolved link state;
- supervised core process state;
- Privacy Envelope exact composition.

Short-lived incompleteness or a changing surrounding network is treated as convergence first unless a hard privacy/ownership invariant is already violated.

#### Soft diagnostic evidence

Examples include:

- one DNS UDP timeout;
- one DNS TCP timeout;
- one external TCP/443 failure;
- one TLS handshake failure;
- one HTTPS endpoint failure;
- temporarily incomplete resolver inspection;
- a short NetworkManager transitional state.

A single soft failure can reduce confidence and trigger re-observation. It cannot independently cause terminal cleanup.

### Evidence independence

Terminal classification based on external reachability must not depend on one provider or one probe family.

Where an external endpoint could otherwise create a false terminal result, the supervisor must require corroboration from at least two independent signals/providers before external failure is considered persistent data-plane-unusable evidence. Existing diagnostic target infrastructure should be reused rather than creating provider-specific policy adapters.

Independence is evaluated by failure domain, not by merely counting commands. For example, UDP and TCP DNS queries to the same resolver are useful protocol evidence but are not two independent Internet providers. Likewise, TCP/TLS/HTTPS stages against the same host do not become three independent providers merely because three operations failed.

Positive independent evidence must be allowed to prevent an external-only terminal decision when one provider is failing.

Local confirmed hard invariants do not require artificial external corroboration.

## Classification and disposition

Each round produces a structured classification such as:

- `healthy`: sufficient current protected data-plane evidence exists;
- `converging`: authoritative local evidence is changing or temporarily incomplete;
- `soft-degraded`: one or more soft signals failed but the protected session is not proved unsafe;
- `repairable-owned-drift`: Podlaz-owned state is provably drifted and a safe exact-owned repair is available;
- `replacement-required`: the surrounding path changed enough that a new protected Data Plane Generation is required;
- `hard-unsafe`: a confirmed hard invariant is violated;
- `blocked-ownership`: automatic repair/cleanup authority is not sufficiently proven;
- `persistent-unusable`: bounded fresh multi-signal evidence shows no safe usable protected data plane and no convergence progress.

These are internal policy concepts, not new CLI modes.

Classification determines the next action. It must not be inferred solely from the previous classification or from an event type.

## Progress model

The supervisor tracks a deterministic progress signature for each fresh round. The signature must exclude timestamps, retry numbers, and other values that change without meaning.

It should contain only evidence capable of showing meaningful convergence, for example:

- underlying-uplink fingerprint / safe bootstrap path identity;
- authoritative route availability relevant to the VPN server;
- Podlaz-owned exact composition state;
- supervised core/TUN state;
- Privacy Envelope state;
- resolver readiness relevant to the protected session;
- normalized success/failure groups for independent data-plane evidence.

Progress means that fresh evidence changed in a way that can plausibly move the session toward a safe result, for example:

- a replacement default route appeared;
- DHCP/address identity changed and then stabilized;
- a safe server path became available;
- a previously incomplete resolver observation became authoritative;
- a Podlaz-owned resource moved from drifted to exact;
- an independent probe group recovered;
- a replacement core/TUN generation became healthy.

A repeated equivalent failing signature is no progress.

A merely different signature is not sufficient to make reconciliation unbounded. The cycle has an overall deadline that is never extended by progress, so a flapping host cannot reset recovery forever.

## Boundedness

Reconciliation has two independent bounds:

1. an overall wall-clock deadline for the whole supervisor cycle;
2. a bounded no-progress budget for repeated equivalent failing rounds.

Progress may reset or reduce the no-progress count according to the policy, but it never extends the overall deadline.

Backoff and event-assisted wakeups are implementation mechanisms, not policy authority. Event bursts remain coalesced through the coordinator. A fresh relevant event may allow the next observation sooner, but it does not grant another unbounded lifetime.

The exact constants are internal and covered by deterministic fake-clock/policy tests. They are not exposed as normal CLI tuning knobs.

When the overall deadline or no-progress budget is exhausted, the supervisor must make one explicit decision from the latest fresh evidence. Exhaustion alone is not sufficient evidence of terminal failure if the latest state proves the session healthy. Conversely, stable persistent-unusable or hard-unsafe evidence may authorize terminal disposition.

After a terminal disposition, there are no continuing automatic reconciliation attempts for that Network Session. A new explicit connection intent is required, consistent with #261.

## Reconciliation actions

The supervisor never repairs arbitrary host networking. It may request only actions scoped to the current Podlaz Network Session.

There are three action levels.

### 1. Observe again without mutation

Use this for normal network convergence where mutation would be premature, including:

- temporary default-route disappearance;
- short NetworkManager transitions;
- temporarily incomplete systemd-resolved evidence;
- reordered/bursty netlink events;
- one soft external-probe failure.

The Privacy Envelope remains armed for an established protected session.

### 2. Exact-owned targeted reconciliation

A targeted repair is allowed only when all of the following are true:

- the resource is proven to belong to the current Podlaz Network Session;
- the desired identity/composition is still valid for the current network;
- the repair does not delete or normalize foreign state;
- persistence/rollback semantics remain exact and crash-recoverable;
- the Privacy Envelope continues to protect direct egress;
- the repaired composition is freshly re-observed and exactly verified afterward.

This path is appropriate only for narrowly repairable Podlaz-owned drift where the existing identity remains safe. If those conditions cannot be met with a clear transaction-backed repair contract, use protected generation replacement instead of an ad-hoc mutation.

### 3. Protected Data Plane Generation replacement

Use the #261 protected replacement lifecycle when the underlying network changed enough that the existing generation is no longer the correct plan, for example:

- uplink replacement;
- changed DHCP/default-route/bootstrap path requiring a different server bypass;
- core/TUN restart that cannot be repaired in place;
- topology/resource conflict requiring a fresh collision-free plan.

The durable `networkSessionState.Request` is the reconnect intent/source required to build the replacement generation. The replacement must re-run current host observation and collision-safe planning from #260; it must not reconstruct a new plan by editing historical routing constants.

Required privacy ordering remains:

```text
fresh candidate plan
        |
        v
widen/prepare exact Privacy Envelope if bootstrap endpoint changes
        |
        v
replace/rebuild Podlaz Data Plane Generation
        |
        v
verify exact owned composition
        |
        v
verify protected data plane with envelope active
        |
        v
narrow/commit Privacy Envelope replacement
        |
        v
fresh observation
        |
        v
CONNECTED
```

A failed replacement must restore or converge the previous exact protected state when that remains possible, using the already established #261 replacement recovery contract. If neither old nor new protected state can be restored safely, the supervisor continues to the bounded terminal policy without opening direct traffic.

## Lifecycle serialization

Reconciliation mutation must use the same lifecycle operation authority as explicit connect/disconnect/recovery. It must not introduce a second networking mutex.

The coordinator may execute read-only observation/verification under the existing revalidation serialization. When the supervisor decides that privileged repair is required, that repair becomes a lifecycle mutation with the same precedence rules as other mutations.

The required properties are:

- explicit connect/disconnect/recover/shutdown can cancel an in-flight supervisor run before acquiring mutation authority;
- cancellation caused by a higher-priority lifecycle operation is non-terminal for the cancelled supervisor;
- a cancelled trigger is requeued according to the existing coordinator semantics when appropriate;
- no repair mutation races with another lifecycle mutation;
- no stale pre-mutation observation can authorize post-mutation health publication;
- after repair, health publication requires a new observation from the current coordinator publication revision;
- daemon shutdown fences new mutations and cannot be kept alive by reconciliation retries.

Do not call public HTTP/CLI lifecycle endpoints from the supervisor. Reuse internal lifecycle primitives so the operation lock and Network Session state remain the single authority boundary.

## Privacy contract

For an already protected Network Session, reconciliation never removes the Privacy Envelope merely to make diagnostics or repair easier.

During `converging`, `soft-degraded`, targeted repair, or protected generation replacement:

- ordinary direct egress remains blocked by the exact session-scoped Privacy Envelope;
- only the exact control/bootstrap allowances defined by #261 may be widened;
- any widening is exact, persisted before mutation, and verified;
- a new bootstrap endpoint uses the existing overlap/atomic replacement contract;
- foreign firewall/routing state remains untouched.

If the Privacy Envelope itself cannot be maintained safely, that is hard evidence and reconciliation moves to the explicit terminal policy. Envelope removal still occurs only through the #261 ordered terminal teardown after Podlaz data-plane cleanup.

## Health publication

Do not add a user-facing `--adaptive` mode or a second lifecycle status system.

Keep the existing top-level TUN health vocabulary (`verified`, `revalidating`, `degraded`, `cleanup-required`) for compatibility. Change its semantics so `degraded` no longer implies that terminal cleanup has already been chosen.

While a bounded reconciliation cycle is active, publish `revalidating` with stable classifications that distinguish at least:

- existing `uplink_revalidating` / `uplink_changed` where those descriptions are accurate;
- `network_converging` for fresh surrounding-network convergence;
- `owned_state_reconciling` for an active exact-owned repair or protected generation replacement.

`degraded` represents fresh insufficient/soft evidence while the session remains protected and the supervisor is still inside its terminal boundary. It must not become a silent permanent state: the bounded supervisor must converge to `verified`, `cleanup-required` when cleanup authority/action is blocked, or deliberate terminal teardown.

A classification describes evidence; it does not itself grant mutation or cleanup authority.

Publication-revision fencing remains mandatory. A result from an older observation cannot overwrite health after a newer hint has advanced the coordinator revision.

## Terminal decision

The supervisor may return a terminal outcome only from fresh evidence owned by the current publication revision.

Terminal disposition is valid for conditions such as:

- confirmed privacy leak or inability to maintain the Privacy Envelope;
- confirmed unrecoverable routing loop;
- supervised core/TUN cannot be restored within bounded reconciliation;
- no safe underlying VPN-server path remains after bounded network convergence;
- exact Podlaz-owned state cannot be reconciled without mutating unowned state;
- independent data-plane evidence remains unusable across fresh no-progress rounds with no positive corroborating evidence;
- the overall terminal boundary is reached with stable serious evidence and no safe repair available.

One DNS, TCP, TLS, HTTPS, resolver, NetworkManager, or external-provider failure is never sufficient by itself.

Terminal handling reuses the #261 sequence rather than creating a second cleanup path:

```text
supervisor publishes terminal decision
        |
        v
persist terminal Network Session intent
        |
        v
stop reconciliation / automatic reconnect attempts
        |
        v
KEEP Privacy Envelope armed
        |
        v
cleanup exact Podlaz data-plane state
        |
        v
remove exact Privacy Envelope
        |
        v
verify remaining host network
        |
        v
DISCONNECTED
```

If terminal cleanup cannot complete safely, exact recovery authority remains and the client must not falsely publish a clean disconnected state.

## Ownership and foreign state

Fresh observation may discover a new unrelated TUN device, policy rule, route, DNS link, nftables object, or other routing layer while Podlaz is active. Its existence is not itself a failure and never grants Podlaz mutation authority.

The supervisor asks only whether the new surrounding state prevents a safe Podlaz data plane.

If Podlaz can adapt by selecting/reconciling its own exact resources, it does so. If the only way to recover would be deleting or rewriting unowned state, that candidate repair is rejected. Bounded terminal handling may then occur if no safe plan remains.

## Failure reporting and diagnostics

Keep diagnostics separate from disposition authority.

The supervisor should retain structured evidence sufficient to explain the final classification and terminal cause, while existing redaction rules remain mandatory. A terminal diagnostic report should identify failed evidence groups and reconciliation outcome without logging profile secrets, private endpoints, or raw generated configs.

Diagnostic persistence remains best-effort and must not suppress privacy-preserving cleanup.

For non-terminal soft/convergence rounds, avoid producing a permanent failure report per retry. Use bounded structured runtime evidence/logging so event storms do not create unbounded report files.

## Deterministic test contract

Implementation follows TDD. Tests must prove policy and ordering, not only final status strings.

### Soft-failure resilience

Prove that an established protected session does not terminally disconnect solely because of:

1. one DNS UDP/TCP timeout;
2. one external HTTPS/TLS provider failure while independent evidence remains healthy;
3. temporarily incomplete systemd-resolved observation;
4. a short NetworkManager transition;
5. temporary default-route disappearance followed by a replacement route;
6. reordered/bursty netlink notifications.

Each regression must assert that terminal cleanup is not invoked by the isolated soft condition.

### Progress awareness

Use deterministic scripted observations/fake clock to prove:

- changing DHCP/address evidence counts as convergence rather than repeated identical failure;
- route disappearance then replacement resets no-progress classification but not the overall deadline;
- repeated identical failing evidence consumes the no-progress budget;
- meaningless timestamp/retry changes do not count as progress;
- flapping evidence cannot run forever because the overall deadline is fixed;
- a newer coordinator publication supersedes stale in-flight evidence.

### Network-change convergence

Exercise:

- Wi-Fi reconnect with refreshed DHCP identity;
- suspend/resume;
- uplink replacement;
- a new unrelated TUN/routing layer appearing while Podlaz is active;
- Podlaz-owned DNS/routing drift that can be repaired safely;
- changed server bootstrap path requiring protected generation replacement.

Expected result when a safe data plane can be restored:

```text
verified -> revalidating/reconciling -> verified
```

The Network Session and Privacy Envelope remain authoritative throughout.

### Evidence independence

Prove that:

- one provider failure plus independent positive evidence cannot produce terminal disposition;
- multiple stages against one provider are not miscounted as multiple providers;
- independent provider/signal failures can corroborate persistent-unusable classification after fresh bounded rounds;
- fresh positive evidence supersedes an older soft failure;
- soft diagnostic failure changes confidence without creating cleanup authority.

### Reconciliation ownership

Prove that:

- targeted repair mutates only exact Podlaz-owned resources;
- malformed/incomplete ownership blocks repair rather than deleting a candidate resource;
- foreign TUN/routes/rules/DNS/firewall state remains structurally unchanged;
- protected generation replacement uses fresh #260 collision-safe planning;
- rollback/replacement failure retains exact durable recovery authority;
- privacy protection remains armed during every repair boundary.

### Hard-failure classification

Deterministically reproduce hard failures such as:

- confirmed privacy-boundary failure;
- unrecoverable routing loop or equivalent unsafe local invariant;
- no safe server path after bounded convergence;
- unrecoverable core/TUN replacement;
- exact-owned reconciliation conflict that cannot be solved without foreign mutation;
- persistent multi-signal data-plane failure with no convergence progress.

Prove terminal teardown starts only after the supervisor makes the explicit terminal decision and then follows #261 ordering.

### Cancellation and serialization

Prove race-free precedence with explicit connect, disconnect, recovery, and daemon shutdown:

- lifecycle mutation cancels supervisor work before privileged mutation proceeds;
- cancellation does not create a recursive terminal disconnect;
- a retained/requeued trigger observes state after the lifecycle mutation;
- supervisor repair cannot mutate concurrently with explicit lifecycle mutation;
- shutdown cannot be prolonged by a new reconciliation mutation after the shutdown fence;
- publication from a pre-repair/pre-mutation observation cannot become authoritative afterward.

## Installed-package / real-host acceptance

Add sanitized packaged acceptance for #262 covering at least:

- suspend/resume while TUN is active;
- Wi-Fi/DHCP churn that changes the underlying identity;
- a surrounding-routing change or unrelated TUN layer appearing while Podlaz is active;
- one controlled soft external-probe failure;
- one repairable Podlaz-owned routing/DNS drift scenario where safe fault injection is available.

Acceptance must demonstrate that the client stays or returns connected automatically when a safe protected data plane remains possible, without manual `recover`, service restart, `ip`, `resolvectl`, `nft`, or NetworkManager repair commands.

The acceptance harness must also include a deterministic terminal fault to prove the new supervisor still reaches #261 terminal teardown after its explicit bounded decision.

All fixtures, logs, comments, docs, PR text, and test values use documentation/example identities only. No real user IP addresses, domains, SSIDs, profile IDs, or private endpoints are added to the repository or GitHub discussion.

## Documentation changes

When implementation lands, update the current-behavior documents that still describe terminal-on-first-verification-failure behavior, primarily:

- `docs/tun-uplink-revalidation.md`;
- `docs/state-and-security.md`;
- `docs/e2e.md`;
- `docs/packaged-tun-runtime.md` where packaged lifecycle behavior changes;
- installed man/CLI references only if their current-health wording materially changes.

The target architecture document remains the product-level source; implementation docs must describe the actual bounded evidence/reconciliation contract rather than claiming every verifier stage must be green.

## Non-goals

- no product-specific adapters for foreign VPN software;
- no global network normalization;
- no NetworkManager or systemd-resolved restart as generic repair;
- no global firewall flush or foreign-state cleanup;
- no unbounded retry loop;
- no normal CLI tuning surface for retry counts/backoff/deadlines;
- no resurrection of the closed Cartesian MTU/DNS adaptation search design;
- no requirement that every diagnostic signal be green before `CONNECTED`;
- no weakening of exact cleanup authority from #256/#257/#260;
- no weakening of the #261 Privacy Envelope during reconciliation.

## Acceptance criteria

The design is implemented when all of the following are true:

- a single soft diagnostic failure cannot cause terminal disconnect;
- fresh observation always supersedes stale event payloads;
- network convergence is progress-aware and bounded;
- actual privileged repair is restricted to exact Podlaz-owned session state;
- surrounding foreign network state is preserved;
- protected replacement reuses the #260/#261 ownership and privacy contracts;
- external terminal evidence is not dependent on one provider;
- hard unsafe conditions still fail safely;
- terminal teardown is an explicit supervisor decision and reuses #261;
- no unbounded retries or silent permanent degraded state exist;
- lifecycle cancellation/serialization remains race-free;
- deterministic tests and packaged acceptance cover the scenarios required by Issue #262;
- current revalidation/security/E2E documentation matches the implemented behavior.
