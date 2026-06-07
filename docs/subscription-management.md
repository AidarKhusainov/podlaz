# Subscription management

`tunwarden subscription` is the implemented v0.1 command group for managing Base64 URI-list subscription sources in local user-owned TunWarden state.

Canonical command names, flags, exit codes, JSON compatibility, and milestone boundaries remain owned by [CLI contract](./cli.md). This document describes the implemented behavior.

## Commands

```bash
tunwarden subscription add --name <name> --url <file-or-http-url>
tunwarden subscription update <subscription-id>
tunwarden subscription list [--json]
tunwarden subscription show <subscription-id> [--json]
```

`subscription delete` is intentionally not implemented yet because the product contract still needs explicit behavior for whether deleting a subscription also removes profiles previously imported from it.

## Supported sources and formats

The v0.1 implementation stores subscription metadata under the documented XDG user state directory and supports sources with these URL schemes:

- `file://`
- `http://`
- `https://`

The source content must be a Base64-encoded URI list. Decoded entries are read line by line. Empty lines are ignored.

Supported entries:

- VLESS share URIs, normalized through the same importer used by `tunwarden profile import`.

Unsupported entries, malformed URIs, or duplicate imported profile IDs are reported as unsupported entries without failing the whole update when at least one supported VLESS profile was imported.

## Update behavior

`subscription update <subscription-id>` performs this safe apply sequence:

1. read the stored subscription source;
2. fetch the source content;
3. decode the Base64 URI list;
4. normalize and validate supported VLESS entries;
5. report unsupported entries and warnings;
6. replace only the profiles previously owned by that subscription;
7. persist the updated subscription metadata with the latest imported profile IDs and update timestamp.

The command prints a stable human-readable update summary:

```text
Subscription updated: my-sub
Imported: 8
Updated: 2
Unchanged: 6
Removed: 0
Unsupported: 3
Warnings: 1
```

`Removed` counts profiles that were previously imported from the same subscription but no longer appear in the latest successful update.

If fetching, Base64 decoding, parsing, normalization, or validation fails before apply, the existing profile store and subscription metadata are left unchanged, so the last known good imported profile set remains available through `tunwarden profile list`.

## Output and redaction

`subscription list --json` and `subscription show --json` use the common v1 JSON shape with `schema_version`, `status`, `warnings`, and `errors`.

Human and JSON output redact subscription source URLs. Full subscription URLs, full share URIs, and VLESS user identities must not be printed by subscription commands.

`subscription add --json` and `subscription update --json` are deferred. They fail fast as invalid usage with exit code `2` until their JSON contract is implemented.

## Safety boundary

Subscription commands only parse input and mutate persistent local user-owned profile/subscription state. They never start Xray, never start network processes, and never mutate TUN devices, routes, DNS, nftables, or firewall state.

HTTP(S) subscription updates perform a bounded client fetch of the configured source URL. They do not change host networking configuration.
