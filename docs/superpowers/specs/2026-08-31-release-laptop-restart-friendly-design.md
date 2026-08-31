# Release laptop restart-friendly state machine design

Status: proposed for user review.

## Goal

Make the standalone `release-laptop.sh` resilient to ordinary operator mistakes, transient host conditions, and interrupted runs without weakening its fail-closed ownership rules.

The operator should be able to rerun the same one-file harness after a failure and get a deterministic outcome:

- continue safely when the last boundary is replay-safe;
- cleanly retire the previous run and start over when exact owned state can be proven and restored;
- refuse to guess when live state is ambiguous;
- fail quickly for permanent/preflight errors instead of waiting through long scenario timeouts.

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
```

No secret, endpoint, IP, interface, profile ID, or host identifier is written to shareable output.

Failure classes:

- `INPUT`: bad CLI, missing/changed package, invalid profile, unsafe artifact path;
- `HOST_CAPABILITY`: unsupported optional Wi-Fi/suspend/cgroup feature;
- `HOST_STATE`: required host state is currently incompatible but no harness-owned mutation is lost;
- `PRODUCT`: Podlaz behavior did not satisfy the acceptance contract;
- `OWNERSHIP`: exact harness authority is ambiguous or foreign state occupies a recorded identity;
- `INTERRUPTED`: shell/process/power interruption detected at a persisted boundary;
- `INTERNAL`: invariant/parser/script defect.

Retry policies:

- `RETRY`: rerunning the same command can safely re-attempt the current replay-safe step;
- `RESTART`: current qualification evidence is invalid for continuation, but exact cleanup permits a fresh run;
- `RESUME_AFTER_REBOOT`: intentional reboot phase;
- `ABORT_ONLY`: cleanup/restoration is allowed but continuing or restarting automatically is not;
- `MANUAL_DIAGNOSIS`: ownership/evidence is ambiguous; no automated mutation follows.

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
- `failed`: preserve failure evidence; only restart after exact cleanup.

The three intentional reboot phases remain special persisted boundaries and require a real new `boot_id` for `--resume`.

## Fast-fail behavior

Fast failure does not mean shortening legitimate product convergence windows. It means proving permanent errors before entering them.

Before the first disruptive mutation, one consolidated preflight checks:

- candidate/previous package existence, identity, architecture, checksum and Debian ordering;
- required tools;
- candidate input compatibility with any existing checkpoint;
- original-user and artifact path safety;
- checkpoint ownership/schema;
- exclusive lock;
- profile existence/validation;
- clean initial product/network boundary;
- required fault-injection seam availability for mandatory scenarios;
- remote-session restrictions for disruptive local-only actions;
- fixture/drop-in identities that must be free before later use, where this can be checked without creating them.

Permanent failures return immediately with no long network wait.

Runtime waits remain bounded and scenario-specific. Poll loops should stop early when a terminal state proves success is impossible instead of always consuming the full timeout.

Operator output on failure is concise and actionable, for example:

```text
FAILED: graceful_restart
Class: PRODUCT
Reason: daemon restarted but protected session converged to terminal
Next: sudo ./release-laptop.sh ./candidate.deb --restart --profile <profile-id>
Evidence: <local private run directory>
```

For an intentional reboot boundary:

```text
PAUSED: reboot required
Next: sudo ./release-laptop.sh --resume
```

## Restart reconciliation

`--restart` and automatic fresh-run retirement use the same cleanup engine as `--abort`, but with a different final action.

Order:

1. acquire the global lock;
2. validate checkpoint schema/ownership and recorded candidate identity as far as required for cleanup;
3. reconcile transitional mutations read-only first;
4. stop only harness-owned observers/background jobs;
5. request normal Podlaz disconnect only when the acceptance run owns an active test session and the candidate API is usable;
6. restore exact NetworkManager state if owned;
7. release exact fixtures and fault-injection hooks;
8. restore exact autostart policy and remove exact synthetic profile;
9. restore/install the recorded candidate only when package authority proves it is required and the artifact still exists;
10. verify ordinary networking and unresolved mutation ledger;
11. write a sanitized `RESTARTED_CLEAN`/old-run failure report;
12. remove the old checkpoint only after cleanup verification;
13. start the new run using the newly supplied candidate/profile/options.

If the old candidate artifact disappeared but cleanup does not require reinstalling it, restart/abort must not fail merely because the file is gone. Artifact identity is required only before an operation that will actually read/install that artifact. This keeps recovery possible after an operator moves or deletes an already-installed `.deb`.

If package restoration does require the missing exact artifact, fail closed with `MANUAL_DIAGNOSIS`/`ABORT_ONLY` and retain the checkpoint.

## Default rerun behavior

When the user reruns the normal candidate command and a checkpoint exists:

- same candidate identity + replay-safe phase -> auto-resume;
- same or new candidate + old run already fully clean/retirable -> archive old run and start fresh;
- non-replayable failed phase but exact cleanup succeeds -> start fresh;
- reboot-wait phase -> exit quickly and require explicit `--resume` or `--restart` to avoid accidental destruction of valuable reboot evidence;
- ambiguous state -> fail closed.

This makes the common operator path "run the same command again" useful without turning the harness into an unsafe auto-repair tool.

## Evidence lifecycle

Every run receives an immutable run ID and keeps its evidence directory even when restarted. The root checkpoint points only to the currently active run.

Retired runs get one of:

```text
FAILED_RESTARTABLE
RESTARTED_CLEAN
ABORTED_CLEAN
FAIL_CLEANUP_FAILED
```

A restarted old run can never become `PASS`; only the newly created run can later produce `QUALIFIED_PASS`/`PARTIAL_PASS`/`FAIL`.

## Testing strategy

Use TDD and fake-host regression tests. At minimum cover:

1. preflight error creates no checkpoint/mutation and exits quickly;
2. same command after an interrupted replay-safe phase continues without duplicate mutation;
3. failure in a non-replayable scenario is classified restart-required;
4. normal candidate invocation automatically retires an exactly-cleanable failed run and starts a new run;
5. `--restart` performs exact cleanup and creates a new run ID;
6. reboot-wait phase is never auto-retired by an ordinary rerun;
7. ambiguous fixture/drop-in/NM/autostart state blocks restart and preserves checkpoint;
8. missing candidate `.deb` does not prevent abort/restart when no package reinstall is required;
9. missing candidate `.deb` blocks cleanup when exact package restoration really requires it;
10. retry does not duplicate profile, route/rule/nft, drop-in or package mutations;
11. global lock prevents two concurrent restart/resume/new operations;
12. last-failure classification and recommended next action are deterministic and sanitized;
13. existing standalone safety/recovery/CI contracts remain green.

## Non-goals

- no broad `recover --execute` fallback;
- no route/rule/nft flush;
- no automatic deletion of ambiguous state;
- no automatic reboot;
- no automatic dependency repair or package download;
- no weakening of privacy, collision, terminal, rollback or final-restoration qualification criteria;
- no Python runtime or repository checkout requirement.
