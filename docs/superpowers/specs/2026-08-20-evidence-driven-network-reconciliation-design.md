# Evidence-Driven Network Reconciliation Design

Status: approved design for Issue #262.

This document refines the reconciliation and evidence parts of `2026-08-18-resilient-network-session-design.md` on top of lifecycle continuity from #259, collision-free exact ownership from #260, and the session-scoped Privacy Envelope from #261.

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

Only the reconciliation supervisor may convert current-session health evidence into an automatic terminal disposition. The existing terminal teardown from #261 remains the only automatic terminal cleanup mechanism.

## Architectural boundary

The existing `tunRevalidationCoordinator` remains the event/coalescing and publication-fencing mechanism. #262 preserves rather than rewrites these properties:

- one active revalidation run at a time;
- bounded one-element wakeup/coalescing behavior;
- trigger merging;
- publication revisions that prevent stale runs from becoming authoritative;
- cancellation/requeue when an explicit lifecycle mutation takes precedence;
- serialization with connect, disconnect, recovery, and shutdown through `lifecycleOperationLock`.

The current problem is the disposition path after a fresh observation:

```text
observe -> verify -> error -> terminal outcome
```

#262 changes that policy layer:

```text
coordinator
   |
   v
reconciliation supervisor
   |
   +-- observe fresh authoritative state
   +-- classify structured evidence
   +-- optionally request exact Podlaz-owned reconciliation
   +-- observe again
   +-- verify using independent evidence
   +-- publish verified / continue bounded reconciliation / terminal
```

The coordinator schedules and fences work. The supervisor decides what evidence means. The lifecycle layer performs privileged mutation. These responsibilities remain separate.

## Core components

### Reconciliation supervisor

Introduce a `tunReconciliationSupervisor`-style component with narrow dependencies rather than expanding `tunRevalidationRuntime` into a large policy object.

Its responsibilities are:

1. maintain one bounded reconciliation cycle for the current protected Network Session;
2. evaluate one fresh authoritative observation per coordinator round;
3. obtain structured verification evidence rather than only one aggregate error;
4. classify hard invariants, soft evidence, repairable Podlaz drift, and network convergence;
5. track meaningful progress across rounds;
6. request a narrowly defined Podlaz-owned reconciliation action when justified;
7. require a fresh post-mutation observation before any healthy publication;
8. produce an explicit round decision.

The supervisor does not directly own Linux commands, transaction persistence, Privacy Envelope mutation, or lifecycle locking.

### Round decisions

A supervisor round returns one explicit decision:

- `verified`: sufficient fresh evidence proves the protected data plane safe and usable;
- `retry`: evidence is transient/incomplete and another bounded fresh observation is required;
- `reconcile`: fresh evidence justifies a specific Podlaz-owned lifecycle repair;
- `blocked-ownership`: safe automatic mutation/cleanup authority cannot be proven;
- `terminal`: fresh bounded evidence proves that the protected session cannot safely be restored;
- `superseded`: cancellation or newer evidence invalidated this round before publication/action.

`reconcile` is not terminal and is not executed while read-only revalidation authority is held.

`blocked-ownership` ends automatic mutation for that unsafe authority state and publishes actionable fail-closed health; it must not invoke automatic cleanup that itself requires the missing proof.

## Observation

A reconciliation observation is immutable evidence for one round. It is derived from fresh current state, not from the event payload that caused the run.

At minimum it contains or references:

- the exact current Podlaz Network Session identity;
- the exact active committed TUN transaction identity;
- the current supervised core identity;
- the current Privacy Envelope identity/composition state;
- the fresh underlying-uplink fingerprint;
- the current safe VPN-server bootstrap-path evidence;
- the current Podlaz-owned routing, policy-rule, DNS, firewall, and TUN composition required for verification;
- enough structured local state to distinguish missing/drifted Podlaz-owned resources from unknown or foreign state.

Event payloads remain hints only. A route-delete event, NetworkManager transition, resume notification, or netlink burst may schedule work, but it cannot itself prove that a route is absent, an uplink changed, or repair is safe.

## Structured evidence

Replace the policy significance of one aggregate `Verify() error` with structured evidence grouped by authority and failure domain.

The implementation may retain lower-level verifier functions, but the supervisor must distinguish these categories without parsing human error text.

### Hard local invariants

Confirmed violations may justify immediate or bounded terminal handling because continuing the protected session is unsafe:

