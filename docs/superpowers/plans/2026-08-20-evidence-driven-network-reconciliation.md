# Evidence-Driven Network Reconciliation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace terminal-on-first-revalidation-failure with bounded evidence-driven reconciliation that preserves exact ownership and the #261 Privacy Envelope while recovering both active and degraded TUN generations.

**Architecture:** Keep `tunRevalidationCoordinator` as the bounded event/coalescing scheduler, but route generation-one and later fresh observations through a new reconciliation supervisor that separates mandatory authoritative local proof from soft external evidence. Automatic `reconcile` and `terminal` decisions use one linearizable post-run admission path that fences publication revision, lifecycle mutation generation, and Network Session identity before owning the existing operation token exactly once. Protected rebuild reuses #260/#261 planning/privacy mechanics and has an explicit degraded-source path for an already-dead core.

**Tech Stack:** Go 1.26 / toolchain 1.26.6, Linux rtnetlink/systemd-resolved/NetworkManager observations, existing Podlaz transaction/planner/executor/Privacy Envelope components, deterministic Go tests, shell-based self-hosted Ubuntu 24.04 package acceptance.

**Spec:** `docs/superpowers/specs/2026-08-20-evidence-driven-network-reconciliation-design.md`

## Global Constraints

- Work only in `issue-262-evidence-driven-reconciliation`, based on `master` commit `4f585997c920227b76342507deb3ac49483fca86`.
- Do not introduce a user-facing adaptive/retry mode or normal CLI tuning knobs.
- Preserve exact cleanup authority: historical names, priorities, tables, interfaces, or provider identities never grant mutation authority.
- Preserve the session-scoped Privacy Envelope during every established-session retry/reconcile/rebuild boundary; do not open ordinary direct egress to make repair easier.
- Never mutate foreign TUN, route, rule, DNS, nftables, NetworkManager, or other VPN state.
- Mandatory local `unknown` can never publish `verified` and can never be outweighed by external provider success.
- One soft external DNS/TCP/TLS/HTTPS/provider failure can never independently request terminal teardown.
- Generation-one post-commit proof uses the same supervisor/evidence policy as later generations.
- `reconcile` and `terminal` share one post-run automatic-disposition admission; stale terminal is not special.
- Automatic admission owns the existing lifecycle operation token exactly once and then executes unwrapped internal lifecycle primitives.
- Reconciliation has a fixed overall deadline plus a no-progress budget; progress never extends the overall deadline.
- Use only documentation/example identities in code/tests/docs/PR text; never copy private host/profile/domain/IP data.
- Run `govulncheck ./...` because this change touches networking, process lifecycle, privilege boundaries, and generated network state.

---

### Task 1: Define Structured Reconciliation Evidence and Decisions

**Files:**
- Create: `internal/daemon/tun_reconciliation_evidence.go`
- Create: `internal/daemon/tun_reconciliation_evidence_test.go`
- Modify: `internal/api/tun_health.go`
- Test: `internal/api/tun_health_test.go`

**Interfaces:**
- Produces: `tunLocalProofState`, `tunMandatoryEvidence`, `tunProbeEvidence`, `tunEvidenceSet`, `tunReconciliationDecision`, `tunAutomaticDisposition`.
- Produces public health classifications: `network_converging` and `owned_state_reconciling`.
- Consumed by Tasks 2-10.

- [ ] **Step 1: Write failing API-health tests for the two new revalidating classifications**

Add table cases that require both values to validate only with `TunHealthRevalidating`:

```go
{
    name: "network converging",
    health: api.TunHealthStatus{
        State: api.TunHealthRevalidating,
        NetworkGeneration: 2,
        Classification: api.TunHealthNetworkConverging,
    },
},
{
    name: "owned state reconciling",
    health: api.TunHealthStatus{
        State: api.TunHealthRevalidating,
        NetworkGeneration: 2,
        Classification: api.TunHealthOwnedStateReconciling,
    },
},
```

Run: `go test ./internal/api -run TunHealth -count=1`

Expected: FAIL because the constants/validator cases do not exist.

- [ ] **Step 2: Add the health classifications without adding a new health-state enum**

Add:

```go
TunHealthNetworkConverging      TunHealthClassification = "network_converging"
TunHealthOwnedStateReconciling  TunHealthClassification = "owned_state_reconciling"
```

Accept them only in the `TunHealthRevalidating` branch of `ValidateTunHealthStatus`. Keep `verified`, `degraded`, and `cleanup-required` semantics separate.

Run: `go test ./internal/api -run TunHealth -count=1`

Expected: PASS.

- [ ] **Step 3: Write failing evidence-model tests**

Cover these invariants with constructors/helpers that compare typed values rather than strings:

```go
func TestTunMandatoryUnknownCannotBeHealthy(t *testing.T) { /* DNS or NM local proof unknown => not healthy */ }
func TestTunSoftProviderFailureDoesNotViolateMandatoryProof(t *testing.T) { /* local proven + one provider failed */ }
func TestTunAutomaticDispositionCarriesAllLifecycleFences(t *testing.T) { /* revision + mutation generation + session ID + tx ID */ }
```

Define the expected core types in the test:

