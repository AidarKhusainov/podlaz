# CI/CD simplification design

## Goal

Make Podlaz CI/CD small, explicit, and runnable with the infrastructure that actually exists today.

The target has three active GitHub Actions workflows with distinct responsibilities:

- `ci.yml` answers whether a change is safe to merge.
- `integration.yml` answers whether the merged product works as an installed package on GitHub-hosted Linux.
- `release.yml` answers whether one exact tag SHA is safe to publish.

No active workflow may depend on the absent `vpn-e2e` self-hosted runner.

## Constraints and invariants

- Preserve product behavior, package/service compatibility, lifecycle ordering, recovery semantics, network ownership, and privacy guarantees.
- `proxy-only` integration must not mutate host networking.
- Destructive TUN, route, rule, DNS, nftables, NetworkManager, suspend/resume, recovery, and coexistence scenarios must not be moved to GitHub-hosted runners unless an equivalent isolated environment is proven.
- Tests must be behavior-oriented. Tests that only assert workflow wiring, helper names, literal implementation shape, or historical issue coverage are not permanent product tests.
- Permanent names describe domains and invariants, never issue numbers.
- Workflow YAML is orchestration. Reusable verification mechanics stay in `scripts/ci/**` or `scripts/e2e/**`.
- Do not add a new workflow merely because a new test exists. A separate workflow requires a separate trigger, security/permission boundary, runner/environment boundary, lifecycle, or materially different execution policy.
- External Actions are pinned by full commit SHA and kept updateable through the existing Dependabot `github-actions` configuration.
- Workflow permissions remain explicit and least-privilege.
- CI fixtures, logs, PR text, and tests use only reserved/example network data.

## Alternatives considered

### One large workflow

Rejected. It minimizes workflow count but mixes PR gating, secret-bearing integration, release publication, and different failure/latency policies in one file. The result would be harder to reason about and easier to misconfigure.

### Four active workflows including `qualification.yml`

Rejected for now. Qualification is a valid architectural tier, but Podlaz currently has no dedicated runner capable of safely executing destructive host-network scenarios. An active workflow with no valid execution environment would be dead configuration.

### Three active workflows

Selected. It is the smallest structure that preserves the real security and lifecycle boundaries that exist today.

## Workflow architecture

### `ci.yml`: deterministic merge gate

Triggers:

- pull requests;
- pushes to `master`.

The obsolete `foundation` push trigger is removed.

CI runs only checks that are deterministic, do not require private VPN credentials, and are suitable for GitHub-hosted runners:

- workflow and shell lint;
- repository structure checks;
- formatting;
- `go test ./...`;
- daemon race tests;
- `go vet ./...`;
- `govulncheck ./...`;
- CLI contract tests;
- generated shell-completion smoke checks;
- Debian package build and metadata/content validation;
- package install, reinstall, service validation, and purge.

PR concurrency cancels superseded runs. Master runs may cancel superseded runs because only the latest mainline state is relevant.

`cli-contract.sh` moves into this deterministic layer because its behavior is local and fixture-driven; it does not require a privileged network lifecycle.

The separate `boot-continuation-contract.sh` CI step/script is removed if, after the wiring cleanup, it contains no behavior not already executed by `go test ./...` and existing CI guards.

### `integration.yml`: installed-product integration

Runs on explicit `ubuntu-24.04` GitHub-hosted runners.

It has two execution classes inside one workflow because they share the same product-integration responsibility but have different external-dependency policies.

#### Deterministic installed-package integration

Triggers:

- every push to `master`;
- manual dispatch for diagnosis.

Contains focused installed-product scenarios that do not require real host-network mutation or a real external VPN provider. Expected coverage includes:

- package/service lifecycle beyond static package validation;
- systemd behavior;
- ordinary-user client access and authorization boundaries;
- journald/log-window behavior;
- focused proxy/control-plane lifecycle that can use a deterministic test Xray fixture.

`remote-client-acceptance.sh` should be made runner-agnostic and provider-independent where possible. Its core invariant is ordinary-user behavior without private service-group membership, not reachability of a real VPN endpoint.

`package-service.sh` is the primary starting point for deterministic installed-package integration. Duplicate package setup/cleanup mechanics should be consolidated only where semantics are genuinely identical.

