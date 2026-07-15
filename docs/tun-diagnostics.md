# TUN diagnostics

`podlaz doctor --tun` is the canonical layered diagnostic command for an active
podlaz full-tunnel session. The command is read-only and is executed by
`podlazd`, because the daemon owns the authoritative active-session metadata and
can inspect privileged networking state without delegating mutation rights to
the unprivileged CLI.

```bash
podlaz doctor --tun
podlaz doctor --tun --verbose
podlaz doctor --tun --json
```

Compact output identifies the failed layer, primary classification, saved report
path, and a practical next step. `--verbose` adds bounded route, DNS, TLS, HTTP,
IPv6, command, and timing evidence. `--json` emits the same redacted report model
with `schema_version: 1`; it is not a separate diagnostic implementation.

Exit code `0` means the report is healthy or degraded without an unhealthy
root cause. Exit code `3` means the report is unhealthy or unavailable. Invalid
flag combinations return `2`; daemon transport or runtime failures retain their
normal CLI error classification.

## Layer order

The runner is sequential and dependency-aware. A higher layer is marked
`skipped` with an explicit dependency reason when its prerequisite failed.
Every probe has a deadline and all response bodies, command output, and stored
reports are bounded.

1. active session, Xray process, transaction id, and podlaz ownership metadata;
2. VPN server bypass route through the physical uplink;
3. IPv4 policy rule and effective route through `podlaz0`;
4. `systemd-resolved` link ownership, planned DNS servers, `~.`, and
   `+DefaultRoute`;
5. DNS A query over UDP to the configured server;
6. the same DNS query over TCP;
7. positive system resolver lookup for `example.com`;
8. NXDOMAIN integrity lookup under the reserved `.invalid` domain;
9. TCP/443 reachability;
10. TLS handshake and certificate validation;
11. small bounded HTTPS requests to independent providers;
12. RFC 8484 DNS-over-HTTPS requests to Cloudflare and Google using catalogued
    bootstrap addresses, normal SNI, and normal certificate validation;
13. host, uplink, TUN, and default-route IPv6 state;
14. guarded PMTU evidence from two independent bounded 16 KiB HTTPS transfers.

PMTU is never inferred from one timeout. `likely_pmtu_blackhole` requires a
successful small HTTPS probe, two independent larger-transfer failures, and no
lower-layer route, DNS, TCP, or TLS failure that better explains the symptom.

## Classifications

Stable classifications include:

- `session_inactive`, `session_metadata_inconsistent`, `ownership_mismatch`;
- `server_bypass_failure`, `route_failure`, `policy_rule_failure`;
- `dns_apply_failure`, `foreign_dns_conflict`, `dns_udp_failure`,
  `dns_tcp_failure`, `dns_resolution_failure`, `dns_hijack_detected`;
- `tcp_443_failure`, `tls_failure`, `https_failure`;
- `doh_partial_failure`, `doh_failure`;
- `ipv6_not_present`, `ipv6_unusable`, `ipv6_leak`;
- `likely_pmtu_blackhole`;
- `timeout`, `cancelled`, `internal_diagnostic_error`.

The classifier chooses the earliest credible root cause. A failure from only one
DoH provider is degraded as `doh_partial_failure`; both providers failing is
`doh_failure`.

## Endpoint and privacy contract

The endpoint catalog is source-controlled in `internal/tundiag/catalog.go`.
Every target has a stable id, timeout, maximum response size where applicable,
required/best-effort status, and a privacy note. The current catalog uses only
public documentation/test names in source and never contains a user's VPN
endpoint.

Running the command can disclose the diagnostic host's source IP to the selected
public providers and sends the documented `example.com` DNS query. DoH uses
Cloudflare and Google as independent providers. The PMTU layer uses bounded
Cloudflare and Hetzner transfers. Unit tests replace all network operations with
local fixtures and never contact live internet endpoints.

## Latest report and failed connect capture

The daemon atomically replaces one private latest report:

```text
/run/podlaz/diagnostics/tun-last.json
```

The directory is daemon-owned and the report mode is `0600`. The JSON size is
limited to 256 KiB. There is no unbounded history. Output and persisted evidence
pass through the same central redaction and truncation policy.

If post-apply connectivity verification fails during `connect --mode tun`, the
daemon runs the bounded diagnostics before the first rollback command, stores
the report, includes its primary classification and path in the returned error,
and then performs the normal rollback. A later `podlaz doctor --tun` can return
that saved report when no active TUN session exists; the report is marked
historical so callers can distinguish it from a live run.

## Safety contract

The diagnostic path must not:

- change TUN MTU or any other interface setting;
- add or delete routes, policy rules, nftables tables, or firewall rules;
- modify `systemd-resolved`, NetworkManager, `/etc/resolv.conf`, or resolver
  configuration;
- restart services, disconnect other VPNs, trigger recovery, or change browser
  state;
- disable certificate validation or silently fall back to insecure transport.

Only read-only kernel lookups, bounded protocol probes, snapshot collection, and
private latest-report replacement are allowed.