```go
type tunLocalProofState uint8

const (
    tunLocalProofUnknown tunLocalProofState = iota
    tunLocalProofProven
    tunLocalProofViolated
)

type tunReconciliationDecisionKind string

const (
    tunDecisionVerified         tunReconciliationDecisionKind = "verified"
    tunDecisionRetry            tunReconciliationDecisionKind = "retry"
    tunDecisionReconcile        tunReconciliationDecisionKind = "reconcile"
    tunDecisionBlockedOwnership tunReconciliationDecisionKind = "blocked-ownership"
    tunDecisionTerminal         tunReconciliationDecisionKind = "terminal"
    tunDecisionSuperseded       tunReconciliationDecisionKind = "superseded"
)
```

Run: `go test ./internal/daemon -run 'TunMandatory|TunSoftProvider|TunAutomaticDispositionCarries' -count=1`

Expected: FAIL because the evidence types do not exist.

- [ ] **Step 4: Implement the evidence and disposition types**

Use typed fields, not error-message parsing:

```go
type tunMandatoryEvidence struct {
    SessionOwnership tunLocalProofState
    CoreTUN          tunLocalProofState
    OwnedComposition tunLocalProofState
    PrivacyEnvelope  tunLocalProofState
    UplinkPath       tunLocalProofState
    NetworkManager   tunLocalProofState
    ResolvedDNS      tunLocalProofState
}

type tunProbeEvidence struct {
    Group    string
    Provider string
    Success  bool
    Cause    error
}

type tunEvidenceSet struct {
    Mandatory tunMandatoryEvidence
    Probes    []tunProbeEvidence
}

type tunAutomaticDisposition struct {
    Kind                       tunReconciliationDecisionKind
    PublicationRevision        uint64
    ExpectedMutationGeneration uint64
    NetworkSessionID           string
    TransactionID              string
    Generation                 uint64
    Classification             api.TunHealthClassification
    Plan                       planner.TunPlan
    Cause                      error
}
```

Add helpers that answer `mandatoryUnknown()`, `mandatoryViolated()`, and provider/failure-domain counts without flattening the evidence into one `error`.

Run: `go test ./internal/daemon -run 'TunMandatory|TunSoftProvider|TunAutomaticDispositionCarries' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit Task 1**

```bash
git add -- internal/api/tun_health.go internal/api/tun_health_test.go internal/daemon/tun_reconciliation_evidence.go internal/daemon/tun_reconciliation_evidence_test.go
git commit -m "feat: define TUN reconciliation evidence model"
```

---

### Task 2: Collect Mandatory Local Proof Separately from Soft Data-Plane Evidence

**Files:**
- Modify: `internal/daemon/tun_revalidation_backend.go`
- Modify: `internal/daemon/tun_revalidation_dataplane.go`
- Modify: `internal/daemon/tun_revalidation_dataplane_test.go`
- Create: `internal/daemon/tun_reconciliation_backend_test.go`

**Interfaces:**
- Produces: `tunReconciliationObservation` with exact session/transaction identity plus mandatory evidence.
- Produces: `collectTunRevalidationProbeEvidence(ctx, plan, client) []tunProbeEvidence`.
- Consumes Task 1 evidence types.

- [ ] **Step 1: Write failing local-proof tests for authoritative unknown**

Add deterministic cases where:

```text
systemd-resolved inspection incomplete + external probes healthy => mandatory ResolvedDNS=unknown
NetworkManager detected but active-connection inventory unavailable => mandatory NetworkManager=unknown
```

The tests must assert that the backend returns structured `unknown`, not `TunHealthConnectivityFailed`, and does not claim `verified` evidence.

Run: `go test ./internal/daemon -run 'ReconciliationBackend.*Unknown' -count=1`

Expected: FAIL with the current aggregate observation/verification contract.

- [ ] **Step 2: Split production observation from aggregate terminal error semantics**

Replace `tunRevalidationObservation` policy usage with a richer immutable observation containing:

```go
type tunReconciliationObservation struct {
    sessionID     string
    transactionID string
    fingerprint   tunUplinkFingerprint
    plan          planner.TunPlan
    mandatory     tunMandatoryEvidence
}
```

Load `networkSessionStateStore` during observation and require exact same-session identity for an established protected session. Preserve current transaction verification and fresh snapshot collection. Map incomplete resolver/NM observation to `tunLocalProofUnknown`; map positively invalid exact ownership to `tunLocalProofViolated`/blocked ownership, not to soft external evidence.

- [ ] **Step 3: Write failing probe-collection tests proving one failed stage does not short-circuit later independent evidence**

Use a fake `tunRevalidationNetworkClient` where Cloudflare TLS fails but Google HTTPS succeeds. Assert the returned evidence contains both results. Also prove TCP/TLS/HTTPS against Cloudflare share provider `cloudflare` instead of counting as three providers.

Run: `go test ./internal/daemon -run 'RevalidationProbeEvidence' -count=1`

Expected: FAIL because `verifyTunRevalidationDataPlane` currently returns on the first failure.

- [ ] **Step 4: Implement bounded evidence collection**

Keep per-probe deadlines with `runProbe`, but collect results instead of returning on first soft failure. Reuse existing catalog targets and add the existing independent Google HTTPS target as corroboration. Do not add a new provider adapter or new external endpoint.

Representative evidence groups:

```go
[]tunProbeEvidence{
    {Group: "dns-udp", Provider: "session-resolver", Success: udpOK, Cause: udpErr},
    {Group: "dns-tcp", Provider: "session-resolver", Success: tcpDNSOK, Cause: tcpDNSErr},
    {Group: "tcp", Provider: "cloudflare", Success: tcpOK, Cause: tcpErr},
    {Group: "tls", Provider: "cloudflare", Success: tlsOK, Cause: tlsErr},
    {Group: "https", Provider: "cloudflare", Success: cfHTTPSOK, Cause: cfHTTPSErr},
    {Group: "https", Provider: "google", Success: googleHTTPSOK, Cause: googleHTTPSErr},
}
```

Cancellation/deadline of the supervisor context remains terminal for the round itself and stops further probing; ordinary probe failures do not.

Run: `go test ./internal/daemon -run 'RevalidationProbeEvidence|RevalidationDataPlane' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit Task 2**

