# podlaz docs

Keep this directory small. podlaz is a VPN client, not a documentation site.
Permanent docs must be short, current, and directly useful for users,
packaging, releases, or contributors.

## Keep

| Document | Purpose |
| --- | --- |
| [CLI reference](./cli.md) | Commands, flags, modes, exit codes, and JSON/deferred behavior. |
| [State and security](./state-and-security.md) | State locations, redaction, daemon boundary, privileged networking safety. |
| [Debian package](./debian-package.md) | `.deb` layout, install behavior, service behavior, validation gates. |
| [Development guide](./development.md) | Local checks and contribution rules. |
| [Release workflow](./release.md) | Tag-to-release mapping and published artifacts. |
| [Self-hosted E2E](./e2e.md) | Manual real-host validation and runner contract. |
| [podlaz(1)](./man/podlaz.1) | Installed CLI man page. |
| [podlazd(8)](./man/podlazd.8) | Installed daemon/admin man page. |

## Rules

- Do not add a new doc for every command or internal subsystem.
- Prefer updating `cli.md` for user-visible CLI behavior.
- Prefer updating `state-and-security.md` for safety, state, and networking boundaries.
- Prefer updating `debian-package.md` for packaging behavior.
- Keep roadmap, evidence, temporary limitations, and acceptance notes in issues, PRs, or release notes.
- Man pages are concise installed references, not architecture documents.
