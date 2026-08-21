# Boot Autostart and Product Lifecycle UX Design

Status: approved design for Issue #263.

This design is the user-facing completion layer on top of the Network Session continuity from #259, collision-safe exact ownership from #260, session-scoped Privacy Envelope from #261, and evidence-driven reconciliation from #262.

## Goal

Keep the product lifecycle simple and make boot reconnect an explicit, separate user choice:

```text
explicit connect
  -> active now
  -> daemon restart/crash/package upgrade continues the same current-boot Network Session
  -> reboot ends that ordinary connection

boot autostart enabled
  -> a new boot may create exactly one new logical automatic connection attempt
```

A normal connection must never implicitly become persistent boot policy.

## Architectural invariants

1. Same-boot continuation and boot autostart are different authorities and different persistence domains.
2. The existing `networkSessionState`/continuation remains boot-bound and continues to discard previous-boot state by `boot_id`.
3. Boot autostart is explicit daemon-owned persistent policy stored outside `/run/podlaz`.
4. A boot-autostart attempt always enters the canonical normal `Connect` lifecycle; there is no special networking path for boot.
5. Existing continuation/resume always has startup priority over evaluation of boot autostart.
6. At most one logical boot-autostart lifecycle may be initiated for one `boot_id`.
7. Daemon crash/restart may continue that same logical attempt; a terminally completed attempt never starts a second automatic connection during the same boot.
8. Explicit disconnect after a successful autostart does not make the current boot eligible for another automatic connection.
9. Disabled autostart causes no boot-attempt state mutation.
10. No persistent boot policy contains or exposes foreign-VPN handoff semantics. Autostart uses the canonical default semantics of normal `Connect`.

## Boot Autostart Manifest

Introduce one minimal daemon-owned persistent `BootAutostartManifest`.

Conceptually:

```text
BootAutostartManifest
  schema_version
  enabled
  mode
  profile_id
  profile_name
  validated connection snapshot
```

The validated connection snapshot is the minimal canonical `ProfileSnapshot` material already required by the normal daemon `Connect` request. It may contain sensitive connection material because the daemon must be able to connect after boot without reading a user home directory.

The manifest is not a second profile database. It deliberately excludes subscription URLs, import history, UI metadata, profile collections, or any other state not required to perform the configured boot connection.

### Manifest creation

`podlaz autostart enable ...` performs:

```text
user profile store
    |
    v
load selected profile
    |
    v
run the same profile/mode validation used by explicit connect
    |
    v
construct canonical ProfileSnapshot / connect material
    |
    v
authorized daemon request
    |
    v
daemon validates request again and atomically replaces BootAutostartManifest
```

The CLI remains the only component that reads the user-owned XDG profile store. The privileged daemon never searches user home directories to resolve an autostart profile.

### Persistent storage

Use the packaged private daemon state directory (`StateDirectory=podlaz`, normally `/var/lib/podlaz`) rather than the volatile runtime directory.

Requirements:

- root-owned/private;
- file mode `0600`;
- versioned schema;
- strict decoding and validation;
- bounded file size;
- atomic temp-file replacement;
- file fsync before rename;
- directory fsync after rename;
- no manifest contents, credentials, server names, profile IDs, or endpoints in journal messages, normal status errors, PR text, or fixtures;
- only documentation/example values in tests.

Disabling autostart atomically replaces/removes the enabled manifest according to one canonical representation. The implementation must leave no ambiguous partially-written policy.

## Public autostart UX

Add one dedicated narrow command family rather than a general settings framework:

```text
podlaz autostart enable [--mode proxy-only|tun] <profile-id>
podlaz autostart disable
podlaz autostart status
```

`enable` configures future boot behavior only. It does not connect immediately.

`disable` prevents creation of a new automatic lifecycle on future boots. It does not disconnect the currently active Network Session.

`status` is read-only and reports whether boot autostart is enabled and, when safe to display, the redacted profile name/mode configured for the next boot.

`--json` behavior follows the existing CLI compatibility policy: do not silently invent an unstable JSON contract where the command family does not define one.

### Authorization

Changing boot-autostart policy is a separate privileged action from connecting now.

Add a dedicated polkit authorization action for autostart configuration. Do not reuse `connect-tun`, `connect-proxy-only`, or `disconnect` as authorization for persistent boot policy changes.

Read-only autostart status should not require a privileged networking mutation. If daemon access is used, it must remain a read-only status path.

## Boot attempt state

Introduce a volatile root-owned `BootAutostartAttempt` under `/run/podlaz`, keyed and validated against the current Linux `boot_id`.

It records only sanitized lifecycle control metadata, not a duplicate connection snapshot. Conceptually:

```text
BootAutostartAttempt
  schema_version
  boot_id
  manifest_revision_or_digest
  state: in_progress | succeeded | terminal
  terminal_reason_code?   # sanitized stable classification only
```