```bash
git add -- internal/daemon/tun_revalidation_backend.go internal/daemon/tun_revalidation_dataplane.go internal/daemon/tun_revalidation_dataplane_test.go internal/daemon/tun_reconciliation_backend_test.go
git commit -m "feat: collect structured TUN health evidence"
```

---

### Task 3: Implement the Bounded Evidence-Driven Supervisor, Including Generation One

**Files:**
- Create: `internal/daemon/tun_reconciliation_supervisor.go`
- Create: `internal/daemon/tun_reconciliation_supervisor_test.go`
- Modify: `internal/daemon/tun_revalidation_runtime.go`
- Modify: `internal/daemon/tun_revalidation_review_regression_test.go`

**Interfaces:**
- Produces: `tunReconciliationSupervisor.RunRound(ctx, input) tunReconciliationDecision`.
- Produces cycle state keyed by Network Session ID with fixed deadline and no-progress budget.
- Consumes Task 2 observations/evidence and Task 1 decisions.

- [ ] **Step 1: Write RED policy tests before changing runtime behavior**

Required cases:

```go
func TestTunSupervisorInitialSoftFailureRetriesInsteadOfTerminal(t *testing.T) {}
func TestTunSupervisorInitialMandatoryUnknownCannotVerify(t *testing.T) {}
func TestTunSupervisorOneProviderFailureCanStillVerify(t *testing.T) {}
func TestTunSupervisorRepeatedEquivalentFailureConsumesNoProgressBudget(t *testing.T) {}
func TestTunSupervisorChangingDHCPProgressDoesNotExtendOverallDeadline(t *testing.T) {}
func TestTunSupervisorPersistentIndependentFailuresBecomeTerminal(t *testing.T) {}
func TestTunSupervisorBlockedOwnershipNeverRequestsCleanupMutation(t *testing.T) {}
```

Use a fake clock injected through `now func() time.Time`; do not sleep in policy tests.

Run: `go test ./internal/daemon -run '^TestTunSupervisor' -count=1`

Expected: FAIL because the supervisor does not exist.

- [ ] **Step 2: Implement fixed cycle policy and progress signature**

Start with internal constants, not CLI flags:

```go
const (
    defaultTunReconciliationDeadline = 45 * time.Second
    defaultTunNoProgressRounds       = 3
)
```

The cycle stores its original deadline and never extends it. A progress signature must normalize only meaningful fields: fingerprint/bootstrap path, mandatory proof states, exact owned composition/core/privacy states, and provider/group success summary.

- [ ] **Step 3: Implement classification order explicitly**

Decision order:

```text
context/publication stale -> superseded
mandatory ownership blocked -> blocked-ownership
confirmed hard unsafe -> terminal
mandatory unknown -> retry/network_converging
repairable exact-owned drift -> reconcile/owned_state_reconciling
replacement-required -> reconcile/owned_state_reconciling
mandatory proven + sufficient positive evidence -> verified
insufficient soft evidence + budget remains -> retry/degraded
stable persistent independent failure + boundary exhausted -> terminal
```

Never let a timeout counter alone convert an otherwise healthy latest observation to terminal.

- [ ] **Step 4: Route both initial and later runtime paths through the supervisor**

Refactor `InitializePending`/`Revalidate` so they share one `runSupervisorRound` path. Keep `initialPending` only as scheduling state. Remove the direct `terminalTunRevalidationOutcome(...)` call from generation-one verification and from ordinary single verifier errors.

Generation 1 remains `revalidating` until the supervisor returns `verified`; one soft provider failure becomes retry or still-verified depending on sufficient evidence.

Run:

```bash
go test ./internal/daemon -run 'TunSupervisor|TunRevalidationInitialize|GenerationOne' -count=1
```

Expected: PASS.

- [ ] **Step 5: Add deterministic retry-scheduling seam**

The supervisor decision should expose `RetryAfter time.Duration`; actual timer wiring comes in Task 8. Tests prove retry scheduling does not alter the cycle deadline/budget itself.

- [ ] **Step 6: Commit Task 3**

```bash
git add -- internal/daemon/tun_reconciliation_supervisor.go internal/daemon/tun_reconciliation_supervisor_test.go internal/daemon/tun_revalidation_runtime.go internal/daemon/tun_revalidation_review_regression_test.go
git commit -m "feat: add bounded TUN reconciliation supervisor"
```

---

### Task 4: Add One Linearizable Automatic-Disposition Admission to the Lifecycle Operation Lock

**Files:**
- Modify: `internal/daemon/lifecycle_operation_lock.go`
- Create: `internal/daemon/lifecycle_operation_lock_automatic_test.go`
- Modify: `internal/daemon/tun_revalidation_coordinator.go`
- Modify: `internal/daemon/tun_revalidation_coordinator_test.go`

