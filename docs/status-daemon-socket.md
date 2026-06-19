# Status daemon socket classification

This document defines how `podlaz status` classifies the local daemon socket when daemon-backed status is unavailable.

The packaged daemon socket access model is defined by the daemon API and state/security documents: the systemd unit runs `podlazd` as the dedicated `podlaz:podlaz` service identity, systemd creates `/run/podlaz` with `RuntimeDirectoryMode=0750`, the daemon creates `/run/podlaz/podlazd.sock` with mode `0660`, and users who should run daemon-backed CLI commands must be members of the `podlaz` group.

## Classification rules

`podlaz status` remains read-only. It must not create, delete, recover, or mutate daemon socket paths, runtime directories, generated runtime configs, transaction files, TUN devices, routes, DNS settings, nftables objects, firewall rules, profiles, subscriptions, or core processes.

When the daemon API is reachable, `status` renders the daemon-backed response and does not use local stale-state classification.

When the daemon API is not reachable, the local fallback classifies the socket path before classifying runtime state:

| Socket/runtime observation | User-visible classification | Recovery candidate behavior |
| --- | --- | --- |
| Daemon socket is missing and runtime directory is missing | daemon not reachable, socket missing, clean inactive local state | no recovery candidate |
| Daemon socket is missing and runtime directory is present | daemon not reachable, socket missing, runtime directory present as stale | runtime directory remains a recovery candidate |
| Daemon socket exists and daemon API connect failed with permission denied | daemon not reachable because socket is inaccessible, socket present but inaccessible, runtime stale status unknown | runtime directory, generated runtime configs, and transaction files are not reported as stale fallback candidates because they may belong to a live daemon that the user cannot inspect |
| Daemon API connect failed with permission denied and socket path inspection also fails with permission denied | daemon not reachable because socket/runtime path is inaccessible, socket inaccessible, runtime stale status unknown | runtime directory, generated runtime configs, and transaction files are not reported as stale fallback candidates because they may belong to a live daemon that the user cannot inspect |
| Daemon socket path exists but is not a Unix socket | socket path present as a non-socket stale path | daemon socket path is a recovery candidate |
| Socket path inspection fails for another reason | socket state unknown and inspection incomplete | no clean-host claim; status reports a warning |

Permission-denied socket access is intentionally different from missing socket access. It is normally fixed by adding the user to the packaged `podlaz` group, starting a new login session so the group membership is active, or correcting the packaged socket ownership/mode. The fallback must keep safety-first behavior: incomplete visibility is reported as unhealthy/incomplete, but potentially live daemon-owned runtime state is not presented as executable stale cleanup.

## Exit status

A clean inactive fallback, such as missing socket plus missing runtime directory, exits with code `0`.

A permission-denied or otherwise incomplete fallback exits with code `3`, matching the diagnostic-command contract for unhealthy or incomplete state.
