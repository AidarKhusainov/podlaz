# Evidence-Driven Network Reconciliation Design

Status: approved design for Issue #262, revised after lifecycle-race review.

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

- one active read-only revalidation run at a time;
- bounded one-element wakeup/coalescing behavior;
- trigger merging;
- publication revisions that prevent stale observations from becoming authoritative;
- cancellation/requeue when an explicit lifecycle mutation takes precedence;
- serialization with connect, disconnect, recovery, and shutdown through `lifecycleOperationLock`.

The current problem is the disposition path after fresh observation:

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
   +-- observe again after mutation/convergence
   +-- verify using sufficient independent evidence
   +-- publish verified / continue bounded reconciliation / request terminal
```

The coordinator schedules and fences work. The supervisor decides what evidence means. The lifecycle layer performs privileged mutation. A single post-run automatic-disposition handoff fences both `reconcile` and `terminal` decisions after read-only authority is released.

## Core components

### Reconciliation supervisor

Introduce a `tunReconciliationSupervisor`-style component with narrow dependencies rather than expanding `tunRevalidationRuntime` into one large policy object.

Its responsibilities are:

1. maintain one bounded reconciliation cycle for the current protected Network Session;
2. evaluate one fresh authoritative observation per coordinator round;
3. obtain structured verification evidence rather than only one aggregate error;
4. classify mandatory local proof, hard invariants, soft external evidence, repairable Podlaz drift, and network convergence;
5. track meaningful progress across rounds;
6. request a narrowly defined Podlaz-owned reconciliation action when justified;
7. require a fresh post-mutation observation before any healthy publication;
8. produce one explicit round decision.

The supervisor does not directly own Linux commands, transaction persistence, Privacy Envelope mutation, or lifecycle locking.

### Round decisions

A supervisor round returns one explicit decision:

- `verified`: sufficient fresh evidence proves the protected data plane safe and usable;
- `retry`: evidence is transient/incomplete and another bounded fresh observation is required;
- `reconcile`: fresh evidence justifies a specific Podlaz-owned lifecycle repair;
- `blocked-ownership`: safe automatic mutation/cleanup authority cannot be proven;
- `terminal`: fresh bounded evidence proves that the protected session cannot safely be restored and exact terminal cleanup authority is available;
- `superseded`: cancellation, newer publication evidence, or a lifecycle transition invalidated the round before publication/action.

`reconcile` and `terminal` are automatic mutation dispositions. Neither executes while read-only `runRevalidation` authority is held.

`blocked-ownership` ends automatic mutation for that unsafe authority state and publishes actionable fail-closed health. It must not invoke automatic cleanup that itself requires the missing proof.

## Automatic-disposition identity

Every `reconcile` or `terminal` outcome carries the identity required to prove that the decision still belongs to the lifecycle it observed:

- coordinator `publication_revision` that authorized the round;
- `expected_mutation_generation` captured for that read-only lifecycle observation;
- exact Podlaz `network_session_id`;
- active transaction identity when the disposition depends on one exact committed Data Plane Generation;
- the structured reconciliation or terminal decision payload.

Publication revision and lifecycle mutation generation protect different races and are both required:

- publication revision detects newer network evidence/hints;
- mutation generation detects connect/disconnect/recover/shutdown lifecycle transitions, including ABA where the external status later looks similar again.

Network Session identity prevents an old automatic decision from applying to a different protected session even if other scalar state happens to match.

## Generation-one contract

`tunRevalidationTriggerInitial` / generation-one post-commit proof is part of the same supervisor and evidence policy. It is not a legacy exception.

After a successful TUN commit:

1. `PrepareInitialize` publishes generation 1 as fail-closed `revalidating` before lifecycle mutation authority is released;
2. the coordinator schedules `initial` exactly as today;
3. the initial fresh observation is evaluated by the reconciliation supervisor;
4. mandatory local proof must be authoritative and sufficient before generation 1 can become `verified`;
5. an isolated soft external/provider failure does not directly produce terminal disconnect;
6. if evidence is insufficient but not hard-unsafe, generation 1 remains protected and enters the same bounded retry/reconciliation cycle;
7. terminal disposition, if eventually justified, uses the same post-run automatic-disposition admission and #261 teardown as every later generation.

Connect-time verification remains historical pre-commit proof. It never substitutes for generation-one fresh current-health evidence.

Implementation may retain `initialPending` as an internal scheduling detail, but clearing that flag must not bypass the supervisor or restore terminal-on-first-soft-failure behavior for the initial cycle.

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
- enough structured local state to distinguish present/exact, missing/drifted, unknown/incomplete, and foreign state.

Event payloads remain hints only. A route-delete event, NetworkManager transition, resume notification, or netlink burst may schedule work, but it cannot itself prove that a route is absent, an uplink changed, or repair is safe.

## Structured evidence

Replace the policy significance of one aggregate `Verify() error` with structured evidence grouped by authority and failure domain. The implementation may retain lower-level verifier functions, but the supervisor must distinguish these categories without parsing human error text.

### Mandatory authoritative local proof

Required local evidence has three conceptual states:

- `proven`: fresh authoritative observation establishes the required fact;
- `unknown`: inspection is incomplete/ambiguous or the surrounding network is still converging;
- `violated`: fresh authoritative observation positively establishes an unsafe or incorrect condition.

Mandatory local proof includes, where relevant to the active generation:

- exact Network Session and committed transaction ownership identity;
- supervised core/TUN identity;
- exact Podlaz route/rule/TUN/DNS/firewall composition;
- exact Privacy Envelope composition/effectiveness for an established protected session;
- authoritative underlying-uplink/default-route/bootstrap-path state;
- authoritative NetworkManager identity when NetworkManager participates in the uplink fingerprint;
- authoritative systemd-resolved link state required to prove the Podlaz-owned DNS path.

An `unknown` mandatory local fact is **not soft positive or negative evidence**. It cannot be outweighed by Cloudflare, Google, DNS, TLS, HTTPS, or any other external probe.

Therefore:

- mandatory local `unknown` => `converging/revalidating`, fresh observation required, no `verified` publication;
- mandatory local `violated` => repairable drift, blocked ownership, or hard-unsafe depending on the exact invariant and available authority;
- mandatory local `proven` is a prerequisite for `verified`.

A short NetworkManager transition or temporarily incomplete `systemd-resolved` inspection is treated as authoritative-local `unknown`, not as a soft external diagnostic failure.

This preserves the existing fail-closed rule for ambiguous local observation while avoiding terminal-on-first-incomplete-snapshot behavior.

### Hard local invariants

Confirmed violations may justify immediate or bounded terminal handling because continuing the protected session is unsafe, for example:

- the Privacy Envelope cannot be kept effective or a protected-traffic leak is positively confirmed;
- an unrecoverable routing loop is confirmed;
- the supervised core/TUN cannot be made consistent with the exact Network Session lifecycle;
- a safe VPN bootstrap path cannot be established without mutating unowned state after bounded convergence;
- exact Podlaz-owned state cannot be reconciled safely;
- required durable ownership for a proposed mutation is positively invalid.

A hard condition still does not create cleanup authority. Cleanup authority continues to come only from exact persisted transaction / Network Session ownership established by #259-#261.

If cleanup authority itself is incomplete or ambiguous, the result is `blocked-ownership`/`cleanup-required` as appropriate rather than deleting state by resemblance.

### Soft external/data-plane evidence

Soft evidence is diagnostic/probe evidence whose isolated failure can occur even while mandatory local state remains safe and exact, for example:

- one explicit DNS UDP timeout;
- one explicit DNS TCP timeout;
- one external TCP/443 failure;
- one TLS handshake failure;
- one HTTPS endpoint/provider failure;
- one independent external provider being unavailable.

A single soft failure can reduce confidence and may trigger another observation. It cannot independently cause terminal cleanup.

External soft evidence is evaluated only after mandatory local proof is sufficiently authoritative. Positive external evidence cannot turn mandatory local `unknown` into `verified`.

## Evidence independence and sufficient proof

Terminal classification based on external reachability must not depend on one provider or one probe family.

Where an external endpoint could otherwise create a false terminal result, the supervisor requires corroboration from at least two independent signals/providers before external failure contributes to `persistent-unusable`. Existing diagnostic target infrastructure is reused rather than creating product/provider-specific policy adapters.

Independence is evaluated by failure domain, not by merely counting commands:

- UDP and TCP DNS queries to the same resolver are useful protocol evidence but are not two independent Internet providers;
- TCP, TLS, and HTTPS stages against the same host do not become three independent providers merely because three operations failed.

Positive independent evidence prevents an external-only terminal decision when one provider is failing.

Local confirmed hard invariants do not require artificial external corroboration.

Publishing `verified` requires all of the following:

1. every mandatory local fact required for the active generation is freshly `proven`, not `unknown`;
2. exact Podlaz-owned composition required by the active generation is correct;
3. the Privacy Envelope is exact and effective for an established protected session;
4. sufficient positive protected data-plane evidence exists after grouping external probes by independent failure domain.

It does **not** require every soft external probe to be green. An isolated external failure can coexist with `verified` when all mandatory local proof is authoritative and sufficient independent positive data-plane evidence remains.

If positive proof remains insufficient across independent groups after bounded fresh rounds, the result may become `persistent-unusable`; that is different from terminally trusting one failing endpoint.

## Classification

Each fresh round produces an internal classification such as:

- `healthy`: all mandatory local proof is authoritative and sufficient current protected data-plane evidence exists;
- `converging`: mandatory local evidence is changing or temporarily `unknown`;
- `soft-degraded`: mandatory local evidence is proven but external/data-plane evidence is currently insufficient for healthy publication without proving unsafe state;
- `repairable-owned-drift`: Podlaz-owned state is provably drifted and safe reconciliation is available;
- `replacement-required`: surrounding-path or data-plane topology changed enough to require a new protected generation;
- `hard-unsafe`: a confirmed hard invariant is violated;
- `blocked-ownership`: automatic repair/cleanup authority is not sufficiently proven;
- `persistent-unusable`: bounded fresh multi-signal evidence shows no safe usable protected data plane and no convergence progress.

These are internal policy concepts, not new CLI modes. Classification determines the next action and is never inferred solely from the previous classification or event type.

## Reconciliation cycle and progress state

A reconciliation cycle starts when a protected session can no longer immediately publish sufficient current evidence. It ends only when that same session becomes `verified`, reaches deliberate terminal disposition, enters `blocked-ownership`, or is superseded by an explicit lifecycle transition to a different/absent session.

Cycle state is separate from one coordinator publication revision so boundedness survives self-retries and automatic-disposition handoffs. It is keyed to the current Network Session identity and tracks at least:

- cycle start/deadline;
- no-progress budget state;
- previous meaningful progress signature;
- current network generation / active transaction identity as evidence, not as a budget reset trigger.

A new event revision, DHCP identity, route generation, or successful replacement must not silently reset the overall cycle deadline. A new explicit user lifecycle/Network Session identity may start a new cycle because it represents new intent.

Only evidence from a still-current coordinator publication may mutate progress/budget state. A stale/superseded round cannot consume, reset, or extend the cycle budget.

### Progress signature

The progress signature excludes timestamps, retry numbers, and values that change without meaning. It contains only evidence capable of showing convergence, for example:

- underlying-uplink fingerprint / safe bootstrap-path identity;
- authoritative route availability relevant to the VPN server;
- mandatory-local proof states;
- Podlaz-owned exact composition state;
- active transaction/core/TUN state;
- Privacy Envelope state;
- resolver readiness relevant to the protected session;
- normalized success/failure groups for independent external data-plane evidence.

Progress means fresh evidence changed in a way that can plausibly move the session toward a safe result, for example:

- a replacement default route appeared;
- DHCP/address identity changed and then stabilized;
- a safe server path became available;
- a previously unknown resolver/NetworkManager observation became authoritative;
- a Podlaz-owned resource moved from drifted to exact;
- an independent probe group recovered;
- a replacement core/TUN generation became healthy.

Repeated equivalent failing evidence is no progress. A merely different/flapping signature cannot make reconciliation unbounded because the overall deadline never moves.

## Boundedness and retry scheduling

Reconciliation has two independent bounds:

1. an overall wall-clock deadline for the whole cycle;
2. a bounded no-progress budget for repeated equivalent failing rounds.

Progress may reset or reduce the no-progress count according to internal policy, but it never extends the overall deadline.

The exact constants are internal and covered by deterministic fake-clock/policy tests. They are not exposed as normal CLI tuning knobs.

A retry must not sleep while holding the lifecycle operation token. The preferred mechanism is one bounded/coalesced internal retry timer that feeds the existing coordinator. There is at most one scheduled self-retry per active cycle; real network events coalesce with or supersede it. The timer is cancelled when the cycle verifies, becomes terminal/blocked, or the Network Session identity changes.

A retry timer is scheduling only. It does not create evidence, reset budgets, or extend deadlines.

When the deadline or no-progress budget is exhausted, the supervisor makes one explicit decision from the latest fresh evidence. Exhaustion alone is not proof of terminal failure. Mandatory local `unknown` still cannot be promoted to `verified` by external success. Stable `persistent-unusable` or `hard-unsafe` evidence may authorize terminal disposition; insufficient cleanup authority yields `blocked-ownership` instead.

After an admitted terminal disposition there are no continuing automatic reconciliation attempts for that Network Session, consistent with #261.

## Reconciliation actions

The supervisor never repairs arbitrary host networking. It may request only actions scoped to the current Podlaz Network Session.

### Observe again without mutation

Use this for normal network convergence where mutation would be premature, including:

- temporary default-route disappearance;
- short NetworkManager transition / unknown active-connection identity;
- temporarily incomplete systemd-resolved observation;
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

#262 does not require inventing a second repair journal merely to make in-place repair look smaller. If the current transaction/executor model cannot provide these properties, use protected Data Plane Generation replacement instead.

### Protected Data Plane Generation replacement

This is the preferred initial privileged reconciliation primitive for #262 because #261 already makes it transaction-backed, privacy-preserving, collision-safe, rollback-aware, and restart-recoverable.

Use protected replacement when the existing generation is no longer the correct plan, including:

- uplink replacement;
- changed DHCP/default-route/bootstrap path requiring a different server bypass;
- exact Podlaz-owned routing/DNS drift that cannot be safely repaired in place with existing transaction semantics;
- core/TUN restart that cannot be repaired in place;
- topology/resource conflict requiring a fresh collision-free plan.

The durable `networkSessionState.Request` is the reconnect intent/source required to build the replacement generation. Replacement re-runs current host observation and collision-safe planning from #260; it does not edit historical routing constants into a synthetic plan.

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

A failed replacement restores/converges the previous exact protected state when possible using #261 replacement recovery. If neither old nor new protected state can be restored safely, the same reconciliation cycle continues toward bounded terminal policy without opening direct traffic.

## Unified post-run automatic-disposition handoff

`reconcile` and `terminal` use the same post-run handoff. There is no separate weaker terminal callback.

A read-only round runs under `lifecycleOperationLock.runRevalidation`. The round may decide that automatic mutation is required, but it only returns an `automaticDisposition`; it never performs that mutation re-entrantly.

Conceptually:

```text
runRevalidation owns operation token
        |
        v