**Interfaces:**
- Produces: `lifecycleMutationSnapshot()` returning generation/pending/fenced state.
- Produces: `tryAdmitAutomaticMutation(expectedGeneration uint64) (*lifecycleAutomaticAdmission, bool)`.
- Admission owns the operation token and one mutation registration exactly once.
- Consumed by Tasks 5, 7, 8.

- [ ] **Step 1: Write RED lock-ordering tests**

Cover these exact races:

```go
func TestAutomaticAdmissionRejectsChangedMutationGeneration(t *testing.T) {}
func TestAutomaticAdmissionRejectsAlreadyPendingMutation(t *testing.T) {}
func TestAutomaticAdmissionRejectsShutdownFence(t *testing.T) {}
func TestAutomaticAdmissionOwnsTokenBeforeSuccess(t *testing.T) {}
func TestAutomaticAdmissionFirstForcesLaterExplicitMutationToWait(t *testing.T) {}
func TestAutomaticAdmissionReleaseIsIdempotent(t *testing.T) {}
```

Use channels to prove order; do not use timing-only assertions.

Run: `go test ./internal/daemon -run '^TestAutomaticAdmission' -count=1`

Expected: FAIL.

- [ ] **Step 2: Implement automatic admission under `mutationMu` with the spec linearization point**

The helper must perform, in this order while holding `mutationMu`:

```text
reject mutationsClosed
reject pendingMutations != 0
reject mutationGeneration != expected
non-blocking receive from token
open mutationIdle if needed
pendingMutations++
mutationGeneration++
return admission object
```

If the token cannot be received immediately, reject/supersede; never wait while pretending automatic precedence has been established.

The admission object owns:

```go
type lifecycleAutomaticAdmission struct {
    lock *lifecycleOperationLock
    once sync.Once
}

func (a *lifecycleAutomaticAdmission) Release() {
    a.once.Do(func() {
        // decrement pending under mutationMu, close idle if zero, then return token exactly once
    })
}
```

Do not call `interruptRevalidation` from this helper; the read-only run already ended.

- [ ] **Step 3: Expose mutation generation to the read-only round only after `runRevalidation` owns the token**

Add a narrow snapshot method used inside the `runRevalidation` callback so the supervisor captures the lifecycle generation it actually observed.

- [ ] **Step 4: Generalize coordinator post-run callback from terminal-only to automatic disposition**

Replace `terminal tunRevalidationTerminalFunc` with a callback that receives a `tunAutomaticDisposition`. The coordinator still clears `activeTrigger` before calling the post-run path. Both `reconcile` and `terminal` use this callback.

The coordinator must hold its own mutex across the final publication-revision check and the operation-lock admission callback so `Notify()` cannot advance the revision between those two halves of the handoff. The operation-lock callback must not call back into coordinator methods while that mutex is held; tests enforce the lock ordering.

- [ ] **Step 5: Add stale-terminal regressions at coordinator/lock boundary**

Prove:

```text
old terminal A + explicit replace/recover declared before admission => superseded
old reconcile A + newer Notify before admission => superseded
automatic admission first + later explicit connect => automatic executes first
```

Run:

```bash
go test ./internal/daemon -run 'AutomaticAdmission|AutomaticDisposition|StaleTerminal' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 4**

```bash
git add -- internal/daemon/lifecycle_operation_lock.go internal/daemon/lifecycle_operation_lock_automatic_test.go internal/daemon/tun_revalidation_coordinator.go internal/daemon/tun_revalidation_coordinator_test.go
git commit -m "feat: fence automatic TUN lifecycle dispositions"
```

---

### Task 5: Execute Automatic Terminal Teardown Under the Already-Owned Token

**Files:**
- Modify: `internal/daemon/tun_revalidation_terminal.go`
- Modify: `internal/daemon/tun_revalidation_terminal_test.go`
- Modify: `internal/daemon/network_session_teardown.go`
- Modify: `internal/daemon/server.go`

**Interfaces:**
- Produces: `tunAutomaticDispositionHandler.Handle(admission, disposition)`.
- Terminal execution calls the unwrapped `networkSessionTeardownCoordinator.Teardown(..., networkSessionTeardownTerminal)` or equivalent internal primitive.
- Consumes Task 4 admission and Task 1 disposition identity.

- [ ] **Step 1: Write RED tests proving terminal cannot use wrapped Disconnect**

Required assertions:

```go
func TestAdmittedTerminalUsesUnwrappedNetworkSessionTeardown(t *testing.T) {}
func TestAdmittedTerminalDoesNotAdvanceMutationGenerationTwice(t *testing.T) {}
func TestStaleTerminalSessionIdentityPerformsNoDiagnosticsOrCleanup(t *testing.T) {}
```

The identity mismatch test reloads Network Session state while the admission token is held and changes nothing on mismatch.

- [ ] **Step 2: Refactor terminal handler into the unified automatic-disposition handler**

Under an already-admitted token:

```text
reload Network Session
verify session ID and transaction ID if required
collect bounded diagnostics
persist terminal intent
run #261 exact data-plane cleanup while Privacy Envelope stays armed
remove exact Privacy Envelope
verify remaining host network
finalize diagnostics
release admission once
```

Do not call `lockedLifecycle.Disconnect`, `operationLockedLifecycle.Disconnect`, or `runRecoveryWithFollowUp`.

- [ ] **Step 3: Preserve cleanup-required publication on failed terminal teardown**

If the #261 teardown cannot converge, leave exact recovery authority durable and publish `cleanup-required` with the terminal classification. Do not clear Network Session state or Privacy Envelope authority falsely.

- [ ] **Step 4: Rewire `server.go` to give the automatic handler the unwrapped `sessionLifecycle` and exact state store**

Keep public HTTP Connect/Disconnect/Recover on `lockedLifecycle`. Only the automatic handler uses the internal unwrapped path because Task 4 already owns operation authority.

Run: `go test ./internal/daemon -run 'AdmittedTerminal|TunRevalidationTerminal|NetworkSessionTeardown' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit Task 5**

