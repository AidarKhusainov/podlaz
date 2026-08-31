# Release laptop restart-friendly state machine design

Status: approved in chat; written specification pending final user review.

## Goal

Make the standalone `release-laptop.sh` resilient to ordinary operator mistakes, harmless representation changes, transient host conditions, interrupted runs, and expected product convergence without weakening its fail-closed ownership rules.

The harness must behave like a release qualification controller, not a brittle linear shell script. A simple mismatch that is already conclusively safe must not consume a long timeout, strand an active test session, or require the operator to manually collect diagnostics and run cleanup commands.

The operator should be able to rerun the same one-file harness after a failure and get a deterministic outcome:

- continue safely when the last boundary is replay-safe;
- automatically preserve all useful failure evidence;
- automatically restore the laptop after an unexpected failed qualification when exact cleanup authority is proven;
- cleanly retire the previous run and start over when exact owned state can be proven and restored;
- refuse to guess when live state is ambiguous;
- fail quickly for permanent/preflight errors instead of waiting through long scenario timeouts.

Convenience must never be implemented by deleting authority, broadly flushing networking, or treating cleanup as evidence of PASS.

## Robustness invariants

The following are mandatory design rules for every scenario and helper:

1. **Machine decisions use semantic state, never presentation strings.** Human-readable fields such as `tun: "enabled (podlaz0)"`, formatted CLI text, warning wording, or summary labels are evidence only and must not be treated as enums unless the product contract explicitly defines them as such.
2. **Stable JSON/schema fields are validated explicitly.** Unknown or incompatible schema is an immediate classified failure with the payload preserved; it must not degrade into a full timeout.
3. **Already-satisfied state succeeds immediately.** Polling helpers first evaluate the current authoritative snapshot before sleeping.
4. **Known impossible/terminal state fails immediately.** A poll must stop as soon as fresh evidence proves the target state cannot be reached in that scenario.
5. **Transient progress is not failure.** States such as health `revalidating` may continue within the scenario's bounded convergence window when the product contract says they represent progress.
6. **Read retries are allowed; mutation retries are not blind.** Before repeating any mutation, the harness re-observes exact live state and its persisted ownership ledger.
7. **Every disruptive mutation has a durable pre-mutation boundary.** Shell death must never make it impossible to determine whether the harness may reconcile that mutation.
8. **No accidental `set -e` semantics.** Expected non-zero command results are captured and classified explicitly. `grep`, `jq`, `find`, `systemctl`, `ip`, `nft`, `resolvectl`, `curl`, and similar commands must not terminate the run merely because a non-zero status is meaningful in that check.
9. **One failure controller owns post-checkpoint failure handling.** Cleanup is never recursively invoked from nested helpers.
10. **One global lock covers new/resume/restart/abort/failure-finalization.** Concurrent lifecycle operations are rejected.

These rules apply to current scenarios and to future additions to the harness.

## Semantic Podlaz status contract

The harness must not compare the human-readable `tun` field to a synthetic value such as `"active"`.

For an active protected TUN session, the harness evaluates the structured status contract using stable semantic evidence, including at minimum:

- `connection == "active"`;
- `mode == "tun"`;
- `tun_health.state == "verified"` when the scenario requires verified health;
- a current committed active transaction consistent with the reported active transaction identity;
- no authoritative terminal/cleanup state contradicting the active session.

A status payload such as:

```json
{
  "connection": "active",
  "mode": "tun",
  "tun": "enabled (podlaz0)",
  "tun_health": {"state": "verified"},
  "transactions": [{"state": "committed", "requires_cleanup": false}]
}
```

must satisfy `wait active`; the formatted `tun` string is not part of the predicate.

For inactive convergence, the harness similarly uses semantic inactive/session/transaction evidence rather than requiring one particular UI string.

Regression tests must include both `revalidating -> verified` progress and verified active payloads whose human-readable fields differ from earlier versions.

## CLI contract

Primary invocation remains:

```bash
sudo ./release-laptop.sh ./podlaz_<candidate>_linux_<arch>.deb --profile <profile-id>
```

Existing explicit recovery commands remain supported:

```bash
sudo ./release-laptop.sh --resume
sudo ./release-laptop.sh --abort
```

Add one explicit restart mode:

```bash
sudo ./release-laptop.sh ./podlaz_<candidate>_linux_<arch>.deb --restart --profile <profile-id>
```

`--restart` means: reconcile and restore only exact harness-owned state from the previous checkpoint, archive its result/evidence, then start a new run from a clean boundary using the newly supplied inputs. It never broad-cleans unknown network/systemd state and never discards ambiguous authority.

