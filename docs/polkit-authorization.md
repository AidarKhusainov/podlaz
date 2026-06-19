# Polkit authorization

This document defines podlaz's optional daemon-side polkit authorization model.

## Enablement

Polkit checks are disabled by default. The default packaged access model remains the local daemon socket plus the dedicated `podlaz` group.

Enable daemon-side polkit checks intentionally with:

```bash
PODLAZ_POLKIT_AUTHORIZATION=required
```

Accepted enabled values are `1`, `true`, `yes`, `on`, `required`, and `polkit`. Accepted disabled values are empty, `0`, `false`, `no`, `off`, and `disabled`.

When checks are disabled, the daemon uses socket-group access as the non-polkit fallback. When checks are enabled but `pkcheck`, local peer credentials, or an authentication agent are unavailable, privileged operations fail clearly instead of silently falling back.

## Actions and defaults

The packaged policy file is installed at:

```text
/usr/share/polkit-1/actions/io.github.aidarkhusainov.podlaz.policy
```

It defines these operation-specific actions:

| Operation | Action | Default policy |
| --- | --- | --- |
| `podlaz connect --mode proxy-only <profile-ref>` | `io.github.aidarkhusainov.podlaz.connect-proxy-only` | admin authentication; active sessions may reuse recent authentication |
| `podlaz connect --mode tun <profile-ref>` | `io.github.aidarkhusainov.podlaz.connect-tun` | admin authentication |
| `podlaz disconnect` | `io.github.aidarkhusainov.podlaz.disconnect` | admin authentication; active sessions may reuse recent authentication |
| `podlaz recover --execute --yes` | `io.github.aidarkhusainov.podlaz.recover-execute` | admin authentication |

The policy must not contain broad `yes` defaults that silently allow all local users to execute privileged operations.

## Daemon enforcement boundary

Authorization is enforced by `podlazd`, not by the unprivileged CLI. The daemon authorizes fixed operation identifiers before executing lifecycle or recovery work.

Read-only daemon-backed status and doctor requests are not polkit-gated after socket access is already available.

Denied or unavailable authorization fails before runtime mutation. It must not start Xray, stop Xray, apply TUN/networking state, roll back transactions, delete generated configs, or mutate recovery state.

Authorization decisions use the local Unix peer process and fixed operation IDs. The daemon must not pass profile payloads, share URIs, subscription URLs, UUID-like user IDs, generated core configs, passwords, private keys, provider tokens, or other secrets through polkit messages or logs.

## CLI behavior

Users keep running the normal non-root CLI commands:

```bash
podlaz connect --mode tun <profile-ref>
podlaz disconnect
podlaz recover --execute --yes
```

Do not ask users to fix authorization by running normal lifecycle commands as `sudo podlaz ...`. Root-owned CLI state is not the podlaz privilege model.

On desktop systems with a polkit authentication agent, users should see an authentication prompt when a privileged daemon action requires admin authorization. On headless or TTY-only systems without an agent, the command fails with guidance to provide an agent or intentionally disable `PODLAZ_POLKIT_AUTHORIZATION` and use the socket-group fallback.

## Troubleshooting

If a privileged operation is denied:

1. Confirm the user can reach the daemon socket through the `podlaz` group.
2. Confirm `PODLAZ_POLKIT_AUTHORIZATION` is intentionally enabled for the daemon environment.
3. Confirm `pkcheck` is installed and available in the daemon `PATH`.
4. Confirm a desktop or TTY polkit authentication agent is running.
5. Inspect recent daemon logs with `podlaz logs` or `journalctl -u podlazd -n 50 --no-pager`.

If a host intentionally does not use polkit, leave `PODLAZ_POLKIT_AUTHORIZATION` unset or set it to `disabled` and rely on the documented socket-group access model.

## Validation

CI validates the static policy file for the expected action IDs, required defaults, and absence of broad `yes` defaults. Package validation must also confirm the file is present in the generated Debian package and installed under `/usr/share/polkit-1/actions/`.

Real GUI prompts and TTY/headless agent behavior require VM or host validation with systemd and polkit available.