```bash
git add -- internal/daemon/tun_revalidation_terminal.go internal/daemon/tun_revalidation_terminal_test.go internal/daemon/network_session_teardown.go internal/daemon/server.go
git commit -m "fix: fence terminal TUN teardown to current session"
```

---

### Task 6: Prove and Prepare Protected Replacement Sources Before Rebuild

**Files:**
- Create: `internal/daemon/tun_reconciliation_replacement_source.go`
- Create: `internal/daemon/tun_reconciliation_replacement_source_test.go`
- Modify: `internal/daemon/tun_handoff_preflight.go`
- Modify: `internal/daemon/tun_active_replacement_coexistence.go`

**Interfaces:**
- Produces: `protectedTunReplacementSource` with `active` and `degraded` kinds.
- Produces: `loadProtectedTunReplacementSource(runtimeDir, managerState, process, sessionState) (protectedTunReplacementSource, error)`.
- Consumed by Task 7.

- [ ] **Step 1: Write RED tests for active and degraded source proof**

Define degraded source exactly as:

```text
Network Session exists, intent=resume
Privacy Envelope authority exists and is exact
manager mode=tun
manager Connection="error (core exited)"
manager cmd/process is absent
manager transaction ID points to the exact persisted committed/recoverable TUN generation
transaction/session/profile identity matches durable Network Session request
```

Tests:

```go
func TestProtectedReplacementSourceAcceptsActiveGeneration(t *testing.T) {}
func TestProtectedReplacementSourceAcceptsDegradedCoreExitedGeneration(t *testing.T) {}
func TestProtectedReplacementSourceRejectsDegradedMissingTransactionAuthority(t *testing.T) {}
func TestProtectedReplacementSourceRejectsDifferentNetworkSession(t *testing.T) {}
```

Run: `go test ./internal/daemon -run '^TestProtectedReplacementSource' -count=1`

Expected: FAIL.

- [ ] **Step 2: Implement typed source classification without pretending the dead core is active**

Use:

```go
type protectedTunReplacementSourceKind string

const (
    protectedTunReplacementActive   protectedTunReplacementSourceKind = "active"
    protectedTunReplacementDegraded protectedTunReplacementSourceKind = "degraded"
)
```

Active source still requires the live supervised-process proof. Degraded source explicitly requires no process and must prove durable transaction/session/privacy authority instead.

- [ ] **Step 3: Change replacement preflight to use the source proof**

`preflightActiveReplacementSessionOwnership` must no longer be the only accepted proof for automatic protected replacement. Keep public active handoff behavior unchanged, but expose a reusable internal preflight that can prove the degraded source generation without granting any foreign cleanup authority.

Run: `go test ./internal/daemon -run 'ProtectedReplacementSource|Handoff' -count=1`

Expected: PASS.

- [ ] **Step 4: Commit Task 6**

```bash
git add -- internal/daemon/tun_reconciliation_replacement_source.go internal/daemon/tun_reconciliation_replacement_source_test.go internal/daemon/tun_handoff_preflight.go internal/daemon/tun_active_replacement_coexistence.go
git commit -m "feat: prove degraded protected replacement sources"
```

---

### Task 7: Rebuild a Protected Session from a Degraded Source Generation

**Files:**
- Modify: `internal/daemon/tun_connect_lifecycle.go`
- Modify: `internal/daemon/privacy_envelope_lifecycle.go`
- Modify: `internal/daemon/network_session_lifecycle.go`
- Modify: `internal/daemon/network_session_replacement_restore.go`
- Create: `internal/daemon/network_session_degraded_rebuild_test.go`
- Modify: `internal/daemon/network_session_replacement_test.go`
- Modify: `internal/daemon/network_session_replacement_recovery_test.go`

**Interfaces:**
- Produces: internal `ReconcileProtectedTun(ctx, expectedSessionID string) error` (name may be method on `networkSessionLifecycle`; keep one unwrapped entry point).
- Reuses Task 6 source proof and existing `privacyEnvelopeLifecycle.PrepareReplacement`.
- Consumed by Task 8 automatic `reconcile` execution.

- [ ] **Step 1: Write the mandatory degraded-source RED regression from the review**

Build a fixture where:

```text
durable Network Session + armed Privacy Envelope exist
committed TUN transaction exists
XrayManager retains TUN/profile/transaction state
m.cmd=nil and Connection="error (core exited)"
new current host plan needs a different server bootstrap path
```

Assert the rebuild ordering:

```text
prove degraded source
prepare/widen exact Privacy Envelope to old+new bootstrap
recover/cleanup exact obsolete generation behind envelope
plan fresh collision-safe generation
start/apply/verify new generation
commit/narrow Privacy Envelope
fresh observation required before verified publication
```

The test must fail if `connectTun` simply checks `active == true` before preparing endpoint overlap.