The manifest remains the source of connect material. The attempt record exists only to make one-logical-attempt semantics crash-consistent.

### Why three states are required

A boolean `attempted=true` is insufficient:

- if it is written before `Connect` and the daemon crashes immediately, the first logical attempt would be lost;
- if it is written only after `Connect`, a crash during admission could start two logical attempts;
- if successful autostart is not recorded as completed, an explicit disconnect followed by daemon restart could incorrectly reconnect during the same boot.

The state machine therefore distinguishes an unfinished logical lifecycle from completed success and completed terminal failure.

## One logical attempt per boot

The invariant is:

> One boot may create at most one new autostart lifecycle. Daemon replacement may continue that lifecycle, but completion never grants another automatic connect in the same boot.

### Start

When autostart is enabled and no resumable Network Session exists:

1. load and strictly validate the persistent manifest;
2. read current `boot_id`;
3. inspect the boot-attempt record;
4. if no record exists for this boot, atomically create `in_progress` before beginning the automatic connect lifecycle;
5. run the canonical normal `Connect` path through the existing lifecycle operation serialization.

Creating `in_progress` is admission to one logical boot attempt, not proof that a particular `Connect` call has already executed.

### Crash before continuation exists

If the daemon crashes after `in_progress` is durable but before normal `Connect` has persisted Network Session continuation, restart sees:

```text
attempt = in_progress
no resumable Network Session
```

This means the same logical autostart attempt was admitted but not completed. Startup may continue that same logical attempt by entering the normal `Connect` path using the manifest. This is not a second logical attempt and must not create another attempt record.

### Crash after continuation exists

If the daemon crashes after normal `Connect` persisted same-boot Network Session authority, startup continuation has priority. The daemon resumes/reconciles that existing session through #259-#262 and does not start a new boot connection.

After the resumed autostart connection is sufficiently established, the attempt record converges to `succeeded`.

### Successful connection

Once the automatic normal `Connect` completes successfully, mark the attempt `succeeded`.

`Succeeded` means this boot's automatic-connect authority has been consumed. It does not mean the VPN must remain connected forever during the boot.

Therefore:

```text
autostart succeeds
-> user explicitly disconnects
-> continuation is removed
-> attempt remains succeeded
-> daemon restart in the same boot does not reconnect
```

### Terminal failure

If the autostart lifecycle reaches a terminal connect/reconciliation outcome, complete the normal exact-owned terminal cleanup first according to #261/#262, then mark the boot attempt `terminal` with only a stable sanitized high-level reason code.

After `terminal`:

- ordinary networking must be usable according to the existing terminal teardown contract;
- primary state is `Disconnected`;
- no automatic reconnect is scheduled;
- daemon restart in the same boot does not start another connection;
- the next boot gets a new eligibility decision because `boot_id` changed.

A crash while terminal teardown is incomplete is still continuation/recovery work and has priority over autostart. The attempt may remain `in_progress` until that same lifecycle is conclusively converged, but startup must not interpret it as permission for a competing fresh connection while durable Network Session cleanup authority exists.

## Strict startup ordering

Daemon startup follows this order:

```text
podlazd startup
    |
    v
load/recover/resume any current-boot Network Session authority
    |
    +-- resumable or cleanup/convergence work exists
    |      -> finish that lifecycle first
    |      -> never evaluate a competing fresh autostart connect
    |
    v
no current-boot Network Session remains to continue
    |
    v
evaluate BootAutostartManifest
    |
    +-- disabled / absent
    |      -> Disconnected; no attempt-state mutation
    |
    +-- enabled
           |
           v
       evaluate current-boot BootAutostartAttempt
           |
           +-- none -> admit one logical attempt -> normal Connect
           +-- in_progress -> continue the already-admitted logical attempt
           +-- succeeded -> no automatic Connect
           +-- terminal -> no automatic Connect
```

This ordering is mandatory. Autostart never competes with restart continuation, crash recovery, terminal cleanup, or protected replacement recovery.

## Canonical Connect path

Autostart must reuse the existing lifecycle stack rather than introducing `bootConnect`, `quickConnect`, or a verifier-bypassing launcher.

The automatic boot request enters the same serialization and Network Session code path as explicit `connect`, thereby inheriting:

- profile/connect request validation;
- collision-safe allocation;
- exact transaction ownership;
- server bootstrap-path safety;
- Privacy Envelope arming and replacement semantics;
- normal data-plane verification;
- evidence-driven reconciliation;
- automatic exact-owned repair;
- bounded terminal decision;
- terminal teardown and ordinary-network verification.

The manifest does not persist `handoff`. The boot request uses the canonical default normal `Connect` semantics. Current normalization may resolve an omitted policy to `block`, but that is not part of the persistent autostart schema and may evolve independently.