capture expected mutation generation
        |
        v
fresh observe / supervisor decision
        |
        v
return reconcile or terminal disposition
        |
        v
runRevalidation releases operation token
        |
        v
coordinator clears active read-only trigger
        |
        v
ONE automatic-disposition admission point
        |
        +-- stale/newer publication ----------> superseded
        +-- lifecycle generation changed -----> superseded
        +-- different Network Session --------> superseded
        +-- shutdown/already-pending mutation -> superseded
        |
        v
admitted mutation owns operation token exactly once
        |
        v
execute unwrapped internal lifecycle primitive
```

### Linearization point

Automatic admission must have one precise linearization point that orders it against both newer network publications and explicit lifecycle mutations.

The coordinator owns the publication side of admission. While proving that `publication_revision` is still current, it invokes a narrow operation-lock automatic-admission primitive. The lock ordering must be documented and tested.

The operation-lock primitive performs the following under `mutationMu` before admission is considered successful:

1. verify `mutationsClosed == false`;
2. verify `pendingMutations == 0` so an already-declared explicit/automatic mutation wins precedence;
3. verify current `mutationGeneration == expected_mutation_generation`;
4. non-blockingly acquire the existing operation token while the admission critical section is still protected;
5. register this automatic mutation in `pendingMutations` and advance `mutationGeneration` exactly once.

If the token is not immediately available, admission fails/supersedes rather than waiting while claiming precedence it did not actually obtain.

The publication claim and lifecycle admission are treated as one coordinator-owned handoff: a newer `Notify` cannot slip between a successful publication check and successful token ownership. Once token ownership is established, the automatic disposition is admitted. Later network hints may queue fresh observation, but they do not revoke an already-admitted bounded mutation.

This gives the required ordering:

- explicit mutation declared before admission changes/pends lifecycle generation and wins;
- automatic disposition admitted first already owns the token, so a later explicit mutation may register but must execute after it;
- there is no scheduling race where both are registered and a later explicit request steals the token from an earlier admitted repair/terminal decision.

### Network Session identity recheck

After automatic admission owns the operation token and before the first privileged network mutation, reload the durable Network Session state and prove that `network_session_id` (and exact transaction identity when required) still matches the disposition.

A mismatch converts the disposition to `superseded`, releases the token/mutation registration, and performs no network mutation.

The operation token prevents a lifecycle transition from racing this identity recheck.

### Exactly-once operation authority

The admission helper already owns both mutation registration and the operation token. Therefore automatic execution must call an **unwrapped internal lifecycle primitive** that assumes its caller owns lifecycle operation authority.

It must not call `operationLockedLifecycle.Connect`, `operationLockedLifecycle.Disconnect`, `runRecoveryWithFollowUp`, or any wrapper that performs another `beginExternalMutation()`/`acquire()` pair. Doing so would create re-entrant operation locking/deadlock and double-count mutation generation.

For protected generation replacement, invoke the underlying internal Network Session lifecycle/connect primitive with the persisted request and `replace-podlaz` handoff while retaining the one admitted token.

For terminal teardown, invoke the underlying #261 terminal Network Session teardown primitive directly under the one admitted token. Do not call the externally wrapped `Disconnect` path from inside automatic admission.

Token release and mutation completion occur exactly once in a single deferred/finalized path regardless of success, cancellation, supersession after identity recheck, or error.

### Stale terminal is superseded

Terminal does not receive special weaker fencing.

A terminal decision from generation/session A is discarded if, before admission, any of these occurred:

- coordinator publication revision advanced;
- lifecycle mutation generation advanced because connect/disconnect/recover/shutdown was declared;
- Network Session identity changed;
- another mutation was already pending;
- shutdown fenced mutations.

In particular, an old terminal outcome cannot call `Disconnect` against a replacement Data Plane Generation or new Network Session merely because its old publication revision still happens to be current.

The new lifecycle/event state is re-observed normally after the winning mutation.

## Lifecycle serialization and cancellation

No automatic reconciliation or terminal mutation may race with explicit connect, disconnect, recovery, another automatic disposition, or shutdown.

Required properties:

- explicit lifecycle mutation cancels an in-flight read-only supervisor round before privileged mutation proceeds;
- cancellation caused by a higher-priority lifecycle operation is non-terminal;
- a cancelled trigger is requeued according to existing coordinator semantics when appropriate;
- automatic admission cannot jump ahead of an already-declared mutation;
- once automatic admission succeeds, later explicit mutations serialize behind its already-owned token;
- no stale pre-mutation observation can authorize post-mutation health publication;
- after every repair, health publication requires a new observation;
- daemon shutdown fences new automatic admission and cannot be kept alive by retry scheduling;
- no second lifecycle/networking mutex is introduced.

The read-only round captures `expected_mutation_generation` only after `runRevalidation` has acquired the operation token and rechecked that no mutation is pending. This ties the snapshot to the lifecycle state actually observed by the supervisor.

Do not call public HTTP/CLI endpoints from the supervisor. Reuse internal lifecycle primitives under exactly one operation-lock admission.

## Privacy contract

For an already protected Network Session, reconciliation never removes the Privacy Envelope merely to make diagnostics or repair easier.

During `converging`, `soft-degraded`, retry, repair, or protected generation replacement:

- ordinary direct egress remains blocked by the exact session-scoped Privacy Envelope;
- only exact control/bootstrap allowances defined by #261 may be widened;
- any widening is exact, persisted before mutation, and verified;
- a new bootstrap endpoint uses the existing overlap/atomic replacement contract;
- foreign firewall/routing state remains untouched.

If the Privacy Envelope itself cannot be maintained safely, that is hard evidence and the supervisor may request terminal policy. Envelope removal still occurs only through #261 ordered terminal teardown after exact Podlaz data-plane cleanup.

## Health publication

Do not add a user-facing `--adaptive` mode or a second lifecycle status system.

Keep the existing top-level TUN health vocabulary (`verified`, `revalidating`, `degraded`, `cleanup-required`) for compatibility. Change semantics so `degraded` no longer means terminal cleanup has already been chosen.

While bounded reconciliation is active, publish `revalidating` with stable classifications that distinguish at least:

- existing `uplink_revalidating` / `uplink_changed` where accurate;
- `network_converging` for mandatory local evidence that is changing/unknown during normal convergence;
- `owned_state_reconciling` for an admitted exact-owned repair/protected replacement.

`degraded` represents fresh insufficient external/data-plane evidence while mandatory local state is otherwise authoritative and the session remains protected inside its terminal boundary.

Mandatory local `unknown` remains `revalidating/converging`; it is not represented as a soft external degradation and cannot become `verified` because external providers answer successfully.

An isolated soft external failure may remain only as diagnostic evidence while health returns to `verified` if mandatory local proof is complete and sufficient independent positive data-plane evidence remains.

No unhealthy state becomes silent/permanent: the bounded supervisor converges to `verified`, deliberate admitted terminal teardown, or `cleanup-required`/blocked authority.

A classification describes evidence; it never grants mutation or cleanup authority.

Publication-revision fencing remains mandatory for health publication.

## Terminal decision

The supervisor returns `terminal` only from fresh bounded evidence and exact cleanup authority. Returning `terminal` is still only a request: actual terminal teardown starts only after the unified automatic-disposition admission proves publication revision, expected lifecycle generation, Network Session identity, mutation ordering, and shutdown state.

Terminal evidence includes conditions such as:

- confirmed privacy leak or inability to maintain the Privacy Envelope;
- confirmed unrecoverable routing loop;
- supervised core/TUN cannot be restored within bounded reconciliation;
- no safe underlying VPN-server path remains after bounded network convergence;
- exact Podlaz-owned state cannot be reconciled without mutating unowned state;
- independent external/data-plane evidence remains unusable across fresh no-progress rounds with no sufficient positive corroborating evidence;
- the overall terminal boundary is reached with stable serious evidence and no safe repair available.

One external DNS, TCP, TLS, HTTPS, or provider failure is never sufficient by itself. Temporary NetworkManager/systemd-resolved incompleteness is mandatory-local `unknown`, not an external soft terminal signal.

An admitted terminal decision reuses #261 rather than creating another cleanup path:

```text
admit current terminal disposition
        |
        v
