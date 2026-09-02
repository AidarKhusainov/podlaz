# Repository simplification baseline

Captured from `master` at `cf19b5176bf39f912135a71814746f5a2eef4fca` before cleanup. This is temporary migration evidence and must not survive the final tree.

## Measurement method

File counts and bytes are measured from tracked files (`git ls-files` + `wc -c`). Context estimates use `ceil(bytes/4)` as a deliberately coarse, tokenizer-independent approximation; they are comparative only.

Reproduction commands:

```bash
find . -type f \( -name '*.md' -o -path './docs/man/*' \) -print0 | sort -z | xargs -0 wc -c
find docs/superpowers/specs -type f -name '*.md' -print0 | sort -z | xargs -0 wc -c
find docs/superpowers/plans -type f -name '*.md' -print0 | sort -z | xargs -0 wc -c
git grep -nE 'issue[0-9]+|Issue[0-9]+' -- ':!docs/superpowers/**'
```

The tracked prose inventory contains root `README.md`/`AGENTS.md`, `docs/*.md`, `docs/man/**`, and historical `docs/superpowers/{specs,plans}/**`. The final target is exactly four permanent prose surfaces: `README.md`, `docs/cli.md`, `ARCHITECTURE.md`, and `AGENTS.md`.

Baseline summary (the active simplification spec/plan are included because they are present on the execution branch but are explicitly temporary):

```text
permanent_prose_file_count=29
permanent_prose_bytes=338846
superpowers_spec_count=6
superpowers_spec_bytes=127375
superpowers_plan_count=6
superpowers_plan_bytes=136567
```

## Documentation migration map

| current surface | category | destination / action |
| --- | --- | --- |
| `README.md` | public entry point | keep; make canonical navigation/build/release entry |
| `docs/cli.md` | public contract | keep; own CLI, outputs, status/state semantics |
| `AGENTS.md` | agent workflow | keep; reduce to workflow + router |
| `docs/daemon-api.md` | engineering invariant | distill to `ARCHITECTURE.md` |
| `docs/state-and-security.md` | engineering invariant | distill to `ARCHITECTURE.md` |
| `docs/packaged-tun-runtime.md` | engineering invariant | distill to `ARCHITECTURE.md` and `README.md` |
| `docs/debian-package.md` | build/package contract | distill to `README.md` |
| `docs/development.md` | contributor workflow | distill to `README.md`/`AGENTS.md` |
| `docs/release.md` | release workflow | distill to `README.md`/`AGENTS.md` |
| `docs/e2e.md`, `docs/e2e-proxy-data-plane.md` | test procedure | distill invariants to `ARCHITECTURE.md`; executable scripts stay canonical |
| provider/TUN/manual topic docs | overlapping implementation notes | distill durable contract to `docs/cli.md` or `ARCHITECTURE.md`, then delete |
| `docs/man/**` | generated/installed reference | source from `docs/cli.md`; remove repository prose copies |
| completed `docs/superpowers/**` | historical/process artifact | delete after durable invariants are distilled |
| active simplification design/plan/baseline | temporary process artifact | delete in final cleanup |

## Issue-oriented artifact migration inventory

Names are chosen from the invariant under test, not issue titles. Callers include `.github/workflows/e2e.yml`, supply-chain checks, repository-cost guards, and neighboring Go contract tests.

| old artifact family | protected invariant | proposed domain name | action |
| --- | --- | --- | --- |
| `issue241-*` | installed-package failure/diagnostic contract | `package-failure-*` | rename |
| `issue243-*` | protected gateway/default TUN package behavior | `protected-gateway-*` | rename |
| `issue247-*` | stale-link cleanup, cleanup trap, bounded log evidence | `stale-link-*` / `log-window-*` | rename and update callers |
| `issue254-*` | remote-client/browser-login persistence | `remote-client-*` | rename |
| `issue256-*` | package lifecycle/network acceptance | `package-lifecycle-*` | rename |
| `issue259-*` | recovery/resume package acceptance | `network-recovery-*` | rename |
| `issue260-*` | privacy-envelope/session acceptance | `session-privacy-*` | rename |
| `issue261-*` | collision-free network resources | `network-resource-isolation-*` | rename |
| `issue262-*` | reconciliation/host-churn acceptance | `network-reconciliation-*` | rename |
| `issue263-*` + `lib/issue263*` | restart-safe boot continuation lifecycle | `boot-continuation-*` | rename and consolidate shared helpers |
| `issue270*` | missing persisted TUN state | `missing-tun-state-*` | rename |
| `issue271*` | multiple persisted TUN sources | `multi-source-tun-*` | rename |
| `issue275*` | provider-scoped identical tags | `provider-scoped-tag-*` | rename |
| `issue277*` | TUN fallback metadata | `tun-fallback-metadata-*` | rename |
| `issue279*` | archive/encoding/deep-path supply-chain cases | encoding/archive invariant names | rename active fixtures; delete superseded archive-only duplicates only with reference proof |
| `issue284*` | normalized TUN connect representation | `tun-connect-normalization-*` | rename |
| `control_issue136_linux_test.go` | daemon Unix-socket control | `control_unix_socket_linux_test.go` | rename |

Test function names and workflow labels carrying issue numbers are migrated with the same invariant names.

## E2E duplication inventory

| helper concept | current definitions | semantic differences | decision |
| --- | --- | --- | --- |
| daemon socket/service readiness | repeated `wait_for_daemon_socket*`/systemd polling | timeouts/messages vary | one parameterized helper in `scripts/e2e/lib` |
| package provenance | repeated dpkg/version/native-arch checks | required fields vary | shared package helper with explicit assertions |
| execution as unprivileged user | repeated `runuser` wrappers | env forwarding varies | shared wrapper preserving explicit environment |
| status polling | repeated JSON/status loops | terminal/progress states vary | common polling primitive; scenario predicate remains local |
| diagnostics capture | repeated journal/status/network capture | scenario additions vary | shared base capture plus scenario hooks |
| cleanup | repeated trap stacks | resources differ | shared LIFO trap primitive; resource cleanup stays scenario-specific |

## Go/test simplification candidates

- `internal/daemon/server.go:Server.Run`: split private composition helpers by setup, reconciliation/revalidation, boot/session startup, HTTP registration, listener serving, and shutdown; preserve construction/order/error semantics.
- Repository-cost/implementation guards: merge duplicated path accounting and remove obsolete issue-specific exceptions once artifacts have domain names.
- E2E contract tests: remove fixture-name-only assertions after renamed behavior assertions cover the same contract.
- Archive-only fixtures: delete only when `git grep` proves no runtime/workflow/test registration or when superseded byte-for-byte/behavior-equivalent fixtures are covered by active cases.

## Representative context budget

The baseline working set follows the current `AGENTS.md` routing: required durable docs plus the directly relevant implementation/tests. Approximation: `ceil(total bytes / 4)`.

| task class | baseline reading set | approximate tokens |
| --- | --- | ---: |
| CLI/public output | AGENTS + README + docs index/CLI/state/security + CLI source/tests | 48k |
| daemon lifecycle/recovery | AGENTS + daemon/state/TUN docs + daemon/recovery source/tests | 82k |
| TUN/networking | AGENTS + state/security + packaged TUN + TUN topic docs + networking source/tests | 96k |
| package/E2E | AGENTS + package/release/E2E docs + workflows/scripts/tests | 118k |

The after-state will use the same byte/4 method and task boundaries, with routing limited to the four permanent knowledge surfaces plus directly relevant executable code.