## Product-oriented lifecycle status

Primary human `podlaz status` becomes a product surface rather than a network-debug dump.

Expose stable product-oriented states:

- `Connected`
- `Connecting`
- `Reconnecting`
- `Disconnected`

The exact internal transaction, TUN-health, reconciliation, startup-scan, route, DNS, firewall, and ownership evidence remains available in daemon/internal diagnostic models and operator commands.

### Mapping

At minimum:

- established sufficiently verified session -> `Connected`;
- explicit or boot connect admitted but not yet established -> `Connecting`;
- established protected session undergoing #262 reconciliation/rebuild -> `Reconnecting`;
- no active protected/product session after normal disconnect or terminal cleanup -> `Disconnected`.

Transient reconciliation must not render as a terminal error.

### Terminal reason

After a confirmed terminal runtime outcome, human status should expose one concise sanitized high-level reason, for example:

```text
Status: Disconnected
Reason: VPN connection could not be restored safely
```

The primary reason must not require knowledge of routing tables, transaction projections, nftables, command envelopes, resolver internals, or ownership tokens.

Detailed evidence and the saved TUN diagnostic report remain available through `doctor`, `recover`, and daemon diagnostic surfaces.

The implementation should use a small stable typed reason classification rather than parse arbitrary internal error strings into user output.

### Autostart display

Human status may show:

```text
Autostart: Enabled
```

or an equivalently concise phrase. Documentation must state clearly that enabled means eligible on the next boot; it does not mean a terminally failed or explicitly disconnected session will reconnect again during the current boot.

## Compatibility of detailed status

Simplify the normal human rendering, not the underlying diagnostic evidence model.

Do not remove detailed fields from daemon status merely to reduce terminal output. Add product-oriented lifecycle fields or derive them compatibly while retaining current operator/diagnostic information needed by tests and tooling.

`podlaz status --json` is currently deferred, so this issue must not accidentally establish an unreviewed JSON schema. If a machine-readable contract is added, it must be explicitly versioned and tested.

## Connect/disconnect human output

Successful explicit connect should be concise, for example:

```text
Connected
Profile: Example VPN
Mode: TUN
```

Successful disconnect should be concise:

```text
Disconnected
```

Do not print routine implementation details such as route state, DNS state, firewall state, runtime config paths, transaction IDs, or network command output in the default success path.

Errors should similarly lead with stable user-level meaning. Engineering detail belongs in diagnostics.

## Recovery UX

`recover` remains an engineering/operator/emergency surface.

Normal successful connect, disconnect, daemon restart, crash recovery, package upgrade, suspend/resume, DHCP/Wi-Fi churn, and exact-owned reconciliation must not instruct the user to run manual Linux repair commands when the daemon can converge automatically.

Primary product guidance may point to a safe read-only diagnostic command such as `podlaz doctor --tun` after failure, while `recover --execute`, `ip`, `resolvectl`, `nft`, NetworkManager/systemd restarts, and similar repair procedures remain outside the normal workflow.

Existing detailed recovery state and warnings are preserved for operators.

## Failure and malformed-state policy

### Invalid persistent manifest

A malformed, unsupported, oversized, permission-invalid, or semantically invalid boot manifest must fail closed:

- do not attempt a connection;
- do not mutate networking;
- publish `Disconnected` plus a sanitized configuration problem;
- keep detailed parse/storage evidence out of normal human output and available only through appropriate daemon/operator diagnostics;
- never log manifest contents.

### Attempt-state corruption

A malformed or ambiguous current-boot attempt record must never grant permission to start multiple automatic connections.

Fail closed: do not create a fresh autostart lifecycle until exact current-boot attempt authority is known. This may require operator diagnostics, but must not damage an existing ordinary network.

### Manifest changes during a boot

`autostart enable`/`disable` changes future boot policy. It does not reset the current boot's consumed attempt authority and does not automatically connect/disconnect the current session.

A manifest revision/digest in the volatile attempt record is diagnostic/fencing metadata only. Changing the manifest during the same boot must not create a second automatic lifecycle.

The next boot evaluates the latest valid manifest.

## Testing strategy

### Persistent manifest tests

Cover:

- strict schema validation;
- minimal validated connect snapshot round-trip;
- no subscription/history/UI-only data;
- atomic replacement semantics;
- private permissions;
- bounded file size;
- malformed/trailing/unknown-field rejection;
- no secret-bearing values in rendered errors/log assertions.

### Boot logical-attempt tests

Deterministically cover:

