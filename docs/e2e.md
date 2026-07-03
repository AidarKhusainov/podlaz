# Manual E2E validation

Manual host validation for behavior that is not suitable for the default pull-request gate.

The repository does not keep a GitHub Actions self-hosted E2E workflow. Privileged networking checks require a Linux host that the maintainer controls, so these checks are run manually when needed and the evidence belongs in the related pull request, issue, or release notes.

## When to run

Run manual E2E validation when a change touches:

- TUN devices;
- route, DNS, nftables, firewall, or resolver behavior;
- daemon privilege boundaries;
- systemd service behavior;
- package install, reinstall, purge, or service lifecycle;
- provider-backed proxy/TUN data-plane behavior;
- crash, rollback, or recovery behavior.

## Host requirements

Use a disposable or recoverable Linux host. Full coverage expects:

- systemd;
- `/dev/net/tun`;
- `iproute2`, `nftables`, `resolvectl`, `journalctl`;
- Go 1.26.4;
- package build tools from `docs/development.md`;
- provider/profile secrets supplied through the shell environment, not committed to the repository.

## Scripts

| Script | Scope |
| --- | --- |
| `scripts/e2e/cli-contract.sh` | CLI command and error checks. |
| `scripts/e2e/package-service.sh` | Package install, reinstall, service, cleanup. |
| `scripts/e2e/data-plane.sh` | Proxy connect, egress, listener scope, cleanup. |
| `scripts/e2e/server-coverage.sh` | Real-provider proxy/TUN probes and snapshots. |

## Suggested order

```bash
bash scripts/e2e/cli-contract.sh
bash scripts/e2e/package-service.sh
bash scripts/e2e/data-plane.sh
bash scripts/e2e/server-coverage.sh
```

Run only the subset that matches the risk of the change. For example, a CLI-only change normally does not need provider-backed data-plane coverage.

## Evidence

Record only non-secret evidence in the PR or issue:

- host OS and architecture;
- commit SHA;
- commands run;
- pass/fail result;
- redacted diagnostics or artifacts when useful.

Do not paste provider URLs, subscription links, private keys, tokens, raw generated configs, or unredacted logs.

## Non-goals

- Not a default PR gate.
- Not a GitHub-hosted CI replacement.
- Not permanent release evidence; keep evidence in issues, PRs, or release notes.