#### Real-provider proxy data plane

Triggers:

- scheduled daily run from `master`;
- manual dispatch restricted to `master`;
- exact-tag qualification from `release.yml`.

This class uses a protected GitHub Environment and environment/repository secrets. It must never execute for pull-request code or arbitrary branch code.

The existing GitHub Environment named `vpn-e2e` may remain temporarily as the secret/approval boundary even after the self-hosted runner is retired. The runner label and the Environment are different concepts. If the Environment is renamed, its secrets/variables must be migrated explicitly; the implementation must not assume that renaming repository YAML migrates GitHub Environment state.

This class verifies real proxy behavior through a real configured profile, including SOCKS/HTTP data plane, loopback bind scope, egress, lifecycle cleanup, and bounded reliability checks.

It is not run on every `master` push because external provider/network health is nondeterministic and should not turn normal mainline feedback into noise.

## Release architecture

`release.yml` remains a separate tag-triggered publication pipeline.

It validates and publishes the exact tagged SHA. Release-critical evidence is rerun against that tag rather than inferred from an earlier `master` run.

Required release stages:

1. validate `vMAJOR.MINOR.PATCH`;
2. check out the exact tag;
3. run deterministic Go verification;
4. build and validate release packages;
5. install/reinstall/service-validate/purge the package;
6. run deterministic installed-package integration required for release confidence;
7. run real-provider proxy data-plane qualification for the exact tag;
8. create release artifacts and checksums;
9. attest artifact provenance;
10. publish immutably.

Release does not repeat unrelated integration work merely for symmetry. Only release-critical checks are repeated on the exact tag SHA.

Release concurrency never cancels an in-progress publication attempt.

## Runner baseline

Hosted jobs migrate to explicit `ubuntu-24.04` where practical. `ubuntu-22.04` is removed from active CI/release configuration as part of this cleanup.

Do not use nested virtualization as the basis for required destructive TUN qualification. It does not replace a dedicated, controlled host-network environment.

## Self-hosted infrastructure removal

The following active workflow layer is removed because there is no current execution environment for it:

- `.github/workflows/e2e.yml`;
- `.github/workflows/e2e-tun-package-convergence.yml`;
- `.github/workflows/e2e-tun-resource-soak.yml`.

Related configuration that exists only for the absent runner is removed, including `.github/actionlint.yaml` if it contains no configuration other than the `vpn-e2e` runner label, and `scripts/ops/bootstrap-e2e-runner.sh` if no other supported workflow consumes it.

Removing these workflows also requires removing or rewriting tests that assert their existence or exact wiring. Product tests must protect behavior, not a retired orchestration file.

## E2E harness disposition

### Delete

Delete artifacts that are stale, meta-level, duplicated, or tied only to retired orchestration:

- `scripts/e2e/coverage-evidence.sh`;
- legacy `real-vpn.sh` and `real-vpn-extended.sh` when reference analysis confirms they are superseded;
- workflow-binding tests whose only contract is that a retired workflow invokes a script;
- implementation-shape meta-tests that assert helper names, exact call placement, or literal timeout values without testing behavior;
- issue-numbered permanent test identifiers;
- dormant TUN soak subsystem when its files are used only by the retired soak workflow and do not provide independent deterministic value.

Deletion requires repository-backed proof: no remaining runtime registration/reference, a superseding implementation, or behavior-preserving test coverage.

### Keep

Keep focused destructive-network harnesses that encode safety-critical executable specifications even though they are not currently automated, including invariants around:

- exact network-resource ownership;
- legacy package/network-session recovery;
- Privacy Envelope lifecycle;
- coexistence with foreign host state;
- rollback/failure convergence;
- stale-link/recovery safety;
- protected-gateway behavior;
- boot/session continuation.

These scripts must not claim current GitHub Actions coverage. Their comments and shared helpers should be made runner-agnostic where the semantics do not depend on a specific self-hosted runner.

### `server-coverage.sh`

Do not preserve this catch-all scenario by default.

Before deletion, map each unique behavior it currently exercises against existing deterministic tests, `data-plane.sh`, package/service integration, and focused TUN acceptance scenarios. If a genuinely unique invariant remains, extract only that invariant into a focused domain-named scenario. Then delete the catch-all script and any meta-tests that exist only to preserve its structure.