- the Privacy Envelope cannot be kept effective or a protected-traffic leak is positively confirmed;
- an unrecoverable routing loop is confirmed;
- the supervised core/TUN cannot be made consistent with the exact Network Session lifecycle;
- a safe VPN bootstrap path cannot be established without mutating unowned state after bounded convergence;
- exact Podlaz-owned state cannot be reconciled safely;
- durable ownership needed for a proposed mutation cannot be proven.

A hard condition still does not create cleanup authority. Cleanup authority continues to come only from exact persisted transaction / Network Session ownership established by #259-#261.

If exact ownership required for safe automatic mutation is missing or ambiguous, the result is `blocked-ownership`/`cleanup-required` as appropriate rather than deleting state by resemblance.

### Authoritative local health evidence

These signals describe the actual local protected data plane and surrounding path. They may require reconciliation but are not automatically terminal:

- current uplink/default-route/bootstrap-path state;
- current NetworkManager identity when authoritative inspection succeeds;
- exact Podlaz route/rule/TUN/DNS/firewall composition;
- systemd-resolved link state;
- supervised core process state;
- Privacy Envelope exact composition.

Short-lived incompleteness or a changing surrounding network is treated as convergence first unless a hard privacy/ownership invariant is already violated.

### Soft diagnostic evidence

Examples include:

- one DNS UDP timeout;
- one DNS TCP timeout;
- one external TCP/443 failure;
- one TLS handshake failure;
- one HTTPS endpoint failure;
- temporarily incomplete resolver inspection;
- a short NetworkManager transitional state.

A single soft failure can reduce confidence and trigger re-observation. It cannot independently cause terminal cleanup.

## Evidence independence and sufficient proof

Terminal classification based on external reachability must not depend on one provider or one probe family.

Where an external endpoint could otherwise create a false terminal result, the supervisor requires corroboration from at least two independent signals/providers before external failure contributes to `persistent-unusable`. Existing diagnostic target infrastructure is reused rather than creating product/provider-specific policy adapters.

Independence is evaluated by failure domain, not by merely counting commands. For example:

- UDP and TCP DNS queries to the same resolver are useful protocol evidence but are not two independent Internet providers;
- TCP, TLS, and HTTPS stages against the same host do not become three independent providers merely because three operations failed.

Positive independent evidence prevents an external-only terminal decision when one provider is failing.

Local confirmed hard invariants do not require artificial external corroboration.

Publishing `verified` does not require every soft probe to be green. It requires:

1. all mandatory hard local invariants that can be authoritatively observed to be safe;
2. exact Podlaz-owned composition required by the active generation to be correct;
3. the Privacy Envelope to be exact and effective for an established protected session;
4. sufficient positive protected data-plane evidence after grouping soft probes by independent failure domain.

Therefore an isolated soft failure can coexist with `verified` when independent positive evidence still sufficiently proves the protected data plane. The soft failure may remain diagnostic evidence, but it is not a health-failure classification that forces the session to stay degraded indefinitely.

If positive proof remains insufficient across independent evidence groups after bounded fresh rounds, the result may become `persistent-unusable`; that is different from terminally trusting one failing endpoint.

## Classification

Each fresh round produces an internal classification such as:

- `healthy`: sufficient current protected data-plane evidence exists;
- `converging`: authoritative local evidence is changing or temporarily incomplete;
- `soft-degraded`: soft evidence is insufficient for healthy publication but does not prove unsafe state;
- `repairable-owned-drift`: Podlaz-owned state is provably drifted and safe reconciliation is available;
- `replacement-required`: surrounding-path or data-plane topology changed enough to require a new protected generation;
- `hard-unsafe`: a confirmed hard invariant is violated;
- `blocked-ownership`: automatic repair/cleanup authority is not sufficiently proven;
- `persistent-unusable`: bounded fresh multi-signal evidence shows no safe usable protected data plane and no convergence progress.

These are internal policy concepts, not new CLI modes.

Classification determines the next action. It is never inferred solely from the previous classification or from an event type.

## Reconciliation cycle and progress state

A reconciliation cycle starts when a protected session can no longer immediately publish sufficient current evidence. It ends only when that same session becomes `verified`, reaches deliberate terminal disposition, enters `blocked-ownership`, or is superseded by an explicit lifecycle transition to a different/absent session.

Cycle state is kept separately from one coordinator publication revision so boundedness survives self-retries and repair handoffs. It is keyed to the current Network Session identity and tracks at least:

- cycle start/deadline;
- no-progress budget state;
- previous meaningful progress signature;
- current network generation / active transaction identity as evidence, not as a budget reset trigger.

A new event revision, DHCP identity, route generation, or successful replacement must not silently reset the overall cycle deadline. A new explicit user lifecycle/session identity may start a new cycle because it is a new intent.

Only evidence from a still-current coordinator publication may mutate progress/budget state. A stale/superseded round cannot consume, reset, or extend the cycle budget.

### Progress signature

The progress signature excludes timestamps, retry numbers, and values that change without meaning.

It contains only evidence capable of showing convergence, for example:

- underlying-uplink fingerprint / safe bootstrap-path identity;
- authoritative route availability relevant to the VPN server;
- Podlaz-owned exact composition state;
- active transaction/core/TUN state;
- Privacy Envelope state;
- resolver readiness relevant to the protected session;
- normalized success/failure groups for independent data-plane evidence.

Progress means fresh evidence changed in a way that can plausibly move the session toward a safe result, for example:

- a replacement default route appeared;
- DHCP/address identity changed and then stabilized;
- a safe server path became available;
- a previously incomplete resolver observation became authoritative;
- a Podlaz-owned resource moved from drifted to exact;
- an independent probe group recovered;
- a replacement core/TUN generation became healthy.

Repeated equivalent failing evidence is no progress.

A merely different/flapping signature cannot make reconciliation unbounded because the overall deadline never moves.

## Boundedness and retry scheduling

Reconciliation has two independent bounds:

1. an overall wall-clock deadline for the whole cycle;
2. a bounded no-progress budget for repeated equivalent failing rounds.

Progress may reset or reduce the no-progress count according to the internal policy, but it never extends the overall deadline.

The exact constants are internal and covered by deterministic fake-clock/policy tests. They are not exposed as normal CLI tuning knobs.

A retry should not require sleeping while holding the lifecycle operation token. The preferred mechanism is one bounded/coalesced internal retry timer that feeds the existing coordinator. There is at most one scheduled self-retry per active reconciliation cycle; real network events coalesce with or supersede it. The timer is cancelled when the cycle verifies, becomes terminal/blocked, or the session lifecycle changes.

A retry timer is scheduling only. It does not create new evidence, reset budgets, or extend deadlines.

When the overall deadline or no-progress budget is exhausted, the supervisor makes one explicit decision from the latest fresh evidence. Exhaustion alone is not proof of failure if the latest state is healthy. Stable `persistent-unusable` or `hard-unsafe` evidence may authorize terminal disposition.

After terminal disposition there are no continuing automatic reconciliation attempts for that Network Session, consistent with #261.

## Reconciliation actions

The supervisor never repairs arbitrary host networking. It may request only actions scoped to the current Podlaz Network Session.

### Observe again without mutation

Use this for normal network convergence where mutation would be premature, including:

- temporary default-route disappearance;
- short NetworkManager transitions;
- temporarily incomplete systemd-resolved evidence;
- reordered/bursty netlink events;
- one soft external-probe failure without sufficient contradictory evidence.

The Privacy Envelope remains armed for an established protected session.

### Exact-owned targeted reconciliation

A targeted in-place repair is allowed only when all of the following are true:

- the resource is proven to belong to the current Podlaz Network Session;
- the desired identity/composition remains valid for the current network;
- the repair does not delete or normalize foreign state;
- persistence/rollback semantics remain exact and crash-recoverable;
- the Privacy Envelope continues to protect direct egress;
- the repaired composition is freshly re-observed and exactly verified afterward.

#262 does not require inventing a second repair journal merely to make in-place repair look smaller. If the current transaction/executor model cannot provide these properties for a targeted repair, use protected Data Plane Generation replacement instead.

### Protected Data Plane Generation replacement

This is the preferred initial privileged reconciliation primitive for #262 because #261 already makes it transaction-backed, privacy-preserving, collision-safe, rollback-aware, and restart-recoverable.

Use protected replacement when the existing generation is no longer the correct plan, including:

- uplink replacement;
- changed DHCP/default-route/bootstrap path requiring a different server bypass;
- exact Podlaz-owned routing/DNS drift that cannot be safely repaired in place with existing transaction semantics;
- core/TUN restart that cannot be repaired in place;
- topology/resource conflict requiring a fresh collision-free plan.

