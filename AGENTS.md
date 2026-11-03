# AGENTS.md

This is the repository workflow and engineering policy for agents. Keep the working set task-specific; do not preload historical issue/spec/plan prose. Optimize for verified task outcomes, not number of changes or passing checks.

## Canonical knowledge surfaces

Read only what the task requires:

- `README.md` — repository entry point, build/package orientation, documentation map.
- `docs/cli.md` — public CLI commands, flags, outputs, lifecycle/status semantics.
- `ARCHITECTURE.md` — durable component, state, security, networking, recovery, package/runtime, and E2E invariants.
- this `AGENTS.md` — workflow and routing.

Then read the directly relevant code/tests/scripts. Code and executable tests are canonical for implementation detail. Git history/Issues are context, not permanent runtime documentation.

## Task routing

| Task | Start with | Then inspect |
| --- | --- | --- |
| CLI command/output/status | `docs/cli.md` | `cmd/podlaz/**`, CLI/client tests |
| daemon lifecycle/recovery/API | `ARCHITECTURE.md` | `internal/daemon/**`, recovery/session packages/tests |
| TUN/routes/DNS/firewall/privacy | `ARCHITECTURE.md` | relevant networking/recovery/core packages and tests |
| package/service/release | `README.md`, `ARCHITECTURE.md` | `packaging/**`, `scripts/**`, `.github/workflows/**` |
| E2E/acceptance | `ARCHITECTURE.md` | `scripts/e2e/**`, CI workflows, neighboring contract tests |
| documentation-only | the target canonical surface | executable source only to verify claims |

Do not recursively read every document/source directory before the task requires it.

## Required engineering rules

- Preserve behavior unless the task explicitly changes product semantics.
- Privileged network mutation remains fail-closed and exact-ownership-driven. Observation or historical resemblance is never cleanup authority.
- Preserve CLI/API/state-schema/package/service compatibility unless the issue explicitly changes it.
- Preserve error classification and lifecycle/recovery ordering; do not hide real defects with retries or presentation-only workarounds.
- Prefer small private helpers and explicit composition over new package layers when decomposing orchestration.
- Keep public APIs minimal. Do not create abstractions without at least two real semantic consumers or a clear domain boundary.
- Keep tests behavior-oriented. Permanent artifact/test names describe the protected invariant, not an issue number.
- Consolidate repeated E2E mechanics in `scripts/e2e/lib/**`; keep scenario-specific predicates/cleanup local when semantics differ.
- Do not add real user IPs, domains, SSIDs, profile IDs, credentials, subscriptions, or endpoint data to code, tests, fixtures, docs, Issues, PRs, or logs. Use RFC-reserved/example values.
- For external technologies, dependencies, tools, and APIs, consult current official documentation compatible with project versions. Do not upgrade without task-driven need; new/updated dependencies must be stable and compatible.

## Task authoring and delegation

These rules apply when drafting an Issue, implementation plan, or prompt for another agent. **Task authoring is a separate deliverable:** do not implement, create Issues/PRs, trigger workflows, or change the repository unless explicitly requested.

- Verify the current repository state and task-relevant canonical contracts, code/tests, Issues, PRs, and previous attempts before prescribing work. Reference the relevant issue/PR, base branch or known SHA, and evidence; distinguish verified facts from hypotheses and unknowns. Never invent current HEAD, CI results, blockers, or acceptance coverage.
- Check whether the problem is already solved, duplicated, superseded, or blocked. State why new work is necessary and how it relates to existing work. Do not prescribe repeating a disproven experiment or reopening deliberately closed scope without new grounds.
- Specify the **problem and desired observable outcome**, not an assumed implementation. Define a narrow boundary, explicit non-goals, dependencies/prerequisites, and product/security/compatibility constraints. Where architecture or feasibility is uncertain, assign an evidence-gathering spike with a decisive stop/go gate before production implementation.
- Make acceptance criteria independently verifiable: map each requirement to an observable behavior, relevant existing or missing test, real integration evidence where needed, and required failure/negative cases. Separate research evidence, component tests, full integrated acceptance, and post-merge/release validation. Never demand an impossible proof or call an unchecked assertion complete.
- Keep the task self-contained but concise: provide authoritative links, concrete known symptoms and repro steps, expected outcome, scope, acceptance evidence, and decisions reserved for the owner. Refer to this file for shared engineering/CI rules instead of copying them into every prompt.
- Avoid prescribing speculative code design, a chain of staged PRs, arbitrary timeouts, or a mandatory merge/release. Delegate implementation choices to the executor within established constraints. Flag decisions that materially alter security, compatibility, scope, or product behavior for explicit owner approval.
- Before handing off, check that the executor can identify a first meaningful verification step, the intended stopping conditions, and what to report if the hypothesis is false or the environment cannot supply the required evidence. A task that cannot yet be scoped safely should ask a specific owner question rather than manufacture certainty.

## Evidence-driven scope and investigation

