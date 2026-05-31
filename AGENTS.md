# AGENTS.md

This file defines how AI agents and automated coding assistants should work in this repository.

TunWarden is a Linux-first, CLI-first VPN/proxy client for Xray-compatible configurations. The project values safe networking behavior, clear architecture, small reliable steps, and reviewable code more than fast feature accumulation.

## Agent role

Act like a careful senior engineer working through a pull request.

Default posture:

- prefer correctness over speed;
- prefer small, reviewable changes over broad rewrites;
- prefer explicit plans, tests, and failure handling over clever shortcuts;
- preserve user trust by being honest about uncertainty;
- keep the product safe for real Linux systems.

Do not treat generated code as final until it is reviewed against the repository contracts.

## Canonical project context

Read these documents before changing behavior:

- `README.md` for current project status and product principles;
- `docs/README.md` for the documentation map;
- `docs/cli.md` for command names, flags, exit codes, JSON behavior, and CLI safety semantics;
- `docs/architecture.md` for CLI/daemon boundaries and transaction model;
- `docs/state-and-security.md` for state ownership, redaction, confirmation behavior, systemd hardening, and core process safety;
- `docs/package-boundaries.md` for package dependency direction;
- `docs/networking-reliability.md` for TUN, routing, DNS, firewall, sleep/resume, and recovery requirements;
- `docs/subscriptions-and-profiles.md` for profile and subscription behavior;
- `docs/development.md` for local checks and contribution rules;
- `docs/roadmap.md` for sequencing.

If a document conflicts with code, do not silently choose one. Explain the mismatch in the PR and either update the code or update the documentation in the same change.

## Non-negotiable engineering rules

- Do not add route, rule, DNS, nftables, TUN, or process mutations without an explicit plan, verification path, and rollback or recovery path.
- Do not add SUID binaries.
- Do not write directly to `/etc/resolv.conf` in normal operation.
- Do not hide networking changes behind vague helper names.
- Do not print secrets in human output, JSON output, logs, errors, or test fixtures.
- Do not broaden daemon privileges without documenting the reason.
- Do not make the CLI mutate privileged networking directly.
- Do not introduce GUI assumptions into the core product path.
- Do not add convenience features before the reliability foundation they depend on exists.

## CLI and naming rules

`recover` is the canonical recovery command. Do not reintroduce old recovery names.

Public CLI behavior must follow `docs/cli.md`:

- command names and flags must match the contract;
- high-impact flags such as `--execute` and `--yes` must stay long-only;
- default recovery behavior must be read-only until the documented milestone enables explicit cleanup;
- `--json` output must include `schema_version` once JSON output is implemented;
- errors must go to stderr and stable output must go to stdout when the implementation supports that separation.

Use user-task language for public commands. Keep implementation details inside internal packages unless they are real user-facing concepts.

## Architecture rules

Preserve these boundaries:

- CLI parses input, renders output, manages user-owned intent/state, and submits requests.
- Daemon owns privileged runtime behavior and active connection state.
- Planners build inspectable plans without requiring root.
- Executors apply already-validated plans and remain narrow, explicit, and auditable.
- Core engines are child processes, not owners of TunWarden system state.

Package dependency direction must follow `docs/package-boundaries.md`. A PR that changes dependency direction must explain why.

## State and security rules

Keep these state categories separate:

- user intent/state: profiles, subscriptions, preferences, selected defaults;
- daemon runtime/state: active connection snapshot, locks, generated runtime config, process state, transaction state;
- system networking state: TUN interfaces, routes, policy rules, DNS link config, nftables state.

Generated core configs are runtime output, not persistent source of truth. Write them atomically, keep permissions restrictive, and do not log them in full.

All status, diagnostics, plans, recovery output, JSON output, and logs must share the same redaction policy.

## Testing and validation

Before proposing a PR, run the relevant checks when possible:

```bash
gofmt -w .
go test ./...
go run ./cmd/tunwarden version
go run ./cmd/tunwarden doctor
go run ./cmd/tunwarden recover
```

For code touching planning, state, CLI output, or security, add or update tests. Prefer deterministic unit tests and fixtures. Do not rely on root-only integration tests for basic correctness.

If checks cannot be run, state that clearly in the PR body with the reason.

## Pull request expectations

Each PR should produce a real product or codebase improvement. Avoid documentation-only changes unless the documentation is the product of that task or the docs must change to keep the contract accurate.

A good PR should include:

- a concise summary of what changed;
- why the change is needed now;
- user-visible behavior changes, if any;
- safety and rollback implications;
- tests or checks run;
- documentation updated in the same PR when behavior changes.

Keep PRs narrow. If a task reveals a larger design issue, document it and open a follow-up issue instead of mixing unrelated work into the same PR.

## Agent behavior rules

- Inspect existing code and docs before editing.
- Prefer extending existing patterns over inventing new ones.
- Keep diffs minimal and easy to review.
- Ask for clarification when product direction is genuinely ambiguous.
- If the requested change is unsafe, explain the risk and propose a safer path.
- Do not guess about Linux networking behavior when the repository already defines a requirement.
- Do not silently weaken safety requirements to make implementation easier.
- Leave the repository cleaner than you found it.
