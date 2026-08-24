# Boot Autostart and Product Lifecycle UX Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Issue #263 with a daemon-owned persistent Boot Autostart Manifest, one logical autostart attempt per boot, continuation-first startup ordering, and a concise product-oriented human lifecycle UX.

**Architecture:** Keep same-boot `networkSessionState` unchanged as the authority for restart/crash/package-upgrade continuity. Add a separate private persistent manifest under the daemon `StateDirectory` and a volatile boot-attempt record under `/run/podlaz`; startup first converges any existing Network Session, then continues an admitted boot attempt or admits one fresh manifest-backed attempt through the existing locked `Connect` path. Human CLI rendering derives stable product states while detailed daemon status/diagnostics remain intact.

**Tech Stack:** Go 1.26.6, Linux/systemd, Unix-socket daemon API, polkit, existing JSON persistence helpers, existing Network Session lifecycle/reconciliation, shell/package acceptance.

**Spec:** `docs/superpowers/specs/2026-08-21-boot-autostart-lifecycle-ux-design.md`

**Implementation status:** Tasks 1-7 are implemented. Final Task 8 validation is intentionally deferred per the maintainer instruction from 2026-08-24; do not interpret unchecked verification items as missing product implementation.

## Global Constraints

- Ordinary `connect` never creates persistent boot policy.
- Continuation/recovery always wins over fresh autostart admission.
- At most one logical autostart lifecycle is admitted per `boot_id`; completed success/terminal states never re-admit in the same boot.
- Autostart configuration takes effect on a later boot, not on same-boot daemon restart.
- Autostart uses canonical normal `Connect`; no verifier/networking bypass and no persisted `handoff` field.
- Manifest and attempt files are private mode `0600`, bounded, strict, atomic, fsynced, and never logged/rendered with sensitive material.
- CLI reads the user profile store; the root daemon never searches user homes.
- Detailed daemon status/doctor/recover evidence remains available; only normal human lifecycle output is simplified.
- Use documentation/example values only in code/tests/docs/PR text.

---

### Task 1: Define typed autostart daemon API

**Files:**
- Create: `internal/api/autostart.go`
- Create: `internal/api/autostart_test.go`

**Interfaces:**
- Produces `AutostartConfigurePath`, `AutostartStatusPath`.
- Produces `AutostartConfigureRequest{Mode string, Profile ProfileSnapshot}` with no `Handoff`.
- Produces `AutostartStatusResponse{Enabled bool, Mode string, ProfileName string}`.
- Produces `ValidateAutostartConfigureRequest` and `ValidateAutostartStatusResponse`.

- [x] Write API tests proving valid proxy/TUN snapshots are accepted, missing mode/profile fields fail, no handoff field exists in encoded configuration requests, and disabled status cannot carry profile/mode data.
- [x] Run focused API tests and confirm RED before implementation.
- [x] Implement the minimal versioned daemon request/status types by reusing `ValidateConnectRequest` through a canonical reconstructed request with omitted handoff.
- [x] Run focused API tests and confirm GREEN.
- [x] Commit `test/feat: define boot autostart API`.

### Task 2: Implement crash-safe manifest and boot-attempt stores

**Files:**
- Create: `internal/daemon/boot_autostart_store.go`
- Create: `internal/daemon/boot_autostart_store_test.go`

**Interfaces:**
- `newBootAutostartManifestStore(stateDir string, readBootID bootIDReader) bootAutostartManifestStore`
- `Enable(api.AutostartConfigureRequest) (bootAutostartManifest, error)`
- `Disable() error`
- `Load() (bootAutostartManifest, bool, error)`
- `newBootAutostartAttemptStore(runtimeDir string, readBootID bootIDReader) bootAutostartAttemptStore`
- `Admit(manifest bootAutostartManifest) (bootAutostartAttempt, error)`
- `LoadCurrent() (bootAutostartAttempt, bool, error)`
- `MarkSucceeded() error`
- `MarkTerminal(reason bootAutostartTerminalReason) error`