1. ordinary explicit connect plus new boot with autostart disabled -> no fresh connect, `Disconnected`;
2. autostart enabled plus new boot -> one fresh normal connect lifecycle is admitted;
3. successful boot connect -> normal `Connected` state and attempt=`succeeded`;
4. terminal boot connect failure -> exact cleanup, `Disconnected`, attempt=`terminal`, no retry;
5. daemon restart without reboot while an ordinary connection is active -> existing session continues regardless of autostart setting;
6. crash after attempt=`in_progress` but before Network Session continuation exists -> restart continues the same logical attempt exactly once;
7. crash after continuation exists -> continuation wins and no competing boot connect is created;
8. explicit disconnect after successful autostart -> restart in same boot does not reconnect;
9. terminal failure followed by daemon restart -> no automatic reconnect;
10. next boot after terminal failure -> eligible for one new attempt if manifest remains enabled;
11. disabled autostart -> no attempt file is created;
12. corrupt/ambiguous attempt state -> fail closed without duplicate connect authority;
13. manifest modification during same boot -> does not reset consumed attempt authority.

### Serialization/race tests

Prove:

- autostart admission is serialized with explicit connect/disconnect/recovery/shutdown;
- startup continuation and autostart cannot both enter lifecycle mutation;
- autostart cannot race startup recovery gate release;
- shutdown after autostart admission preserves the same restart/stop intent semantics as a normal session;
- no second logical attempt appears through daemon `Restart=on-failure`.

### UX contract tests

Cover:

- primary human status uses only stable product-oriented lifecycle state plus concise profile/mode/autostart information;
- `revalidating`/bounded reconciliation maps to `Reconnecting`, not terminal failure;
- terminal teardown maps to `Disconnected` with stable high-level reason;
- detailed diagnostics remain inspectable separately;
- successful connect/disconnect output omits transaction/routing/DNS/firewall/runtime-config detail;
- normal successful automatic recovery does not print manual Linux repair guidance;
- redaction parity remains intact.

### CLI, authorization, completions, and docs

Cover the new `autostart` family in:

- CLI parsing/help;
- completions;
- daemon API validation;
- dedicated polkit action and policy tests;
- man pages;
- `docs/cli.md`;
- `docs/state-and-security.md`;
- `docs/debian-package.md`;
- installed package acceptance.

### Installed-package acceptance

The package gate must exercise real service boundaries for:

- boot boundary with autostart disabled;
- boot boundary with autostart enabled;
- successful autostart connection;
- terminal autostart failure with no same-boot retry;
- daemon restart while connected independent of autostart;
- package upgrade while connected;
- explicit disconnect;
- one terminal runtime failure;
- concise primary status and preserved detailed diagnostics.

Use only repository-controlled example data and sanitized E2E seams.

## Security and privacy implications

The only new durable secret-bearing state is the minimal boot connect manifest. This is necessary because the privileged daemon cannot read user-owned XDG profile storage after boot under the packaged hardening model.

The manifest does not broaden network ownership. It only authorizes creation of a future normal Connect request. All actual networking authority remains subject to the existing exact ownership, collision, privacy, verification, and reconciliation contracts.

The new polkit action protects policy changes. The daemon must never log or return the stored connection snapshot through primary status.

No new daemon capabilities or systemd privilege relaxations are required by this design.

## Documentation/product contract

After implementation the canonical documentation must consistently state:

```text
connect now
-> remains across daemon restart/crash/package upgrade in the current boot
-> does not imply reboot reconnect

autostart enabled
-> next boot may create one new normal Connect lifecycle
-> one logical automatic lifecycle per boot
-> no same-boot retry after success+disconnect or terminal failure
```

Normal user documentation leads with import/profile/connect/disconnect/status/autostart. `doctor` and `recover` remain available but are not presented as routine networking maintenance.

## Non-goals

- no general-purpose settings framework;
- no daemon-side user profile database;
- no user-home scanning by root daemon;
- no persistence of current Network Session continuation across reboot;
- no special boot networking implementation;
- no automatic reconnect loop after terminal failure;
- no foreign VPN discovery/control;
- no persisted legacy handoff policy in the manifest;
- no removal of detailed diagnostics;
- no unversioned new JSON status contract.

## Acceptance criteria

The implementation is complete when all of the following are true:

- reboot reconnect occurs only when an explicit valid Boot Autostart Manifest is enabled;
- ordinary connection continuation remains strictly same-boot and independent of autostart policy;
- startup continuation/recovery always precedes fresh autostart evaluation;
- one boot admits at most one logical automatic lifecycle;
- crashes can continue that same logical lifecycle without losing or duplicating it;
- explicit disconnect after autostart success and terminal failure both suppress further same-boot automatic connects;
- autostart enters the canonical normal Connect/Network Session path and inherits #259-#262 safety;
- default human status/connect/disconnect output is product-oriented and concise;
- terminal failure presents a stable high-level reason without leaking internal evidence;
- detailed diagnostics and operator recovery surfaces remain available;
- CLI docs, man pages, completions, polkit policy, package behavior, and tests describe one consistent lifecycle contract.
