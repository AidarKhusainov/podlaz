# Subscription management

`podlaz subscription` is the implemented v0.1 command group for managing subscription sources in local user-owned podlaz state.

Canonical command names, flags, exit codes, JSON compatibility, and milestone boundaries remain owned by [CLI contract](./cli.md). This document describes the implemented behavior.

## Commands

```bash
podlaz subscription add --name <name> --url <file-or-http-url>
podlaz subscription update <subscription-id>
podlaz subscription list [--json]
podlaz subscription show <subscription-id> [--json]
podlaz subscription delete <subscription-id> --yes [--keep-profiles]
```

## Supported sources and formats

The v0.1 implementation stores subscription metadata under the documented XDG user state directory and supports sources with these URL schemes:

- `file://`
- `http://`
- `https://`

Supported response formats:

- Base64 URI-list
- Xray JSON object or array

URI-list entries are read line by line after decoding or direct format detection. Empty lines are ignored.

Supported entries are normalized through the same profile model used by `podlaz profile import`:

- VLESS share URIs and supported VLESS Xray JSON outbounds.
- VMess share URIs.
- Trojan share URIs.
- Shadowsocks share URIs.

Unsupported entries, malformed URIs, unsupported Xray outbounds, or duplicate imported profile IDs are reported as unsupported entries without failing the whole update when at least one supported profile was imported.

## Client identity placeholder

podlaz does not guess provider-specific HWID or device-id parameter names.

If a provider requires a stable client identity, place `{podlaz-client-id}` as the complete value of a subscription URL query parameter:

```bash
podlaz subscription add --name personal --url 'https://example.com/sub?hwid={podlaz-client-id}'
```

Before fetching the subscription, podlaz replaces the placeholder with a random stable client ID stored at:

```text
$XDG_STATE_HOME/podlaz/client-id
fallback: ~/.local/state/podlaz/client-id
```

The placeholder is allowed only as a complete query parameter value. It is not allowed in the host, userinfo, path, fragment, query parameter name, or as part of a larger query value.

To reset the client identity, remove only the `client-id` file. Resetting it can consume a new provider device slot or break provider-side device binding.

## Update behavior

`subscription update <subscription-id>` performs this safe apply sequence:

1. read the stored subscription source;
2. fetch the source content;
3. detect the response format;
4. normalize and validate supported entries;
5. report unsupported entries and warnings;
6. replace only the profiles previously owned by that subscription;
7. persist the updated subscription metadata with the latest imported profile IDs, detected format, and update timestamp.

The command prints a stable human-readable update summary:

```text
Subscription updated: my-sub
Format: base64
Imported: 8
Updated: 2
Unchanged: 6
Removed: 0
Unsupported: 3
Warnings: 1
```

`Removed` counts profiles that were previously imported from the same subscription but no longer appear in the latest successful update.

If fetching, decoding, parsing, normalization, validation, profile replacement, or metadata update fails, the existing profile store and subscription metadata are left unchanged, so the last known good imported profile set remains available through `podlaz profile list`.

## Delete behavior

`subscription delete <subscription-id> --yes` removes the subscription metadata and removes only profiles owned by that subscription. Manual profiles, one-off imported URI/file profiles, and profiles owned by other subscriptions are preserved.

The command prints a concise summary:

```text
Subscription deleted: personal
Profiles removed: 8
```

`subscription delete <subscription-id> --yes --keep-profiles` removes only the subscription metadata and keeps previously imported profiles in the profile store:

```text
Subscription deleted: personal
Profiles kept: 8
```

`subscription delete` follows the destructive command confirmation model: the current non-interactive path requires `--yes`. Missing subscription IDs fail clearly. Profile cleanup is rolled back if subscription metadata removal fails after profile cleanup has been applied.

## Output and redaction

`subscription list --json` and `subscription show --json` use the common v1 JSON shape with `schema_version`, `status`, `warnings`, and `errors`.

Human and JSON output redact subscription source URLs. Full subscription URLs, full share URIs, imported profile identities, provider tokens, generated client identities, passwords, and generated core configs must not be printed by subscription commands.

`subscription add --json`, `subscription update --json`, and `subscription delete --json` are deferred. They fail fast as invalid usage with exit code `2` until their JSON contracts are implemented.

## Safety boundary

Subscription commands only parse input and mutate persistent local user-owned profile/subscription state. They never start Xray, never start network processes, and never mutate TUN devices, routes, DNS, nftables, or firewall state.

HTTP(S) subscription updates perform a bounded client fetch of the configured source URL. `subscription delete` never connects to the subscription URL.