prove same Network Session identity
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

If ownership evidence is too weak to authorize teardown, use `blocked-ownership`/`cleanup-required` instead of pretending a safe terminal cleanup occurred.

## Ownership and foreign state

Fresh observation may discover a new unrelated TUN device, policy rule, route, DNS link, nftables object, or other routing layer while Podlaz is active. Its existence is not itself a failure and never grants Podlaz mutation authority.

The supervisor asks only whether the new surrounding state prevents a safe Podlaz data plane.

If Podlaz can adapt by selecting/reconciling its own exact resources, it does so. If the only recovery would delete or rewrite unowned state, that repair is rejected. Bounded terminal handling may occur only if no safe plan remains and exact terminal cleanup authority is still proven.

## Failure reporting and diagnostics

Diagnostics remain separate from disposition authority.

The supervisor retains structured evidence sufficient to explain final classification and terminal cause while existing redaction rules remain mandatory. Terminal reports identify failed evidence groups and reconciliation outcome without logging profile secrets, private endpoints, raw generated configs, or host-private identities.

Diagnostic persistence remains best-effort and must not suppress privacy-preserving cleanup.

For terminal disposition, bounded pre-teardown diagnostics execute only after automatic terminal admission owns the operation token, so another lifecycle mutation cannot replace the session between diagnosis and teardown. Diagnostic failure itself still does not suppress cleanup.