A normal new invocation with a candidate is also restart-friendly. If no checkpoint exists it starts normally. If a checkpoint exists, the harness first performs read-only classification:

1. **safe replay boundary** -> continue the existing run automatically when the invocation is compatible with the recorded candidate/config;
2. **retirable failed/interrupted run with exact cleanup authority** -> cleanly restore/close the old run and start the new one automatically;
3. **intentional reboot-wait phase** -> do not silently destroy the qualification run; print the exact `--resume` or `--restart` command and exit quickly;
4. **ambiguous/foreign live state** -> fail closed, retain the checkpoint, and print the exact reason and safe next action.

The harness never treats deleting `current.json` as recovery.

## Failure model

Persist a small failure envelope in the checkpoint whenever a run exits non-zero after checkpoint creation:

```text
last_failure:
  step
  scenario
  class
  exit_code
  retry_policy
  occurred_at
  cleanup_outcome
```

The checkpoint also stores immutable `run_started_at` and the starting boot identity so diagnostics can be bounded to this run rather than collecting an unscoped system history.

No secret, endpoint, IP, user profile identifier, SSID, or host-specific address is written to shareable output.

Failure classes:

- `INPUT`: bad CLI, missing/changed package, invalid profile, unsafe artifact path;
- `HOST_CAPABILITY`: unsupported optional Wi-Fi/suspend/cgroup feature;
- `HOST_STATE`: required host state is currently incompatible but no harness-owned mutation is lost;
- `PRODUCT`: Podlaz behavior did not satisfy the acceptance contract;
- `OWNERSHIP`: exact harness authority is ambiguous or foreign state occupies a recorded identity;
- `INTERRUPTED`: shell/process/power interruption detected at a persisted boundary;
- `INTERNAL`: invariant/parser/script defect or unsupported contract/schema.

Retry policies:

- `RETRY`: rerunning the same command can safely re-attempt the current replay-safe step;
- `RESTART`: current qualification evidence is invalid for continuation, but exact cleanup permits a fresh run;
- `RESUME_AFTER_REBOOT`: intentional reboot phase;
- `ABORT_ONLY`: cleanup/restoration is allowed but continuing or restarting automatically is not;
- `MANUAL_DIAGNOSIS`: ownership/evidence is ambiguous; no automated mutation follows.

## Automatic failure evidence bundle

Every unexpected failure after checkpoint creation must preserve diagnostics **before** automatic cleanup mutates the host.

The private per-run failure bundle contains, where available:

- an atomic copy of the checkpoint at failure time;
- `commands.log` and the exact last command/result envelope;
- last successful and last observed Podlaz JSON status payloads;
- bounded `podlaz doctor --tun` output/report when the daemon API is responsive;
- bounded `systemctl status` for the packaged daemon;
- journal entries for the daemon scoped from `run_started_at` within the recorded boot, not an unbounded system journal dump;
- installed Podlaz package version and recorded package authority;
- exact harness-owned mutation ledger;
- continuation, active transaction, and boot-attempt state when relevant;
- scenario-specific exact route/rule/link/nft/resolved observations needed to diagnose that scenario;
- failure class, current step/scenario, timeout/progress state, and recommended next action.

Failure evidence collection is best-effort for non-authority diagnostics: failure to collect `doctor` or a journal excerpt must not prevent cleanup. Authority-bearing observations required to prove safe cleanup remain mandatory and fail closed if unavailable or ambiguous.

Private evidence may contain host-local values necessary for diagnosis. Public reports are structurally sanitized and must not expose personal endpoints, addresses, profile identifiers, credentials, or subscription data.

## Automatic safe abort after unexpected failure

After the private failure bundle is durably written, an unexpected failed qualification automatically enters a single guarded failure-finalization path.

The operator must not normally need to run `podlaz disconnect` or `--abort` manually.

Automatic cleanup order:

1. mark the run `failure-cleanup-running` durably and prevent recursive finalization;
2. perform fresh read-only reconciliation of the checkpoint and mutation ledger;
3. stop only harness-owned background observers;
4. if the acceptance run owns an active test session, request controlled Podlaz disconnect and verify inactive/cleanup convergence **before package restoration**;
5. reconcile exact NetworkManager state if owned;
6. release exact network fixtures and systemd fault-injection hooks;
7. restore exact autostart policy and remove the exact synthetic profile when owned;
8. restore/install the recorded candidate package only when package authority proves it is required;
9. restart/reload packaged components only where the normal package/lifecycle contract requires it, never as a broad repair heuristic;
10. verify all non-package mutation ledger entries are released;
11. verify candidate package state required by the run's restoration contract;
12. verify Podlaz is conclusively inactive and ordinary DNS/TCP/HTTPS/direct egress work;
13. write final private/public failure reports;
14. remove the root checkpoint only after complete cleanup verification.