## Test cleanup rules

During implementation, classify tests in `scripts/e2e/**` into three groups:

1. behavioral tests that execute a helper/scenario and assert observable results — keep;
2. structural guards that enforce a durable repository invariant — keep only when the invariant itself is intentional and stable;
3. source-text/meta-tests that preserve incidental implementation structure or retired workflow wiring — delete or rewrite behaviorally.

`go test ./...` remains the canonical umbrella Go test invocation. Do not retain secondary CI scripts that only rerun subsets without adding a distinct boundary or environment.

## Security model

- PR CI never receives VPN/provider secrets.
- Real-provider integration is limited to `master` or an exact release tag and a protected Environment.
- Workflows default to `contents: read` or less; publication jobs receive write/OIDC permissions only where required.
- Third-party and GitHub Actions are pinned to immutable full commit SHAs.
- Checkout credentials are not persisted when later authenticated Git operations are unnecessary.
- Diagnostic artifacts remain bounded, short-lived, and redaction-safe.
- No destructive host-network test runs on a general GitHub-hosted runner merely because `sudo` is available.

## Migration sequence

1. Establish characterization/reference evidence for files proposed for deletion.
2. Simplify deterministic CI and move CLI contract coverage into it.
3. Add `integration.yml` with deterministic hosted package/runtime scenarios.
4. Make ordinary-user integration provider-independent where feasible.
5. Add scheduled/master-only manual real-provider proxy data-plane execution with protected secrets.
6. Update `release.yml` to `ubuntu-24.04`, exact-tag integration, immutable Action pins, and least privilege.
7. Remove retired self-hosted workflows and runner-only configuration.
8. Remove stale/meta E2E scripts and tests, including the soak subsystem when reference proof permits.
9. Audit `server-coverage.sh`; extract only unique behavior, then remove the catch-all.
10. Remove stale references/comments/names and run repository-wide verification.

## Verification

At minimum the implementation must pass:

```bash
test -z "$(gofmt -l .)"
go test ./...
go vet ./...
govulncheck ./...
bash scripts/ci/repository-structure.sh --final
```

Also required:

- actionlint/workflow lint;
- shellcheck and E2E helper tests that remain applicable;
- package build/metadata/install/reinstall/purge checks;
- fresh `ci.yml` execution on the PR;
- fresh deterministic hosted integration execution before merge, using a temporary trusted branch trigger or equivalent test run if the new workflow cannot yet be dispatched from the default branch;
- fresh real-provider proxy integration where credentials are available, only from `master` or an exact release tag;
- release workflow syntax/contract checks;
- diff review for dead references, stale self-hosted assumptions, duplicate verification, permissions, secret exposure, and accidental TUN mutation on hosted jobs.

Destructive TUN acceptance is explicitly recorded as not executed unless a safe dedicated environment exists.

## Success criteria

The change is complete when:

- exactly three active product workflows remain: `ci.yml`, `integration.yml`, `release.yml`;
- no active workflow uses `self-hosted` or the `vpn-e2e` runner label;
- any retained `vpn-e2e` GitHub Environment reference is clearly a secret/approval boundary, not a runner dependency;
- PR CI is deterministic and secret-free;
- master integration distinguishes deterministic installed-product checks from scheduled real-provider checks;
- release validates the exact tag SHA before publication;
- no active job uses Ubuntu 22.04;
- no permanent issue-numbered E2E/test artifacts remain in the touched area;
- no test exists solely to preserve deleted workflow wiring or incidental shell implementation shape;
- duplicated CI invocations are removed;
- critical destructive-network executable specifications that remain are honest about being non-automated;
- repository-wide verification is green.

## Non-goals

- Do not create a replacement self-hosted runner in this change.
- Do not emulate destructive TUN qualification with unsupported nested virtualization.
- Do not change VPN lifecycle/network semantics to make tests easier to run.
- Do not introduce reusable-workflow layers unless implementation reveals real workflow-level duplication with at least two semantic consumers.
- Do not silently rename or recreate the current GitHub Environment without an explicit secret/variable migration path.
- Do not add permanent CI/CD prose outside existing canonical documentation; this spec is temporary and must be removed before final repository completion per `repository-structure.sh --final`.
