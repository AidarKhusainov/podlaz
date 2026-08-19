# Network Session Continuity Implementation Plan

**Goal:** make an active TUN session lifecycle-safe across daemon restart, crash restart, and Debian package upgrade without weakening exact ownership or making ordinary sessions survive reboot.

**Architecture:** keep exact network cleanup authority in the existing transaction model; add a separate private volatile continuation record for the connect intent. Distinguish restart jobs from explicit service stops with systemd `RestartKillSignal=` versus `KillSignal=`. Use `KillMode=mixed` so the daemon receives the graceful signal before supervised children are force-killed, and preserve `/run/podlaz` within the boot so exact transaction authority survives incomplete teardown. On startup, converge exact-owned stale state before replaying the current-boot continuation and before opening the API socket.

**Safety invariants:**

- continuation intent never grants cleanup authority;
- only transaction-backed exact ownership can remove networking state;
- explicit disconnect/stop disarms continuation before teardown mutation;
- restart/upgrade teardown preserves continuation;
- a boot-id mismatch can never auto-resume a normal session;
- teardown failure is returned as failure and leaves transaction evidence intact;
- continuation data is `0600`, atomic, never logged, and may contain profile secrets only under `/run`;
- package maintenance uses the normal service restart contract, not package-specific network cleanup.

## Task 1: Specify deterministic lifecycle semantics

**Files:**
- Create `internal/daemon/network_session_continuation_test.go`
- Create `internal/daemon/daemon_lock_test.go`
- Update `internal/service/systemd_test.go`
- Create `internal/app/daemon/shutdown_signal_test.go`

**Tests:**
1. continuation is armed before connect and marked active only after connect succeeds;
2. explicit disconnect disarms before teardown and a teardown failure cannot recreate continuation;
3. restart teardown preserves continuation;
4. a current-boot continuation loads while a mismatched boot-id is rejected and removed;
5. continuation file is private and atomically replaceable;
6. a stale lock file does not block a new daemon, while a live kernel lock does;
7. systemd contract includes `KillMode=mixed`, distinct restart signal, runtime preservation, and sufficient stop timeout;
8. signal classification maps restart signal to restart intent and SIGTERM/SIGINT to explicit stop.

## Task 2: Implement private continuation state and lifecycle wrapper

**Files:**
- Create `internal/daemon/network_session_continuation.go`
- Create `internal/daemon/shutdown_intent.go`

**Implementation:**
- versioned record with owner, boot id, phase, and `api.ConnectRequest`;
- boot id read from `/proc/sys/kernel/random/boot_id` with an injectable reader for tests;
- atomic `0600` write, fsync, rename, directory sync;
- lifecycle wrapper arms before connect, removes after returned connect failure, marks active after success, disarms before ordinary disconnect, and can preserve the record for restart teardown.

## Task 3: Make startup converge then resume before accepting mutations

**Files:**
- Create `internal/daemon/network_session_startup.go`
- Update `internal/daemon/server.go`
- Add focused startup tests to `internal/daemon/network_session_continuation_test.go`

**Implementation:**
1. acquire daemon ownership lock;
2. load current-boot continuation;
3. when continuation exists, execute existing transaction-backed daemon recovery while the lifecycle is still inactive;
4. refuse replay if recovery still has failed/actionable ambiguous state;
5. replay the stored connect request through the normal lifecycle stack;
6. only then publish startup scan and open the API socket;
7. if replay reaches a clean terminal connect failure, disarm continuation; if exact recovery remains required, retain transaction authority and do not manufacture cleanup authority.

## Task 4: Correct shutdown ordering and failure propagation

**Files:**
- Update `internal/daemon/server.go`
- Add focused tests in `internal/daemon/server_test.go`

**Implementation:**
- explicit stop: disarm continuation before disconnect;
- restart/upgrade: preserve continuation while performing normal ordered disconnect;
- return disconnect/rollback errors instead of discarding them;
- join HTTP shutdown / serve errors with lifecycle teardown errors;
- use a bounded shutdown budget derived from existing TUN rollback/core-stop bounds rather than a small arbitrary five-second cleanup window.

## Task 5: Make daemon locking crash-safe

**Files:**
- Create `internal/daemon/daemon_lock_linux.go`
- Update `internal/daemon/server.go`
- Update `internal/daemon/server_test.go` where old O_EXCL semantics were asserted.

**Implementation:**
- use an advisory kernel file lock on the existing lock path;
- keep the lock file as inert metadata; kernel ownership is released automatically on process death;
- never unlink a live lock in a way that could let another daemon lock a different inode.

## Task 6: Encode the systemd/service lifecycle contract

**Files:**
- Update `packaging/systemd/podlazd.service`
- Update `internal/service/systemd_test.go`
- Update `internal/app/daemon/daemon.go`

**Implementation:**
- `KillSignal=SIGTERM` for explicit stop/reboot shutdown;
- `RestartKillSignal=SIGUSR1` for restart/upgrade continuation;
- `KillMode=mixed` so only the main daemon receives the initial stop/restart signal and child cleanup remains daemon-ordered;
- `RuntimeDirectoryPreserve=yes` so incomplete exact transaction authority is not discarded by an explicit stop; `/run` still provides the reboot boundary;
- explicit stop disarms continuation before teardown, so preserving `/run` cannot cause a later manual start to auto-resume;
- set `TimeoutStopSec` above the daemon's bounded rollback/core-stop budget.

## Task 7: Add package/service acceptance coverage

**Files:**
- Create `scripts/e2e/issue259-package-acceptance.sh`
- Update `.github/workflows/e2e-tun-package-convergence.yml`
- Update `docs/e2e.md`

**Acceptance scenarios:**
- active TUN + `systemctl restart podlazd` automatically returns active without CLI reconnect;
- force-kill daemon + systemd automatic restart automatically converges/reconnects or retains exact transaction authority;
- lower package -> candidate package upgrade while active requires no manual reconnect/recover/network commands;
- fault injection during stop/upgrade proves transaction files remain when teardown cannot complete;
- explicit `systemctl stop` does not auto-resume after later start;
- evidence emitted by scripts is normalized/redacted and uses only documentation/example constants in repository text.

## Task 8: Update lifecycle/security documentation

**Files:**
- Update `docs/state-and-security.md`
- Update `docs/packaged-tun-runtime.md`
- Update `docs/man/podlazd.8`

Document continuation versus cleanup authority, systemd signal/process semantics, reboot boundary, and failure propagation. Do not claim privacy guarantees reserved for #261.

## Task 9: Verify and review

Run/require:

```bash
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
govulncheck ./...
bash scripts/ci/workflow-lint.sh
bash scripts/ci/validate-packages.sh
```

Then inspect the full `master...HEAD` diff for scope, secret/domain/IP leakage, fail-closed ownership regressions, package behavior, and documentation consistency. Open a PR only after CI-visible code and tests are complete; if host-only installed-package acceptance cannot run in GitHub-hosted CI, leave the PR draft with that requirement explicit rather than claiming it passed.