For non-terminal soft/convergence rounds, avoid one permanent failure report per retry. Use bounded structured runtime evidence/logging so event storms do not create unbounded report files.

## Deterministic test contract

Implementation follows TDD. Tests prove policy, authority, and ordering rather than only final status strings.

### Generation-one resilience

Prove the `initial` trigger uses the same supervisor contract:

- one soft DNS timeout after commit does not terminally disconnect generation 1;
- one external provider TLS/HTTPS failure after commit does not terminally disconnect while sufficient independent evidence remains;
- mandatory local `unknown` after commit cannot publish `verified` even when external probes succeed;
- generation 1 remains protected/revalidating and retries within the same bounded cycle until sufficient proof, blocked authority, or admitted terminal decision;
- generation-one terminal outcome uses the same automatic-disposition fencing as later generations.

### Mandatory local proof versus soft evidence

Prove:

- incomplete `systemd-resolved` observation is mandatory-local `unknown`, not a soft external failure;
- NetworkManager detected but active-connection inspection unavailable is mandatory-local `unknown`;
- external Cloudflare/Google success cannot override either unknown local condition to publish `verified`;
- once local observation becomes authoritative, sufficient independent positive data-plane evidence can publish `verified` even if one optional/soft provider still fails;
- exact ownership/privacy/core evidence that is unknown never grants mutation or cleanup authority.

