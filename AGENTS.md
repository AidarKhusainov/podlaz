# AGENTS.md

This file is the repository workflow/router. Do not load historical issue/spec/plan prose by default. Keep the working set task-specific.

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

## Development workflow

1. Start from the requested base and work in the requested feature branch/worktree.
2. Read the issue/plan plus only the routed canonical docs and directly relevant code/tests.
3. For a bug/behavior change, write or identify the failing test first. For pure refactors, establish passing characterization tests before movement.
4. Implement the smallest coherent change. Keep product behavior and failure ordering explicit.
5. Format and run the narrow tests while iterating.
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

## Repository simplification guardrails

Permanent prose is intentionally limited to `README.md`, `docs/cli.md`, `ARCHITECTURE.md`, and `AGENTS.md`. New prose needs a durable ownership reason that cannot fit one of those surfaces.

Permanent source/test/workflow artifact names must be domain/invariant-oriented. Do not introduce `issueNNN`/`IssueNNN` names or permanent issue-number labels. Temporary plan/spec files may exist while a plan is actively being executed; `repository-structure.sh --final` requires them to be removed before completion.

When duplication is found, consolidate mechanics only after comparing semantics. When deleting code/fixtures, record proof: no references/runtime registration, a superseding equivalent implementation, or test-backed merge.

## Pull requests and review

PRs target the requested base branch and should contain one coherent goal. The body must state what changed, what behavior is intentionally preserved, relevant migration maps/evidence, and exact validation performed/not performed.

Review comments belong on the specific changed line whenever a line-level finding exists. Prioritize correctness, security/privacy, recovery/rollback, concurrency/order, compatibility, and test gaps over stylistic preference.