Run: `go test ./internal/daemon -run 'DegradedProtectedRebuild' -count=1`

Expected: FAIL on current `active && replace-podlaz` gating.

- [ ] **Step 2: Extract replacement preparation so Privacy Envelope overlap is keyed to durable protected replacement, not live-core activity**

In `connectTun`, after the outer Network Session layer has persisted replacement intent, load the state and call `PrepareReplacement` whenever the proven Task 6 source has exact protection, including `degraded`. For active source, keep the existing old-generation disconnect. For degraded source, do **not** call the active disconnect path or invent a process; proceed to exact stale-generation recovery while the envelope is already widened.

- [ ] **Step 3: Make exact old-generation cleanup happen before the new generation is installed, without removing protection**

Use existing transaction-backed `autoRecoverTunOwnedState`/exact recovery paths only after Task 6 proves ownership. The degraded path may clean the dead generation's exact route/rule/DNS/TUN transaction residue; it must not classify historical IDs as ownership.

- [ ] **Step 4: Make failure behavior explicit for degraded source**

A failed degraded rebuild cannot assume an old running core exists. Keep Network Session intent and Privacy Envelope authority durable. One bounded restore attempt may rebuild from the previous request using the existing replacement-restore contract, but it is a **new generation build**, not resurrection of the dead process. If restore cannot produce a protected data plane, return the failure to the same supervisor cycle; do not remove the Privacy Envelope or publish disconnected.

- [ ] **Step 5: Add crash-boundary tests around degraded rebuild**

Cover:

```text
crash after envelope widening before old tx cleanup
crash after old tx cleanup before new core commit
failure after new generation apply before envelope narrowing
restart recovery converges exact envelope/replacement authority in all cases
```

Run:

```bash
go test ./internal/daemon -run 'DegradedProtectedRebuild|ReplacementRecovery|PrivacyEnvelopeReplacement' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 7**

```bash
git add -- internal/daemon/tun_connect_lifecycle.go internal/daemon/privacy_envelope_lifecycle.go internal/daemon/network_session_lifecycle.go internal/daemon/network_session_replacement_restore.go internal/daemon/network_session_degraded_rebuild_test.go internal/daemon/network_session_replacement_test.go internal/daemon/network_session_replacement_recovery_test.go
git commit -m "feat: rebuild protected TUN from degraded generation"
```

---

### Task 8: Execute Reconcile Dispositions and Schedule Fresh Bounded Re-observation

**Files:**
- Create: `internal/daemon/tun_reconciliation_execution.go`
- Create: `internal/daemon/tun_reconciliation_execution_test.go`
- Modify: `internal/daemon/server.go`
- Modify: `internal/daemon/tun_revalidation_coordinator.go`
- Modify: `internal/daemon/tun_revalidation_lifecycle.go`

**Interfaces:**
- Consumes Task 4 admission and Task 7 `ReconcileProtectedTun`.
- Produces one bounded retry timer per active session cycle.

- [ ] **Step 1: Write RED tests for automatic reconcile execution**

Required cases:

```go
func TestAdmittedReconcileUsesUnwrappedInternalLifecycle(t *testing.T) {}
func TestAdmittedReconcileRechecksNetworkSessionIdentity(t *testing.T) {}
func TestReconcileFailureSchedulesFreshObservationWithoutResettingCycle(t *testing.T) {}
func TestRetryTimerCoalescesWithRealNetworkEvent(t *testing.T) {}
func TestVerifiedOrTerminalCycleCancelsRetryTimer(t *testing.T) {}
```

- [ ] **Step 2: Implement reconcile under one automatic admission**

After identity recheck, invoke only the unwrapped internal Network Session reconciliation primitive. Do not call `lockedLifecycle.Connect`.

On completion, always schedule a fresh coordinator observation before healthy publication. The repair result itself never sets `verified`.

- [ ] **Step 3: Implement one timer per cycle**

Use an injectable timer function in tests. A real event and a retry timer both feed `coordinator.Notify`; coordinator coalescing remains authoritative. Cancel timer on verified, blocked ownership, terminal admission, disconnect, or session-ID change.

- [ ] **Step 4: Wire generation-one retry through the same scheduler**

`initial` may become retry/reconcile without clearing the cycle. A successful new generation from reconcile schedules a fresh initial/current proof rather than trusting connect-time verification.

Run: `go test ./internal/daemon -run 'AdmittedReconcile|RetryTimer|GenerationOne' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit Task 8**

```bash
git add -- internal/daemon/tun_reconciliation_execution.go internal/daemon/tun_reconciliation_execution_test.go internal/daemon/server.go internal/daemon/tun_revalidation_coordinator.go internal/daemon/tun_revalidation_lifecycle.go
git commit -m "feat: execute bounded TUN reconciliation"
```

---

### Task 9: Finish Soft/Hard Classification, Status Publication, and Diagnostic Semantics

**Files:**
- Modify: `internal/daemon/tun_revalidation_runtime.go`
- Modify: `internal/status/tun_health.go`
- Modify: `internal/status/tun_health_test.go`
- Modify: `internal/daemon/tun_revalidation_terminal.go`
- Modify: `internal/daemon/tun_revalidation_terminal_test.go`

**Interfaces:**
- Public API remains `verified`, `revalidating`, `degraded`, `cleanup-required`.
- `network_converging` means mandatory local unknown/changing.
- `owned_state_reconciling` means an admitted exact-owned repair/rebuild is active.