### Soft-failure resilience

Prove an established protected session does not terminally disconnect solely because of:

1. one DNS UDP/TCP timeout;
2. one external HTTPS/TLS provider failure while independent evidence remains healthy;
3. a short mandatory-local resolver observation gap;
4. a short mandatory-local NetworkManager transition;
5. temporary default-route disappearance followed by a replacement route;
6. reordered/bursty netlink notifications.

Each regression asserts isolated soft/converging evidence does not invoke terminal cleanup.

### Sufficient independent evidence

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
- a newer coordinator publication supersedes stale in-flight evidence and stale evidence cannot mutate cycle budget.

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

### Unified automatic-disposition fencing

Deterministically prove both `reconcile` and `terminal` use the same post-run admission contract:

- a disposition carries publication revision, expected mutation generation, and Network Session identity;
- no automatic mutation occurs while `runRevalidation` still holds the operation token;
- a newer publication before admission makes reconcile/terminal `superseded`;
- an explicit connect/recover/disconnect declared after observation but before admission changes mutation generation and makes stale reconcile/terminal `superseded`;
- specifically, stale terminal from old session A cannot disconnect replacement session B;
- stale terminal cannot disconnect a freshly recovered generation after an ABA-like lifecycle transition;
- automatic admission atomically takes the operation token before it reports success;
- an automatic disposition admitted first executes before a later explicit mutation even if the later goroutine reaches `acquire()` sooner;
- an already-declared explicit mutation wins over a not-yet-admitted automatic disposition;
- Network Session identity is rechecked under the admitted token before privileged mutation;
- token/mutation registration are released exactly once on every exit path.