The durable `networkSessionState.Request` is the reconnect intent/source required to build the replacement generation. The replacement re-runs current host observation and collision-safe planning from #260; it does not edit historical routing constants into a synthetic plan.

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

A failed replacement restores/converges the previous exact protected state when possible using the #261 replacement-recovery contract. If neither old nor new protected state can be restored safely, the same reconciliation cycle continues toward the bounded terminal policy without opening direct traffic.

## Read-only-to-mutation handoff

A repair must never execute re-entrantly while `lifecycleOperationLock.runRevalidation` still owns the operation token.

The required handoff is:

```text
runRevalidation owns token
        |
        v
fresh observe/classify
        |
        v
return reconcile request
        |
        v
runRevalidation releases token
        |
        v
coordinator clears read-only active trigger
        |
        v
claim current publication + lifecycle mutation generation
        |
        v
run bounded reconciliation mutation
        |
        v
schedule fresh observation
```

The coordinator may be extended with a narrow post-run reconciliation disposition callback analogous to its existing terminal callback. This is not a second coordinator/state machine: it is the safe boundary that executes a supervisor decision only after read-only authority has been released.

Before starting a repair, the post-run handler must prove that:

- the coordinator publication revision that authorized the request is still current;
- no explicit lifecycle mutation was already declared after the read-only observation;
- daemon shutdown has not fenced new mutations.

The existing `mutationGeneration`/pending-mutation state in `lifecycleOperationLock` should be used for this ordering rather than adding a second networking mutex.

A dedicated internal reconciliation-mutation admission helper may atomically accept the repair only if the expected mutation generation is still current and no higher-priority mutation is already pending. The helper then registers the repair as a normal bounded mutation and acquires the existing operation token. It deliberately does not self-cancel the supervisor decision that requested it because read-only revalidation has already ended.

If an explicit connect/disconnect/recover/shutdown mutation wins the race, the repair request is `superseded` and performs no mutation. Fresh post-lifecycle observation decides what remains to do.

Once a reconciliation mutation is admitted first, later lifecycle mutations serialize behind it like other already-admitted bounded mutations. Shutdown cancellation bounds it through the normal daemon/event context and then drains it using the existing shutdown contract.

## Lifecycle serialization

No repair mutation may race with explicit connect, disconnect, recovery, another repair, or shutdown.

Required properties:

- explicit lifecycle mutation cancels an in-flight read-only supervisor round before acquiring mutation authority;
- cancellation caused by a higher-priority lifecycle operation is non-terminal;
- a cancelled trigger is requeued according to existing coordinator semantics when appropriate;
- repair admission cannot jump ahead of an already-declared explicit mutation;
- no stale pre-mutation observation can authorize post-mutation health publication;
- after every repair, health publication requires a new observation;
- daemon shutdown fences new repair admission and cannot be kept alive by retry scheduling;
- no second lifecycle/networking mutex is introduced.

Do not call public HTTP/CLI endpoints from the supervisor. Reuse internal lifecycle primitives. Protected replacement may invoke the internal Network Session lifecycle using the persisted request with `replace-podlaz` intent while remaining under the same operation-lock authority.

## Privacy contract

For an already protected Network Session, reconciliation never removes the Privacy Envelope merely to make diagnostics or repair easier.

During `converging`, `soft-degraded`, retry, repair, or protected generation replacement:

- ordinary direct egress remains blocked by the exact session-scoped Privacy Envelope;
- only exact control/bootstrap allowances defined by #261 may be widened;
- any widening is exact, persisted before mutation, and verified;
- a new bootstrap endpoint uses the existing overlap/atomic replacement contract;
- foreign firewall/routing state remains untouched.

If the Privacy Envelope itself cannot be maintained safely, that is hard evidence and reconciliation moves to explicit terminal policy. Envelope removal still occurs only through #261 ordered terminal teardown after Podlaz data-plane cleanup.

## Health publication

Do not add a user-facing `--adaptive` mode or a second lifecycle status system.

Keep the existing top-level TUN health vocabulary (`verified`, `revalidating`, `degraded`, `cleanup-required`) for compatibility. Change semantics so `degraded` no longer means terminal cleanup has already been chosen.

While bounded reconciliation is active, publish `revalidating` with stable classifications that distinguish at least:

- existing `uplink_revalidating` / `uplink_changed` where accurate;
- `network_converging` for fresh surrounding-network convergence;
- `owned_state_reconciling` for an accepted exact-owned repair/protected replacement.