- [ ] **Step 1: Write RED publication tests**

Cover:

```text
mandatory local unknown -> revalidating/network_converging
one optional provider failure with sufficient proof -> verified/no failure classification
insufficient external evidence inside budget -> degraded/connectivity_failed
admitted repair -> revalidating/owned_state_reconciling
blocked ownership -> cleanup-required/ownership_invalid
```

- [ ] **Step 2: Remove legacy implication that every verification error is already terminal**

Update helpers/comments/tests so `degraded` means insufficient current evidence inside the bounded supervisor, not "terminal cleanup is already being entered".

- [ ] **Step 3: Preserve bounded diagnostic capture only for admitted terminal disposition**

Soft/convergence retries should not create one persistent failure report per round. Terminal pre-teardown diagnostics still run after current-session admission and before #261 cleanup.

Run: `go test ./internal/status ./internal/daemon -run 'TunHealth|Terminal|Reconciliation' -count=1`

Expected: PASS.

- [ ] **Step 4: Commit Task 9**

```bash
git add -- internal/daemon/tun_revalidation_runtime.go internal/status/tun_health.go internal/status/tun_health_test.go internal/daemon/tun_revalidation_terminal.go internal/daemon/tun_revalidation_terminal_test.go
git commit -m "fix: publish evidence-driven TUN health"
```

---

### Task 10: Add Deterministic End-to-End Daemon Regressions for Issue #262

**Files:**
- Create: `internal/daemon/issue262_reconciliation_regression_test.go`
- Create: `internal/daemon/issue262_disposition_race_test.go`
- Create: `internal/daemon/issue262_degraded_source_regression_test.go`
- Modify existing focused tests only when they duplicate a now-obsolete terminal-on-first-failure assertion.

**Interfaces:**
- Exercises production wiring from coordinator -> supervisor -> admission -> internal lifecycle.

- [ ] **Step 1: Add soft-failure/convergence regressions**

Deterministically script:

```text
one DNS timeout
one Cloudflare TLS/HTTPS failure with Google positive
resolved unknown then proven
NetworkManager unknown then proven
route absent then replacement route
bursty/reordered link/address/route notifications
```

Expected safe cases: no terminal teardown; final `verified` when sufficient proof becomes available.

- [ ] **Step 2: Add network-change/rebuild regressions**

Cover Wi-Fi/DHCP fingerprint change, uplink replacement, foreign unrelated TUN appearance, repairable Podlaz DNS/routing drift, and changed bootstrap path requiring protected replacement.

- [ ] **Step 3: Add stale automatic-disposition ABA regressions**

Use deterministic channels around post-run admission:

```text
terminal from session A -> explicit replace B declared -> old terminal superseded
terminal from generation A -> recovery completes -> old terminal superseded
reconcile admitted first -> later explicit Disconnect waits until reconcile releases token
```

- [ ] **Step 4: Add hard-failure regressions**

Confirm only bounded fresh hard/persistent evidence reaches admitted terminal teardown: privacy failure, no safe server path after convergence, unrecoverable core rebuild, unresolvable exact-owned conflict without foreign mutation, persistent independent provider/data-plane failure.

Run: `go test ./internal/daemon -run 'Issue262|TunSupervisor|AutomaticDisposition' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit Task 10**

```bash
git add -- internal/daemon/issue262_reconciliation_regression_test.go internal/daemon/issue262_disposition_race_test.go internal/daemon/issue262_degraded_source_regression_test.go
git commit -m "test: cover issue 262 reconciliation races"
```

---

### Task 11: Add Sanitized Installed-Package Acceptance and E2E Hooks

**Files:**
- Modify: `internal/daemon/e2e_tun_hooks.go`
- Modify: `internal/daemon/e2e_tun_hooks_test.go`
- Create: `scripts/e2e/issue262-package-acceptance.sh`
- Create: `scripts/e2e/issue262_acceptance_contract_test.go`
- Modify: `.github/workflows/e2e-tun-package-convergence.yml`
- Modify: `docs/e2e.md`

**Interfaces:**
- Adds controlled hooks only under existing `PODLAZ_E2E_TUN_HOOKS`/sanitized E2E gates.
- No production debug listener or permanent unsafe mutation path.

- [ ] **Step 1: Write acceptance-contract tests before the script**

Require the script to prove markers/scenarios for:

```text
soft_provider_failure_stayed_connected
resolved_convergence_recovered
route_replacement_recovered
surrounding_tun_preserved
core_exit_degraded_source_rebuilt
privacy_envelope_present_during_rebuild
terminal_failure_bounded
terminal_no_auto_reconnect
ordinary_network_after_terminal
```

Also reject manual product repair commands on the success path:

```text
podlaz recover --execute
ip rule del
ip route del
resolvectl revert podlaz0
nft delete table inet podlaz_pe_
systemctl restart NetworkManager
systemctl restart systemd-resolved
```

Run: `go test ./scripts/e2e -run Issue262 -count=1`

Expected: FAIL because the script/workflow step does not exist.

- [ ] **Step 2: Add narrowly scoped E2E fault injection**

Add hooks for one soft external failure and a deterministic reconciliation/rebuild boundary. The degraded-source acceptance must terminate the supervised Xray core (not the daemon), wait until status reflects core exit while the Privacy Envelope still exists, then prove the product rebuilds without manual repair.

Do not print the real endpoint/profile/host IP in marker files or public artifacts.

- [ ] **Step 3: Implement `issue262-package-acceptance.sh`**

Reuse shared E2E helpers for package state, privacy checks, and redaction. The script must leave unrelated foreign fixture state structurally unchanged and must always invoke the existing cleanup trap.

- [ ] **Step 4: Wire one manual workflow step**

Add after issue 261 acceptance:

```yaml
- name: Run issue 262 evidence-driven reconciliation acceptance
  timeout-minutes: 60
  shell: bash
  run: bash scripts/e2e/issue262-package-acceptance.sh