### No re-entrant wrapper execution

Prove automatic execution uses unwrapped internal lifecycle primitives:

- admitted protected replacement does not call `operationLockedLifecycle.Connect` and cannot perform a second `beginExternalMutation`/`acquire`;
- admitted terminal teardown does not call wrapped `Disconnect` and cannot deadlock waiting on its own operation token;
- mutation generation advances exactly once for one automatic admission;
- later explicit mutations remain queued behind the admitted automatic token.

### Reconciliation ownership

Prove:

- privileged reconciliation mutates only exact Podlaz-owned resources;
- malformed/incomplete ownership blocks repair rather than deleting a candidate resource;
- foreign TUN/routes/rules/DNS/firewall state remains structurally unchanged;
- protected generation replacement uses fresh #260 collision-safe planning;
- rollback/replacement failure retains exact durable recovery authority;
- Privacy Envelope protection remains armed across every repair boundary;
- repair handoff does not reset overall reconciliation deadline/no-progress state.

### Hard-failure classification

Deterministically reproduce hard failures such as:

- confirmed privacy-boundary failure;
- unrecoverable routing loop or equivalent unsafe local invariant;
- no safe server path after bounded convergence;
- unrecoverable core/TUN replacement;
- exact-owned reconciliation conflict that cannot be solved without foreign mutation;
- persistent multi-signal data-plane failure with no convergence progress.