If cleanup succeeds, the run terminates as:

```text
FAILED_CLEAN
```

The qualification remains failed, but the laptop is restored and the same normal candidate command may be run again immediately.

If any ownership-critical cleanup observation is ambiguous, cleanup stops and terminates as:

```text
FAIL_CLEANUP_FAILED
```

The checkpoint and evidence remain. The harness must not broad-clean or guess in order to reach `FAILED_CLEAN`.

Expected control-flow pauses do **not** trigger automatic abort. In particular:

- real reboot boundaries;
- a deliberate `--resume` requirement;
- successful completion;
- explicit user-requested `--abort`/`--restart` flows.

`SIGINT` and `SIGTERM` after checkpoint creation use the same guarded evidence-then-cleanup finalizer on a best-effort basis. Power loss cannot execute an in-process finalizer; the next invocation performs persisted reconciliation from the last durable boundary.

## Replay-safe checkpoints

Replace the coarse `running-pre-reboot` non-replayable region with explicit persisted scenario boundaries.

Each disruptive scenario uses this shape:

```text
scenario_pending
-> scenario_prepared
-> scenario_running
-> scenario_verifying
-> scenario_passed | scenario_failed
```

The exact persisted phase is updated before any mutation that may survive shell death.

Replay rules:

- `pending/prepared`: safe to run from the beginning after read-only reconciliation;
- `running`: reconcile the scenario's own mutation ledger. If baseline/exact planned/exact acquired state can be classified, either finish cleanup and retry or mark the run restart-required;
- `verifying`: never rerun the mutation blindly. Re-observe product/host state and finish the verdict if evidence is still conclusive; otherwise restart-required;
- `passed`: never execute again in the same run;
- `failed`: preserve failure evidence; automatic safe abort runs unless the failure is intentionally classified as a pause/manual-diagnosis boundary.

The three intentional reboot phases remain special persisted boundaries and require a real new `boot_id` for `--resume`.

## Fast-fail behavior

Fast failure does not mean shortening legitimate product convergence windows. It means proving permanent errors before entering them and ending waits when fresh evidence already determines the outcome.

Before creating a new run checkpoint, consolidated preflight must conclusively verify:

- candidate/previous package existence, identity, architecture, checksum and Debian ordering;
- full-qualification lower-release availability: if the installed version is not already strictly lower than the candidate, an exact strictly-lower `--previous-deb` must already be supplied and valid;
- required tools;
- candidate input compatibility with any existing checkpoint;
- original-user and artifact path safety;
- existing checkpoint ownership/schema when present;
- exclusive lock;
- profile existence/validation;
- clean initial product/network boundary;
- required fault-injection seam availability for mandatory scenarios;
- remote-session restrictions for disruptive local-only actions;
- fixture/drop-in identities that must be free before later use, where this can be checked without creating them.

A permanent preflight failure before checkpoint creation leaves no run checkpoint and performs no cleanup because no harness mutation exists.

Runtime waits remain bounded and scenario-specific. Every poll uses a three-way classifier:

```text
TARGET_REACHED     -> return success immediately
PROGRESS_POSSIBLE  -> bounded wait/re-observe
TERMINAL_IMPOSSIBLE-> fail immediately with captured evidence
```

Unknown schema/contract states are not treated as `PROGRESS_POSSIBLE` until timeout; they fail quickly as an explicit contract/invariant failure.

Operator output on failure is concise and actionable, for example:

```text
FAILED_CLEAN: graceful_restart
Class: PRODUCT
Reason: protected session converged to terminal
Laptop restoration: verified
Evidence: <local private run directory>
Next: rerun the original release-laptop command
```

When cleanup cannot be proven:

```text
FAIL_CLEANUP_FAILED: fixture ownership ambiguous
Class: OWNERSHIP
Automatic cleanup stopped safely
Checkpoint retained
Evidence: <local private run directory>
```

For an intentional reboot boundary:

```text
PAUSED: reboot required
Next: sudo ./release-laptop.sh --resume
```

## Restart reconciliation

`--restart`, explicit `--abort`, automatic fresh-run retirement, and automatic failure cleanup share one reconciliation engine. They differ only in requested final action and result classification.

Order:

1. acquire/retain the global lock;
2. validate checkpoint schema/ownership and recorded package identity only as far as required for the next actual package operation;
3. reconcile transitional mutations read-only first;
4. stop only harness-owned observers/background jobs;
5. request normal Podlaz disconnect first when the acceptance run owns an active test session and the API is usable;
6. restore exact NetworkManager state if owned;
7. release exact fixtures and fault-injection hooks;
8. restore exact autostart policy and remove exact synthetic profile;
9. restore/install the recorded candidate only when package authority proves it is required and the artifact still exists;
10. verify ordinary networking and unresolved mutation ledger;
11. write the appropriate old-run failure/abort/restart report;
12. remove the old checkpoint only after cleanup verification;
13. for restart/fresh-run retirement only, start the new run using newly supplied inputs.

If the old candidate artifact disappeared but cleanup does not require reinstalling it, restart/abort/failure-cleanup must not fail merely because the file is gone. Artifact identity is required only before an operation that will actually read/install that artifact.

If package restoration does require the missing exact artifact, fail closed with `MANUAL_DIAGNOSIS`/`ABORT_ONLY` and retain the checkpoint.

## Default rerun behavior

When the user reruns the normal candidate command and a checkpoint exists:

- same candidate identity + replay-safe phase -> auto-resume;
- same or new candidate + old run already fully clean/retirable -> archive old run and start fresh;
- failed run whose automatic cleanup already completed -> start fresh immediately;
- non-replayable failed phase whose exact cleanup succeeds on this invocation -> start fresh;
- reboot-wait phase -> exit quickly and require explicit `--resume` or `--restart` to avoid accidental destruction of valuable reboot evidence;
- ambiguous state -> fail closed.

This makes the common operator path "run the same command again" useful without turning the harness into an unsafe auto-repair tool.

## Evidence lifecycle

Every run receives an immutable run ID and keeps its evidence directory even when restarted. The root checkpoint points only to the currently active run.

Retired/failed runs use explicit outcomes such as:

```text
FAILED_RESTARTABLE
FAILED_CLEAN
RESTARTED_CLEAN
ABORTED_CLEAN
FAIL_CLEANUP_FAILED
```

A failed/restarted old run can never become `PASS`; only a cleanly executed new run can later produce `QUALIFIED_PASS` or `PARTIAL_PASS`.

## Testing strategy

Use TDD and fake-host regression tests. At minimum cover:

1. candidate already installed + no lower package fails in preflight before checkpoint/mutation;
2. semantic active status accepts `connection=active`, `mode=tun`, committed transaction and verified health even when `tun` is formatted as `enabled (<interface>)`;
3. `revalidating -> verified` is treated as progress and returns promptly once verified;
4. a terminal/impossible status exits immediately instead of consuming the full timeout;
5. unknown/incompatible status schema fails fast with the payload captured;
6. preflight error creates no checkpoint/mutation and exits quickly;
7. unexpected post-checkpoint failure captures the failure bundle before any cleanup mutation;
8. unexpected failure with exactly-owned active session automatically disconnects, restores candidate/owned state, verifies ordinary network, and returns `FAILED_CLEAN`;
9. cleanup ambiguity returns `FAIL_CLEANUP_FAILED`, retains checkpoint, and performs no broad cleanup;
10. failure during the cleanup finalizer does not recursively start a second finalizer;
11. `SIGINT`/`SIGTERM` trigger the guarded failure evidence/cleanup path when safe;
12. expected reboot pause never auto-aborts;
13. same command after an interrupted replay-safe phase continues without duplicate mutation;
14. failure in a non-replayable scenario is classified restart-required or auto-cleaned according to proven authority;
15. normal candidate invocation automatically retires an exactly-cleanable failed run and starts a new run;
16. `--restart` performs exact cleanup and creates a new run ID;
17. reboot-wait phase is never auto-retired by an ordinary rerun;
18. ambiguous fixture/drop-in/NM/autostart state blocks restart and preserves checkpoint;
19. missing candidate `.deb` does not prevent abort/restart/failure cleanup when no package reinstall is required;
20. missing candidate `.deb` blocks cleanup when exact package restoration really requires it;
21. retry does not duplicate profile, route/rule/nft, drop-in or package mutations;
22. expected non-zero results from shell probes do not accidentally terminate the script through `set -e`/`pipefail`;
23. global lock prevents two concurrent restart/resume/new/finalizer operations;
24. last-failure classification and recommended next action are deterministic and sanitized;
25. private failure evidence includes bounded status/doctor/journal/package/ledger snapshots while public reports contain no host-private identifiers;
26. existing standalone safety/recovery/CI contracts remain green.

## Non-goals

- no broad `recover --execute` fallback;
- no route/rule/nft flush;
- no automatic deletion of ambiguous state;
- no automatic reboot;
- no automatic dependency repair or package download;
- no cleanup operation that upgrades a failed scenario to PASS;
- no weakening of privacy, collision, terminal, rollback or final-restoration qualification criteria;
- no Python runtime or repository checkout requirement.
