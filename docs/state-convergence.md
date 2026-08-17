# State convergence contract

Podlaz uses fail-closed host-state ownership rules across TUN preflight, recovery, status, and diagnostics.

## TUN stale routing without durable ownership

Reserved routing identifiers such as policy-rule priorities `9999`/`10000` and routing table `51820` are Podlaz layout signals, not sufficient deletion authority by themselves. If those kernel objects remain after transaction ownership evidence has disappeared, TUN connect is blocked before mutation and the state is reported as ambiguous. Podlaz does not claim that `recover --execute` can remove such objects and does not delete them solely from reserved identifiers. An administrator must independently establish ownership before manual removal.

Stale resources for which the recovery scanner has exact cleanup authority retain the normal `plz recover --execute --yes` guidance.

## Proxy-only active ownership

Proxy-only is intentionally non-transactional. A live active proxy-only lifecycle therefore does not require a committed TUN transaction or TUN routing evidence. The active runtime config is considered owned only when the startup-scan candidate names its generated-config directory and that directory contains exactly the runtime config published by the live active status. Additional files or other candidates remain visible and are not filtered as owned.

## systemd-resolved missing-link classification

Doctor and recovery share the same strict per-link missing-resource classifier. On supported Ubuntu 24.04/systemd 255 behavior, the exact exit-0 stderr envelope

```text
Failed to resolve interface "podlaz0", ignoring: No such device
```

terminated by LF or CRLF proves that the managed link is absent. Other successful stderr remains unknown/fail-closed; generic `No such device` text is not sufficient.