- Before substantial changes, identify the observable problem, expected result, Definition of Done, and *missing* acceptance assertions. Check existing code/tests/evidence before creating more implementation or tests.
- Choose the smallest coherent path to those assertions. Do not expand product behavior, security policy, compatibility, or scope for a local defect without explicit justification and owner approval.
- Before introducing a service, broker, protocol, durable state, or abstraction, establish its real consumer, alternatives, trust/lifecycle risks, maintenance cost, and route to integrated acceptance. Uncalled primitives are not product readiness.
- For bugs, regressions, and recurring failures, inspect relevant Issues, PRs, CI evidence, and Git history. Do not revisit unrelated history or repeat a disproven approach without new grounds.
- Separate facts from hypotheses and unknowns. For nontrivial experiments, specify a falsifiable hypothesis, decisive evidence, and stop criteria. Test critical feasibility/security constraints early, preferably with a minimal real end-to-end scenario.
- If repeated changes leave the same blocker unresolved, reconsider assumptions, prior evidence, and alternatives before adding code. Preserve negative results rather than weakening acceptance or interpreting unrelated GREEN as a fix.
- A blocker for one approach does not automatically block the task. Explore safe in-scope alternatives; otherwise report the evidence and precise prerequisite to proceed. Ask the owner when material requirements, semantics, safety, or compatibility cannot be reliably determined.

## Development workflow

1. Start from the requested base and work in the requested feature branch/worktree.
2. Read the issue/plan plus only the routed canonical docs and directly relevant code/tests.
3. For a bug/behavior change, write or identify the failing test first. For pure refactors, establish passing characterization tests before movement.
4. Implement the smallest coherent change. Keep product behavior and failure ordering explicit.
5. Format and run the narrow tests while iterating. Prefer available local compiler, lint, and focused tests before expensive full GitHub Actions matrices; batch coherent changes instead of pushing every small edit.
6. Before completion run repository-level verification appropriate to the touched surface:

```bash
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
govulncheck ./...
bash scripts/ci/repository-structure.sh --final
```

Also run repository shell/workflow/package checks, race checks for concurrency-sensitive code (at least daemon when daemon lifecycle changes), and the relevant E2E suites. Destructive real-host E2E runs only on the dedicated runner and only when the change requires host mutation coverage.

7. Review the diff for scope, dead references, stale names, docs drift, test quality, security/privacy, and rollback/recovery semantics.
8. Open/update one PR with acceptance-criterion evidence and explicit validation results. Do not claim a check passed without fresh evidence.

## CI failure triage and iteration

- Inspect the exact failed job, logs, steps, and available artifacts. Distinguish demonstrated product, test/harness, and infrastructure causes from unknowns. Fix established causes; do not hide RED through blind reruns, longer timeouts, or weakened assertions.
- Run the expensive full matrix on an intentional candidate, not as an editor/compiler. Avoid superseding useful in-progress evidence with cosmetic pushes. When a deterministic defect invalidates a candidate, a corrected revision may supersede it with an explicit cause.
- If local checkout, toolchain, or guest execution is unavailable, disclose the limitation and use supported focused alternatives. Do not compensate with repeated speculative CI pushes or claim an unexecuted check passed.
- Validate applicable checks for the current source HEAD and, where relevant, the tested PR merge commit; previous-SHA GREEN does not qualify new code. Continue checking queued/pending/in-progress mandatory runs when technically possible.
- Regression GREEN for unchanged behavior, a PoC, or a skipped assertion is not proof of new production functionality. Require direct integrated/runtime evidence for new acceptance claims.

## Repository simplification guardrails

Permanent product prose is intentionally limited to `README.md`, `docs/cli.md`, `ARCHITECTURE.md`, and `AGENTS.md`. GitHub-native community-health files under `.github/` are allowed only for repository governance, contribution, security, accessibility, and issue/PR intake. Other new prose needs a durable ownership reason that cannot fit one of those surfaces.

Permanent source/test/workflow artifact names must be domain/invariant-oriented. Do not introduce `issueNNN`/`IssueNNN` names or permanent issue-number labels. Temporary plan/spec files may exist while a plan is actively being executed; `repository-structure.sh --final` requires them to be removed before completion.

When duplication is found, consolidate mechanics only after comparing semantics. When deleting code/fixtures, record proof: no references/runtime registration, a superseding equivalent implementation, or test-backed merge.

## Pull requests and review

PRs target the requested base branch and should contain one coherent goal. The body must state what changed, what behavior is intentionally preserved, relevant migration maps/evidence, and exact validation performed/not performed.

Review comments belong on the specific changed line whenever a line-level finding exists. Prioritize correctness, security/privacy, recovery/rollback, concurrency/order, compatibility, and test gaps over stylistic preference.

Keep research, partial production stages, and integrated acceptance visibly distinct. Stacked PRs require a justified dependency order and combined validation plan. Preserve useful negative findings, and close/supersede obsolete PRs rather than leaving indefinite research branches. Post material evidence/decisions with exact SHAs, runs and artifacts instead of repetitive progress comments.

## Completion and authorization

- **Completed:** all applicable DoD assertions are proven and required current-revision checks have terminal successful results.
- **In progress:** feasible work, missing proof, or mandatory running checks remain. Do not call partial GREEN complete.
- **Blocked:** a specific evidenced technical/platform constraint or owner decision prevents safe progress; document attempted paths, unknowns, and the condition for resuming. Complexity or a failed attempt alone is not a blocker.
- Continue toward the original DoD while an evidence-based path exists. Do not substitute endless speculative implementation or a research-only PoC for the requested product result.
- Never merge or release without explicit owner authorization.