- [x] Write deterministic tests for strict schema/trailing/unknown fields, `0600`, max-size bound, previous-boot attempt discard, manifest configured in current boot being ineligible, pinned admitted request, disabled-as-absence, and attempt state transitions `in_progress -> succeeded|terminal` only.
- [x] Run focused store tests and confirm RED.
- [x] Implement stores using `atomicWritePrivateFile`, `syncFilesystemDirectory`, cryptographically random non-secret manifest generation, strict `json.Decoder.DisallowUnknownFields`, exact boot-id validation, and fsynced unlink.
- [x] Run focused store tests and confirm GREEN.
- [x] Commit `feat: persist boot autostart authority`.

### Task 3: Add daemon/client configuration surface and authorization

**Files:**
- Create: `internal/daemon/boot_autostart_handlers.go`
- Create: `internal/daemon/boot_autostart_handlers_test.go`
- Create: `internal/client/autostart.go`
- Create: `internal/client/autostart_test.go`
- Modify: `internal/daemon/authorization.go`
- Modify: `internal/daemon/polkit_policy_test.go`
- Modify: `packaging/polkit-1/actions/io.github.aidarkhusainov.podlaz.policy`

**Interfaces:**
- Add `ActionConfigureAutostart` as a dedicated polkit action.
- `registerBootAutostartHandlers(mux, manifestStore, authorizer)` exposes POST configure/disable and GET status without lifecycle mutation authority.
- `client.AutostartClient.Enable/Disable/Status` mirrors daemon API and existing abstract-socket fallback behavior.

- [x] Write handler/client tests for authorization deny/allow, read-only status without mutation authorization, disabled status, strict request validation, redaction/non-leakage, and HTTP method contracts.
- [x] Run focused tests and confirm RED.
- [x] Implement handlers/client and add conservative packaged polkit policy entry.
- [x] Run focused tests and confirm GREEN.
- [x] Commit `feat: expose boot autostart configuration`.

### Task 4: Orchestrate continuation-first one-logical-attempt startup

**Files:**
- Create: `internal/daemon/boot_autostart_startup.go`
- Create: `internal/daemon/boot_autostart_startup_test.go`
- Modify: `internal/daemon/server.go`
- Modify if needed: `internal/daemon/network_session_lifecycle.go`

**Interfaces:**
- `runBootAutostartStartup(ctx, manifestStore, attemptStore, continuation, lockedLifecycle, resumeOutcome) bootAutostartStartupResult`.
- Result is typed: no-op, continued, connected, terminal, blocked; it never exposes secret-bearing errors to normal logs.
- Startup calls existing `resumeNetworkSession` first. Only after no current Network Session remains may it inspect the attempt/manifest.

- [x] Write tests for: disabled -> no attempt; current-boot configured manifest -> no attempt; previous-boot manifest -> one connect; crash after attempt admission before continuation -> replay exact pinned request; continuation exists -> no competing connect; succeeded/terminal attempt -> no same-boot reconnect; explicit disconnect after success -> no restart reconnect; manifest mutation during in-progress replay does not substitute profile; malformed current attempt fails closed; next boot permits a new logical attempt.
- [x] Write serialization tests proving autostart uses the existing lifecycle operation lock/startup gate and cannot race recovery/shutdown.
- [x] Run focused startup tests and confirm RED.
- [x] Implement orchestration and wire it into `Server.Run` after `resumeNetworkSession` convergence but before accepting normal lifecycle requests; keep classification-only startup logs.
- [x] Run focused tests and confirm GREEN.
- [x] Commit `feat: run one logical autostart attempt per boot`.

### Task 5: Add `podlaz autostart` CLI and completions

**Files:**
- Create: `internal/app/cli/autostart_cli.go`
- Create: `internal/app/cli/autostart_cli_test.go`
- Modify: `internal/app/cli/cli.go`
- Modify: `internal/app/cli/help_cli.go`
- Modify: `internal/app/cli/completion_engine.go`
- Modify: relevant completion tests

**Interfaces:**
- `podlaz autostart enable [--mode proxy-only|tun] <profile-id>` loads/validates user profile exactly like connect, constructs canonical snapshot, then calls daemon configuration API.
- `podlaz autostart disable` changes future boot policy only.
- `podlaz autostart status` is read-only and renders `Autostart: Enabled for next boot` or `Autostart: Disabled`.