Prove terminal teardown starts only after the supervisor returns terminal **and** unified automatic admission succeeds, then follows #261 ordering.

### Cancellation and shutdown

Prove race-free precedence with explicit connect, disconnect, recovery, and daemon shutdown:

- lifecycle mutation cancels read-only supervisor work before privileged mutation proceeds;
- cancellation does not create recursive terminal disconnect;
- retained/requeued work observes state after the lifecycle mutation;
- shutdown cannot admit a new automatic disposition after the shutdown fence;
- a retry timer cannot resurrect a verified/terminal/disconnected cycle;
- publication from a pre-repair/pre-mutation observation cannot become authoritative afterward.

## Installed-package / real-host acceptance

Add sanitized packaged acceptance for #262 covering at least:

- suspend/resume while TUN is active;
- Wi-Fi/DHCP churn that changes underlying identity;
- a surrounding-routing change or unrelated TUN layer appearing while Podlaz is active;
- one controlled soft external-probe failure;
- one repairable Podlaz-owned routing/DNS drift scenario where safe fault injection is available.

Acceptance demonstrates that the client stays or returns connected automatically when a safe protected data plane remains possible, without manual `recover`, service restart, `ip`, `resolvectl`, `nft`, or NetworkManager repair commands.

The harness also includes a deterministic terminal fault proving the supervisor still reaches #261 terminal teardown after its explicit bounded decision and current-lifecycle admission.

