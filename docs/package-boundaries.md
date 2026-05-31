# Package Boundaries

This document defines dependency direction for TunWarden packages.

## 1. Goal

The codebase should keep these concerns separate:

- command-line parsing and rendering;
- daemon orchestration;
- API request and response contracts;
- profile and subscription domain models;
- planning logic;
- system integration adapters.

This separation keeps behavior easier to review and test.

## 2. Preferred dependency direction

```text
cmd/tunwarden
  -> internal/app/cli
  -> internal/client
  -> internal/api
  -> internal/render

cmd/tunwardend
  -> internal/app/daemon
  -> internal/api
  -> internal/daemon
  -> internal/service
  -> internal/engine
  -> internal/network/planner
  -> internal/network/executor
  -> internal/profile
  -> internal/sub
  -> internal/state
```

The exact names may evolve, but the direction should remain stable.

## 3. Domain packages

Domain packages include profile parsing, subscription parsing, normalized profile models, engine config models, API contracts, and network planning models.

Expected properties:

- deterministic behavior;
- simple unit tests;
- fixture-based tests where useful;
- no dependency on CLI rendering;
- no dependency on daemon orchestration.

## 4. CLI packages

CLI packages should:

- parse user input;
- call client or local read-only diagnostic abstractions;
- render output;
- keep command behavior aligned with `docs/cli.md`.

## 5. Daemon packages

Daemon packages should:

- validate requests;
- own runtime state;
- coordinate engine lifecycle;
- call planners and system integration adapters;
- expose a small local API.

## 6. Planner packages

Planner packages should:

- build inspectable plans from input snapshots;
- return warnings and ordered steps;
- stay testable with fake snapshots;
- avoid depending on executors.

## 7. Executor and adapter packages

Executor and adapter packages should:

- keep low-level platform integration narrow;
- return explicit results for diagnostics;
- be easy to audit in pull requests;
- avoid hidden planning decisions.

## 8. Review rule

A pull request that changes dependency direction should explain why the new direction is needed and why it does not weaken the CLI/daemon boundary or planner/executor split.