`degraded` represents fresh insufficient soft/diagnostic evidence while the session remains protected and the supervisor is still inside its terminal boundary. It must not become a silent permanent state: bounded reconciliation converges to `verified`, deliberate terminal teardown, or `cleanup-required` when safe authority/action is blocked.

An isolated soft failure may be retained only as diagnostic evidence while health returns to `verified` if sufficient independent positive evidence remains.

A classification describes evidence; it never grants mutation or cleanup authority.

Publication-revision fencing remains mandatory. A result from an older observation cannot overwrite health after a newer hint advances the coordinator revision.

## Terminal decision

The supervisor returns `terminal` only from fresh evidence owned by the current publication revision and only when exact lifecycle cleanup authority remains sufficient for the #261 teardown.

Terminal disposition is valid for conditions such as:

- confirmed privacy leak or inability to maintain the Privacy Envelope;
- confirmed unrecoverable routing loop;
- supervised core/TUN cannot be restored within bounded reconciliation;
- no safe underlying VPN-server path remains after bounded network convergence;
- exact Podlaz-owned state cannot be reconciled without mutating unowned state;
- independent data-plane evidence remains unusable across fresh no-progress rounds with no sufficient positive corroborating evidence;
- the overall terminal boundary is reached with stable serious evidence and no safe repair available.

One DNS, TCP, TLS, HTTPS, resolver, NetworkManager, or external-provider failure is never sufficient by itself.

Terminal handling reuses #261 rather than creating another cleanup path:

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

If ownership evidence is too weak to authorize that teardown, use `blocked-ownership`/`cleanup-required` instead of pretending a safe terminal cleanup occurred.

## Ownership and foreign state

Fresh observation may discover a new unrelated TUN device, policy rule, route, DNS link, nftables object, or other routing layer while Podlaz is active. Its existence is not itself a failure and never grants Podlaz mutation authority.

The supervisor asks only whether the new surrounding state prevents a safe Podlaz data plane.

If Podlaz can adapt by selecting/reconciling its own exact resources, it does so. If the only recovery would delete or rewrite unowned state, that candidate repair is rejected. Bounded terminal handling may occur only if no safe plan remains and exact terminal cleanup authority is still proven.

## Failure reporting and diagnostics

Diagnostics remain separate from disposition authority.

The supervisor retains structured evidence sufficient to explain the final classification and terminal cause while existing redaction rules remain mandatory. Terminal reports identify failed evidence groups and reconciliation outcome without logging profile secrets, private endpoints, or raw generated configs.

Diagnostic persistence remains best-effort and must not suppress privacy-preserving cleanup.

For non-terminal soft/convergence rounds, avoid one permanent failure report per retry. Use bounded structured runtime evidence/logging so event storms do not create unbounded report files.

## Deterministic test contract

Implementation follows TDD. Tests prove policy and ordering, not only final status strings.

### Soft-failure resilience

Prove an established protected session does not terminally disconnect solely because of:

1. one DNS UDP/TCP timeout;
2. one external HTTPS/TLS provider failure while independent evidence remains healthy;
3. temporarily incomplete systemd-resolved observation;
4. a short NetworkManager transition;
5. temporary default-route disappearance followed by a replacement route;
6. reordered/bursty netlink notifications.

Each regression asserts terminal cleanup is not invoked by the isolated soft condition.

### Sufficient evidence

Prove:

- one provider can fail while sufficient independent positive/local protected evidence still publishes `verified`;
- multiple stages against one provider are not miscounted as multiple providers;
- insufficient evidence across independent groups stays bounded and cannot become permanent degraded state;
- independent failures can corroborate `persistent-unusable` after fresh bounded rounds;
- fresh positive evidence supersedes older soft failure;
- soft failure changes confidence without creating cleanup authority.

### Progress awareness

Use deterministic scripted observations/fake clock to prove:

- changing DHCP/address evidence counts as convergence rather than repeated identical failure;
- route disappearance then replacement changes progress state but not the overall deadline;
- repeated identical failing evidence consumes the no-progress budget;
- meaningless timestamp/retry changes do not count as progress;
- flapping evidence cannot run forever because the overall deadline is fixed;
- a newer coordinator publication supersedes stale in-flight evidence and stale evidence cannot mutate the cycle budget.

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

### Reconciliation ownership

Prove:

- no mutation occurs while `runRevalidation` still holds the operation token;
- a reconcile request is executed only after revision and lifecycle-generation claims succeed;
- an explicit mutation declared first supersedes the repair without stale mutation;
- an admitted repair serializes with later explicit mutation;
- privileged reconciliation mutates only exact Podlaz-owned resources;
- malformed/incomplete ownership blocks repair rather than deleting a candidate resource;
- foreign TUN/routes/rules/DNS/firewall state remains structurally unchanged;
- protected generation replacement uses fresh #260 collision-safe planning;
- rollback/replacement failure retains exact durable recovery authority;
- Privacy Envelope protection remains armed across every repair boundary;
- repair handoff does not reset the overall reconciliation deadline/no-progress state.

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

- lifecycle mutation cancels read-only supervisor work before privileged mutation proceeds;
- cancellation does not create recursive terminal disconnect;
- retained/requeued work observes state after the lifecycle mutation;
- supervisor repair cannot mutate concurrently with explicit lifecycle mutation;
- shutdown cannot admit a new reconciliation mutation after the shutdown fence;
- a retry timer cannot resurrect a verified/terminal/disconnected cycle;
- publication from a pre-repair/pre-mutation observation cannot become authoritative afterward.

## Installed-package / real-host acceptance

Add sanitized packaged acceptance for #262 covering at least:

- suspend/resume while TUN is active;
- Wi-Fi/DHCP churn that changes the underlying identity;
- a surrounding-routing change or unrelated TUN layer appearing while Podlaz is active;
- one controlled soft external-probe failure;
- one repairable Podlaz-owned routing/DNS drift scenario where safe fault injection is available.

Acceptance demonstrates that the client stays or returns connected automatically when a safe protected data plane remains possible, without manual `recover`, service restart, `ip`, `resolvectl`, `nft`, or NetworkManager repair commands.

The harness also includes a deterministic terminal fault proving the supervisor still reaches #261 terminal teardown after its explicit bounded decision.

All fixtures, logs, comments, docs, PR text, and test values use documentation/example identities only. No real user IP addresses, domains, SSIDs, profile IDs, or private endpoints are added to the repository or GitHub discussion.

## Documentation changes

When implementation lands, update current-behavior documents that still describe terminal-on-first-verification-failure behavior, primarily:

- `docs/tun-uplink-revalidation.md`;
- `docs/state-and-security.md`;
- `docs/e2e.md`;
- `docs/packaged-tun-runtime.md` where packaged lifecycle behavior changes;
- installed man/CLI references only if current-health wording materially changes.

The target architecture remains the product-level source. Implementation docs describe the actual bounded evidence/reconciliation contract rather than claiming every verifier stage must be green.

## Non-goals

- no product-specific adapters for foreign VPN software;
- no global network normalization;
- no NetworkManager or systemd-resolved restart as generic repair;
- no global firewall flush or foreign-state cleanup;
- no unbounded retry loop;
- no normal CLI tuning surface for retry counts/backoff/deadlines;
- no second repair journal unless a future targeted in-place repair genuinely requires one;
- no resurrection of the closed Cartesian MTU/DNS adaptation search design;
- no requirement that every diagnostic signal be green before `CONNECTED`;
- no weakening of exact cleanup authority from #256/#257/#260;
- no weakening of the #261 Privacy Envelope during reconciliation.

## Acceptance criteria

The design is implemented when all of the following are true:

- a single soft diagnostic failure cannot cause terminal disconnect;
- sufficient independent positive evidence may keep an otherwise safe session `verified` despite an isolated soft failure;
- fresh observation always supersedes stale event payloads;
- network convergence is progress-aware and bounded across retry/repair handoffs;
- actual privileged repair is restricted to exact Podlaz-owned session state;
- no repair is executed re-entrantly under read-only operation authority;
- explicit lifecycle mutation can supersede a not-yet-admitted stale repair;
- surrounding foreign network state is preserved;
- protected replacement reuses #260/#261 ownership/privacy contracts;
- external terminal evidence is not dependent on one provider/failure domain;
- hard unsafe conditions still fail safely;
- terminal teardown is an explicit supervisor decision and reuses #261;
- no unbounded retries or silent permanent degraded state exist;
- lifecycle cancellation/serialization remains race-free;
- deterministic tests and packaged acceptance cover Issue #262 scenarios;
- current revalidation/security/E2E documentation matches implemented behavior.