```

Run:

```bash
go test ./scripts/e2e -run Issue262 -count=1
bash -n scripts/e2e/issue262-package-acceptance.sh
```

Expected: PASS.

- [ ] **Step 5: Commit Task 11**

```bash
git add -- internal/daemon/e2e_tun_hooks.go internal/daemon/e2e_tun_hooks_test.go scripts/e2e/issue262-package-acceptance.sh scripts/e2e/issue262_acceptance_contract_test.go .github/workflows/e2e-tun-package-convergence.yml docs/e2e.md
git commit -m "test: add issue 262 package acceptance"
```

---

### Task 12: Update Canonical Current-Behavior Documentation

**Files:**
- Modify: `docs/tun-uplink-revalidation.md`
- Modify: `docs/state-and-security.md`
- Modify: `docs/packaged-tun-runtime.md`
- Modify man pages only if generated/current wording exposes the changed health classifications.

**Interfaces:**
- Documentation must describe implemented behavior, not target-only intent.

- [ ] **Step 1: Rewrite the current revalidation contract**

Replace `fresh verification failure => terminal` wording with the implemented supervisor contract:

```text
fresh observe -> mandatory local proof -> soft independent evidence -> classify
-> retry/reconcile if recoverable -> fresh observe -> verified
-> admitted terminal only after bounded current-session evidence
```

Explicitly document generation-one inclusion, mandatory local unknown vs soft external evidence, fixed overall bound/no-progress budget, unified automatic-disposition fencing, and degraded-source protected rebuild.

- [ ] **Step 2: Update state/security ownership and privacy sections**

Document that reconciliation can mutate only exact Network Session state, automatic disposition must match session identity, and stale terminal cannot tear down a replacement/recovered session.

- [ ] **Step 3: Update packaged runtime behavior**

Document that unexpected supervised core exit with durable session/privacy authority is a degraded source generation eligible for protected rebuild; it is not treated as an active-core replacement and does not remove the Privacy Envelope.

- [ ] **Step 4: Run documentation/reference tests**

Run:

```bash
go test ./... -run 'TunHealth|Issue262|Revalidation' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit Task 12**

```bash
git add -- docs/tun-uplink-revalidation.md docs/state-and-security.md docs/packaged-tun-runtime.md
git commit -m "docs: describe bounded TUN reconciliation"
```

---

### Task 13: Full Verification and PR Readiness

**Files:**
- No planned production changes; fix only failures demonstrated by these commands and keep fixes inside #262 scope.

- [ ] **Step 1: Run formatting gate**

```bash
test -z "$(gofmt -l .)"
```

Expected: exit 0 and no output.

- [ ] **Step 2: Run complete Go tests**

```bash
go test ./...
```

Expected: exit 0.

- [ ] **Step 3: Run static analysis and vulnerability scan**

```bash
go vet ./...
govulncheck ./...
```

Expected: exit 0 for both.

- [ ] **Step 4: Run CLI smoke checks required by `docs/development.md`**

```bash
go run ./cmd/podlaz version
go run ./cmd/podlaz completion bash >/dev/null
go run ./cmd/podlaz completion zsh >/dev/null
go run ./cmd/podlaz completion fish >/dev/null
```

Expected: exit 0.

- [ ] **Step 5: Run deterministic E2E contract checks**

```bash
go test ./scripts/e2e -count=1
bash -n scripts/e2e/issue262-package-acceptance.sh
```

Expected: exit 0.

- [ ] **Step 6: Build and statically inspect the Debian package**

```bash
bash scripts/build-deb.sh
dpkg-deb --info dist/podlaz_0.0.0~dev-1_linux_amd64.deb
dpkg-deb --contents dist/podlaz_0.0.0~dev-1_linux_amd64.deb
```

Expected: package builds and metadata/content inspection succeeds.

- [ ] **Step 7: Run self-hosted installed-package acceptance before claiming real-host validation**

On the controlled Ubuntu 24.04 `vpn-e2e` runner:

```bash
bash scripts/e2e/issue262-package-acceptance.sh
```

Expected: all sanitized issue-262 scenarios pass. If the self-hosted environment is not available, state that limitation explicitly in the PR instead of claiming packaged real-host validation.

- [ ] **Step 8: Compare branch to master and review scope/privacy**

Verify only #262-related code/tests/docs are changed; scan added fixtures, comments, shell output, and PR body for real user IPs/domains/SSIDs/profile IDs/private endpoints.

- [ ] **Step 9: Open one draft PR against `master`**

Use a concise body covering:

```text
Closes #262
- evidence model and supervisor behavior
- generation-one semantics
- unified reconcile/terminal admission and stale-terminal ABA protection
- active + degraded protected replacement paths
- privacy/ownership/foreign-state guarantees
- deterministic tests and packaged acceptance status
- exact validation commands/results
```

Create the PR as draft until all available deterministic checks are green and any required self-hosted evidence status is accurately recorded.