All fixtures, logs, comments, docs, PR text, and test values use documentation/example identities only. No real user IP addresses, domains, SSIDs, profile IDs, or private endpoints are added to the repository or GitHub discussion.

## Documentation changes

When implementation lands, update current-behavior documents that still describe terminal-on-first-verification-failure behavior, primarily:

- `docs/tun-uplink-revalidation.md`;
- `docs/state-and-security.md`;
- `docs/e2e.md`;
- `docs/packaged-tun-runtime.md` where packaged lifecycle behavior changes;
- installed man/CLI references only if current-health wording materially changes.

The target architecture remains the product-level source. Implementation docs describe mandatory local proof separately from soft external evidence and document the unified automatic-disposition fence.

## Non-goals

- no product-specific adapters for foreign VPN software;
- no global network normalization;
- no NetworkManager or systemd-resolved restart as generic repair;
- no global firewall flush or foreign-state cleanup;
- no unbounded retry loop;
- no normal CLI tuning surface for retry counts/backoff/deadlines;
- no second repair journal unless a future targeted in-place repair genuinely requires one;
- no resurrection of the closed Cartesian MTU/DNS adaptation search design;
- no requirement that every soft external diagnostic signal be green before `CONNECTED`;
- no weakening of mandatory authoritative local proof before `verified`;
- no weakening of exact cleanup authority from #256/#257/#260;
- no weakening of the #261 Privacy Envelope during reconciliation;
- no separate terminal admission path weaker than reconciliation admission.

## Acceptance criteria

The design is implemented when all of the following are true:

- generation-one and later-generation health use the same supervisor/evidence policy;
- a single soft external diagnostic failure cannot cause terminal disconnect;
- mandatory authoritative local `unknown` cannot be outweighed by external success or publish `verified`;
- sufficient independent positive evidence may keep an otherwise proven-safe session `verified` despite an isolated soft external failure;
- fresh observation always supersedes stale event payloads;
- network convergence is progress-aware and bounded across retry/repair handoffs;
- actual privileged repair is restricted to exact Podlaz-owned session state;
- `reconcile` and `terminal` share one post-run automatic-disposition handoff;
- every automatic mutation is fenced by publication revision, expected mutation generation, and Network Session identity;
- automatic admission atomically owns the operation token before it is considered successful;
- an admitted automatic mutation executes through unwrapped internal lifecycle primitives and owns operation authority exactly once;
- an already-declared explicit mutation supersedes stale automatic disposition, while a later explicit mutation waits behind an already-admitted automatic disposition;
- stale terminal evidence can never disconnect a replacement/recovered Network Session;
- surrounding foreign network state is preserved;
- protected replacement reuses #260/#261 ownership/privacy contracts;
- external terminal evidence is not dependent on one provider/failure domain;
- hard unsafe conditions still fail safely;
- terminal teardown is an explicit supervisor decision, a current-lifecycle admission, and reuses #261;
- no unbounded retries or silent permanent degraded state exist;
- lifecycle cancellation/serialization remains race-free;
- deterministic tests and packaged acceptance cover Issue #262 scenarios;
- current revalidation/security/E2E documentation matches implemented behavior.