- [x] Write CLI tests for parsing, same validation path as `connect`, daemon-unavailable exit code, no immediate connect, concise output, deferred `--json`, help, and dynamic profile completion.
- [x] Run focused CLI tests and confirm RED.
- [x] Implement command routing/help/completion/client wiring.
- [x] Run focused CLI tests and confirm GREEN.
- [x] Commit `feat: add autostart CLI`.

### Task 6: Add centralized product lifecycle projection and concise human output

**Files:**
- Create: `internal/status/product.go`
- Create: `internal/status/product_test.go`
- Modify: `internal/status/status.go`
- Modify: `internal/app/cli/status_cli.go`
- Modify: `internal/app/cli/connect_cli.go`
- Modify: corresponding status/connect CLI tests
- Modify daemon status publication only if needed to expose typed terminal reason/autostart summary without removing existing fields.

**Interfaces:**
- Product states: `Connected`, `Connecting`, `Reconnecting`, `Disconnected`, with `Unknown` for unavailable/incomplete evidence.
- Product terminal reasons use a small stable typed classification; no arbitrary internal error parsing.
- `status.Report.ProductView(...)` (or equivalent single centralized projector) renders profile/mode/autostart and hides routes/DNS/firewall/transaction/runtime-config detail from default human status.

- [x] Write tests mapping verified active -> Connected, active revalidation/rebuild -> Reconnecting, admitted connect -> Connecting, exact inactive/terminal cleanup complete -> Disconnected, and unavailable/incomplete evidence -> Unknown; transient reconciliation must not be terminal.
- [x] Write CLI output tests proving connect outputs only `Connected`, profile, mode; disconnect outputs only `Disconnected`; status does not print transaction/routes/DNS/firewall/runtime config while operator data remains in the report/API model.
- [x] Run focused status/CLI tests and confirm RED.
- [x] Implement centralized projection and concise renderers without deleting detailed daemon fields.
- [x] Run focused tests and confirm GREEN.
- [x] Commit `feat: simplify lifecycle human UX`.

### Task 7: Align package/service/docs/man/acceptance contracts

**Files:**
- Modify: `docs/cli.md`
- Modify: `docs/state-and-security.md`
- Modify: `docs/debian-package.md`
- Modify: `docs/e2e.md`
- Modify: `docs/man/podlaz.1`
- Modify: `docs/man/podlazd.8`
- Modify: `internal/service/systemd_test.go`
- Modify/create: `scripts/ci/*` deterministic contract checks as appropriate
- Create/update self-hosted installed-package acceptance for Issue #263.

- [x] Add tests/contracts proving packaged `StateDirectory=podlaz` remains private, new polkit action ships, completions/man pages include autostart, and normal service restart semantics remain independent of autostart.
- [x] Add installed acceptance covering boot boundary with autostart off/on, daemon restart while connected, package upgrade while connected, explicit disconnect, terminal autostart failure, and no retry loop.
- [x] Update canonical docs/man pages to describe Boot Autostart Manifest, one logical attempt per boot, continuation priority, concise human UX, and operator-only recovery detail.
- [ ] Run deterministic shell/package contract tests available in hosted CI. Deferred by maintainer instruction; run in Task 8 validation pass.
- [x] Commit `docs/test: align boot autostart package contract` (implemented as small reviewable commits across owning files).

### Task 8: Verification and PR

**Files:** no new product files unless verification finds defects.

- [ ] Verify final branch diff contains no private user IP/domain/profile/SSID values. Final verification pass deferred by maintainer instruction; implementation uses documentation/example values only.
- [ ] Run/verify hosted equivalents of `test -z "$(gofmt -l .)"`, `go test ./...`, `go vet ./...`, `govulncheck ./...`, version and completion smoke checks, package validation, and relevant deterministic script tests.
- [ ] Inspect failures using workflow job logs; fix root causes and repeat until green.
- [ ] Compare final branch against `master` and self-review for scope, lifecycle races, secret leakage, backward diagnostic compatibility, and Issue #263 acceptance criteria.
- [x] Open a draft PR from `issue-263-boot-autostart-ux` to `master` with summary, safety/recovery implications, checks run/deferred, and `Closes #263`.
