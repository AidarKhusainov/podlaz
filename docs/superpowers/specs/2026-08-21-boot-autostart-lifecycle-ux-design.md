# Boot Autostart and Product Lifecycle UX Design

Status: approved design for Issue #263, revised after crash-consistency self-review.

This design is the user-facing completion layer on top of Network Session continuity from #259, collision-safe exact ownership from #260, the session-scoped Privacy Envelope from #261, and evidence-driven reconciliation from #262.

## Goal

Keep the product lifecycle simple and make reboot reconnect an explicit, separate user choice:

```text
explicit connect
  -> active now
  -> daemon restart/crash/package upgrade continues the same current-boot Network Session
  -> reboot ends that ordinary connection

boot autostart enabled
  -> a later boot may create exactly one new logical automatic connection attempt
```

A normal connection must never implicitly become persistent boot policy.

## Architectural invariants

1. Same-boot continuation and boot autostart are different authorities and different persistence domains.
2. Existing `networkSessionState` remains boot-bound and continues to discard previous-boot continuation by `boot_id`.
3. Boot autostart is explicit daemon-owned persistent policy stored outside `/run/podlaz`.
4. Autostart configuration takes effect for a later boot; enabling it during the current boot must not turn a later daemon restart in that same boot into a synthetic reboot.
5. A boot-autostart attempt always enters the canonical normal `Connect` lifecycle; there is no special networking path for boot.
6. Existing Network Session continuation/recovery always has startup priority over any fresh boot-autostart admission.
7. At most one logical boot-autostart lifecycle may be admitted for one `boot_id`.
8. Daemon crash/restart may continue that same logical attempt. A completed attempt never starts a second automatic connection during the same boot.
9. Explicit disconnect after successful autostart does not make the current boot eligible for another automatic connection.
10. Disabled autostart creates no boot-attempt record.
11. Persistent boot policy does not contain foreign-VPN handoff semantics. Autostart uses canonical default normal-`Connect` semantics.

## Boot Autostart Manifest

Introduce one minimal daemon-owned persistent `BootAutostartManifest`.

Conceptually:

```text
BootAutostartManifest
  schema_version
  generation
  configured_boot_id
  mode
  profile_id
  profile_name
  validated connection snapshot
```

Presence of one valid manifest means autostart is enabled. Absence means disabled. There is no separate durable `enabled=false` file.

`generation` is an opaque non-secret identifier changed on each successful enable/update. It is control metadata only; do not derive it by hashing secret-bearing profile material.

`configured_boot_id` records the boot in which this manifest version was configured. A manifest is eligible for a **fresh** automatic attempt only when its `configured_boot_id` differs from the current boot. This preserves the user meaning of “autostart on boot”: enabling autostart now configures the next boot and must not cause a same-boot daemon restart to connect unexpectedly.

The validated connection snapshot is the minimal canonical `ProfileSnapshot` material already required by normal daemon `Connect`. It may contain sensitive connection material because the daemon must be able to connect after boot without reading a user home directory.

The manifest is not a second profile database. It deliberately excludes subscription URLs, import history, profile collections, UI-only metadata, and any state not required to perform the configured boot connection.

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
daemon validates again, binds configured_boot_id, and atomically replaces manifest
```

The CLI remains the component that reads the user-owned XDG profile store. The privileged daemon never searches user home directories to resolve an autostart profile.

### Persistent storage

Use the packaged private daemon state directory (`StateDirectory=podlaz`, normally `/var/lib/podlaz`) rather than the volatile runtime directory. Production code gets an explicit daemon-state path with a test override; it must not reuse user XDG state.

Requirements:

- root-owned/private directory;
- manifest mode `0600`;
- versioned schema;
- strict decoding with unknown/trailing data rejection;
- semantic validation through the canonical connect/profile snapshot validators;
- bounded file size;
- atomic temp-file replacement;
- file fsync before rename;
- directory fsync after rename or unlink;
- no manifest contents, credentials, server names, profile IDs, endpoints, or opaque connection material in journal messages or normal status errors;
- documentation/example values only in tests, docs, PRs, and comments.

`autostart disable` removes the manifest and fsyncs the containing directory. Absence is the only canonical disabled representation. Disabling does not mutate current-boot attempt state and does not disconnect an active/in-progress Network Session.

## Public autostart UX

Add one dedicated narrow command family rather than a general settings framework:

```text
podlaz autostart enable [--mode proxy-only|tun] <profile-id>
podlaz autostart disable
podlaz autostart status
```

`enable` configures future boot behavior only. It does not connect immediately.

`disable` prevents new automatic lifecycles on future boots. It does not disconnect or cancel an already admitted current-boot lifecycle.

`status` is read-only and reports whether boot autostart is enabled and, when safe to display, the redacted configured profile name/mode. Human wording should make the next-boot meaning clear, for example `Autostart: Enabled for next boot`.

`--json` follows the existing CLI compatibility policy. Do not silently establish an unreviewed machine-readable contract where the command family has not explicitly defined one.

### Authorization

Changing boot-autostart policy is a separate privileged action from connecting now.

Add a dedicated polkit authorization action for autostart configuration. Do not reuse `connect-tun`, `connect-proxy-only`, or `disconnect` authorization for persistent boot policy changes.

Read-only autostart status must not gain networking mutation authority.

## Boot Autostart Attempt

Introduce one volatile root-owned `BootAutostartAttempt` under `/run/podlaz`, strictly validated against the current Linux `boot_id`.

Conceptually:

```text
BootAutostartAttempt
  schema_version
  boot_id
  manifest_generation
  state: in_progress | succeeded | terminal
  admitted connect request snapshot
  terminal_reason_code?   # stable sanitized classification only
```

The attempt record is private (`0600`), bounded, strict, atomically replaced, and fsynced with the same crash-safety standard as other lifecycle authority files.

The admitted connect request snapshot intentionally duplicates only the minimal validated connection material for this **current logical attempt**. This is necessary to close the crash window between boot-attempt admission and creation of normal Network Session continuation:

```text
attempt=in_progress persisted
    |
    X daemon crashes before Connect persists continuation
    |
manifest may be changed or disabled by policy management
```

On restart, the daemon must continue the exact admitted logical attempt, not silently substitute a newly configured profile. Therefore `in_progress` pins its admitted canonical request. Once normal Network Session continuation exists, that existing #259 authority remains the primary current-session source of truth.

The attempt record is not a profile store: it is boot-scoped admission/replay authority for one already-authorized lifecycle and disappears with `/run` at reboot. Its sensitive snapshot is never rendered or logged.

A valid attempt from a different `boot_id` is stale previous-boot control state and may be discarded before current-boot policy evaluation. An invalid/ambiguous attempt for the current boot fails closed and never grants fresh connect authority.

## Why three attempt states are required

A boolean `attempted=true` is insufficient:

- writing it before `Connect` and never replaying would lose the admitted attempt on an immediate crash;
- writing it only after `Connect` would permit duplicate admission during a crash window;
- failing to record successful completion would let explicit disconnect followed by same-boot daemon restart reconnect unexpectedly;
- failing to distinguish terminal completion would allow `Restart=on-failure` to become a prohibited background retry loop.

The state machine therefore distinguishes unfinished logical lifecycle from completed success and completed terminal failure.

## One logical attempt per boot

The invariant is:

> One boot may admit at most one new autostart lifecycle. Daemon replacement may continue that lifecycle, but completion never grants another automatic connect in the same boot.

### Fresh admission

A fresh automatic lifecycle may be admitted only when all are true:

1. startup continuation/recovery found no current-boot Network Session work to resume or finish;
2. no current-boot attempt record exists;
3. a valid persistent manifest exists;
4. the manifest was configured in an earlier boot (`configured_boot_id != current_boot_id`).

Admission atomically creates `BootAutostartAttempt{state: in_progress}` with the exact validated request pinned from that manifest before entering normal `Connect`.

### Crash before continuation exists

If the daemon crashes after attempt admission but before normal `Connect` has persisted Network Session continuation, restart sees:

```text
attempt = in_progress
no current-boot Network Session
```

The daemon continues the **same logical attempt** using the request pinned in the attempt record. It does not reload or substitute the current manifest and does not create another attempt record.

This remains true if the user changed or disabled future autostart policy after the original admission: policy mutation affects future boot admission, not an already admitted lifecycle.

### Crash after continuation exists

If the daemon crashes after normal `Connect` persisted current-boot Network Session authority, startup continuation has priority. The daemon resumes/reconciles that exact session through #259-#262 and does not start a competing boot connect.

If the resumed lifecycle establishes the protected session successfully, the corresponding `in_progress` attempt converges to `succeeded`.

If startup instead proves the admitted lifecycle terminal and finishes its exact cleanup, the attempt converges to `terminal` rather than being replayed as a new connection.

Startup orchestration must therefore retain enough typed outcome information from continuation/teardown convergence to distinguish “session resumed successfully” from “the admitted session was terminally cleaned”. It must not infer this distinction from human error text.

### Successful connection

When the automatic normal `Connect` completes successfully, mark the attempt `succeeded`.

`Succeeded` means current-boot automatic-connect authority has been consumed. It does not mean the VPN must remain connected for the rest of the boot.

Therefore:

```text
autostart succeeds
-> attempt=succeeded
-> user explicitly disconnects
-> Network Session continuation is removed
-> daemon restart in same boot
-> no automatic reconnect
```

If a successfully autostarted session later reaches a terminal runtime failure, the attempt may remain `succeeded`; that state already prevents another same-boot automatic admission. The terminal lifecycle reason is product-status/diagnostic evidence, not new autostart authority.

### Terminal initial attempt

If the admitted boot connect/reconciliation lifecycle fails before reaching successful establishment, use the normal exact-owned terminal cleanup contract from #261/#262. Only after that logical lifecycle has conclusively converged to clean `Disconnected` should the attempt become `terminal` with a stable sanitized high-level reason code.

After `terminal`:

- ordinary networking is usable according to the existing terminal teardown contract;
- primary state is `Disconnected`;
- no automatic reconnect is scheduled;
- daemon restart in the same boot does not start another connection;
- the next boot is eligible for one new attempt if a valid manifest remains enabled.

A crash while terminal teardown is incomplete remains continuation/recovery work and has priority. The attempt stays `in_progress` until the same admitted lifecycle is conclusively classified; startup must never interpret incomplete cleanup as permission for a fresh connect.

## Strict startup ordering

Daemon startup follows this order:

```text
podlazd startup
    |
    v
recover/resume/finish any current-boot Network Session authority
    |
    +-- resumable or cleanup/convergence work exists
    |      -> finish that lifecycle first
    |      -> never evaluate competing fresh autostart admission
    |
    v
no current-boot Network Session remains
    |
    v
inspect current-boot BootAutostartAttempt
    |
    +-- in_progress
    |      -> continue exact pinned logical attempt
    |
    +-- succeeded
    |      -> no automatic Connect
    |
    +-- terminal
    |      -> no automatic Connect
    |
    +-- invalid/ambiguous current-boot record
    |      -> fail closed; no automatic Connect
    |
    +-- no current-boot record
           |
           v
       evaluate BootAutostartManifest
           |
           +-- absent -> Disconnected; no attempt mutation
           +-- configured in current boot -> enabled for next boot; no attempt mutation
           +-- valid from earlier boot -> atomically admit in_progress -> normal Connect
```

This ordering is mandatory. Autostart never competes with restart continuation, crash recovery, terminal cleanup, or protected replacement recovery.

## Canonical Connect path

Autostart reuses the existing lifecycle stack rather than introducing `bootConnect`, `quickConnect`, a second Network Session implementation, or verifier-bypassing startup logic.

The admitted boot request enters the same lifecycle serialization and `networkSessionLifecycle.Connect` path as explicit `connect`, thereby inheriting:

- connect/profile snapshot validation;
- collision-safe resource allocation;
- exact transaction ownership;
- safe VPN-server bootstrap path;
- Privacy Envelope arming/replacement semantics;
- normal data-plane verification;
- evidence-driven reconciliation;
- exact-owned automatic repair;
- bounded terminal decision;
- terminal teardown and ordinary-network verification.

The manifest and attempt do not persist `handoff`. The reconstructed request uses canonical default normal-`Connect` semantics. Current normalization may resolve omitted handoff to `block`, but that legacy terminology is not part of persistent boot policy and may evolve independently.

Autostart admission must use the same lifecycle operation coordinator/lock used by explicit connect, disconnect, recovery, automatic reconciliation mutation, and shutdown. Do not create a parallel mutation lock.

## Product-oriented lifecycle status

Primary human `podlaz status` becomes a product surface rather than a network-debug dump.

Expose stable product-oriented states:

- `Connected`
- `Connecting`
- `Reconnecting`
- `Disconnected`

The exact transaction, TUN-health, reconciliation, startup-scan, route, DNS, firewall, and ownership evidence remains available in daemon/internal diagnostic models and operator commands.

### Mapping

At minimum:

- sufficiently verified established session -> `Connected`;
- explicit or boot connect admitted but not yet established -> `Connecting`;
- established protected session undergoing #262 reconciliation/rebuild -> `Reconnecting`;
- no active product session after normal disconnect or terminal cleanup -> `Disconnected`.

Transient reconciliation must never render as a terminal failure.

The product-state mapping should be typed and centralized rather than duplicated across CLI renderers. Existing detailed daemon status fields are preserved for compatibility and diagnostics.

### Terminal reason

After a confirmed terminal runtime outcome, human status exposes one concise sanitized high-level reason, for example:

```text
Status: Disconnected
Reason: VPN connection could not be restored safely
```

The primary reason must not require knowledge of routing tables, transaction projections, nftables, command envelopes, resolver internals, or ownership tokens.

Use a small stable typed reason classification. Never parse arbitrary internal error prose to manufacture the product reason.

The stable requirement is about the user-facing classification/wording, not about turning all historical diagnostics into persistent product state. Detailed evidence and saved TUN reports remain available through `doctor`, `recover`, and daemon diagnostic surfaces.

### Autostart display

Human status should make policy scope clear, preferably:

```text
Autostart: Enabled for next boot
```

or `Autostart: Disabled`.

If the current boot already consumed an attempt, the line still describes persistent next-boot policy; it does not imply a same-boot reconnect will occur.

## Compatibility of detailed status

Simplify normal human rendering, not the underlying diagnostic evidence model.

Do not remove detailed fields from daemon status merely to reduce terminal output. Add/derive product-oriented lifecycle fields compatibly while retaining current operator/diagnostic information needed by tests and tooling.

`podlaz status --json` is currently deferred. This issue must not accidentally establish an unreviewed JSON schema. Any new machine-readable CLI contract requires explicit versioning and tests.

## Connect/disconnect human output

Successful explicit connect becomes concise, for example:

```text
Connected
Profile: Example VPN
Mode: TUN
```

Successful disconnect becomes:

```text
Disconnected
```

Default success output does not print routine implementation detail such as route state, DNS state, firewall state, runtime config path, transaction identity, or network command output.

Errors similarly lead with stable product meaning. Engineering detail belongs in diagnostics.

## Recovery UX

`recover` remains an engineering/operator/emergency surface.

Normal successful connect, disconnect, daemon restart, crash recovery, package upgrade, suspend/resume, DHCP/Wi-Fi churn, and exact-owned reconciliation must not instruct the user to run manual Linux repair commands when automatic exact-owned convergence is possible.

Primary product guidance may point to a safe read-only diagnostic command such as `podlaz doctor --tun` after failure. `recover --execute`, `ip`, `resolvectl`, `nft`, NetworkManager/systemd restarts, and similar repair procedures remain outside normal workflow.

Existing detailed recovery state and warnings remain available for operators.

## Failure and malformed-state policy

### Invalid persistent manifest

A malformed, unsupported, oversized, permission-invalid, or semantically invalid manifest fails closed:

- no automatic connect;
- no networking mutation;
- product state remains `Disconnected` with a sanitized configuration problem when appropriate;
- detailed parse/storage evidence stays out of normal human output;
- manifest contents are never logged.

### Attempt-state corruption

A malformed or ambiguous current-boot attempt record never grants permission to start a fresh automatic connection.

Fail closed. Detailed operator diagnostics may be required, but ordinary networking must not be damaged.

A valid attempt belonging to a different boot is stale by authoritative `boot_id` evidence and may be removed before evaluating current-boot policy.

### Manifest changes during a boot

`autostart enable`/`disable` changes future boot policy. It does not reset current-boot consumed attempt authority and does not automatically connect/disconnect/cancel the current lifecycle.

If a current-boot attempt is `in_progress`, restart continues its pinned admitted request even if the persistent manifest has since changed or been disabled.

The next boot evaluates the latest valid persistent manifest.

## Testing strategy

### Persistent manifest tests

Cover:

- absence means disabled;
- strict schema validation;
- opaque generation changes on update;
- `configured_boot_id` next-boot eligibility;
- minimal validated connect snapshot round-trip;
- no subscription/history/UI-only data;
- atomic replace and unlink durability semantics;
- private permissions;
- bounded file size;
- malformed/trailing/unknown-field rejection;
- no secret-bearing values in rendered errors/log assertions.

### Boot logical-attempt tests

Deterministically cover:

1. ordinary explicit connect plus reboot boundary with autostart disabled -> no fresh connect, `Disconnected`;
2. enabling autostart during boot A plus daemon restart in boot A -> no automatic connect;
3. same manifest observed in later boot B -> one fresh normal connect lifecycle admitted;
4. successful boot connect -> normal `Connected` and attempt=`succeeded`;
5. terminal boot connect failure -> exact cleanup, `Disconnected`, attempt=`terminal`, no retry;
6. daemon restart without reboot while an ordinary connection is active -> existing session continues regardless of autostart setting;
7. crash after attempt=`in_progress` but before Network Session continuation exists -> restart continues the exact pinned logical request once;
8. manifest changed/disabled during that crash window -> current attempt still continues pinned request; new policy applies next boot;
9. crash after continuation exists -> continuation wins and no competing boot connect is created;
10. explicit disconnect after successful autostart -> restart in same boot does not reconnect;
11. terminal runtime failure after prior autostart success -> no same-boot automatic reconnect;
12. terminal initial autostart failure followed by daemon restart -> no automatic reconnect;
13. next boot after terminal failure -> eligible for one new attempt if a valid manifest remains present;
14. disabled autostart -> no attempt file is created;
15. corrupt/ambiguous current-boot attempt -> fail closed without duplicate connect authority;
16. valid previous-boot attempt -> discarded/ignored by boot identity before current policy evaluation.

### Serialization/race tests

Prove:

- autostart admission is serialized with explicit connect/disconnect/recovery/shutdown;
- startup continuation and fresh autostart cannot both enter lifecycle mutation;
- autostart cannot bypass startup recovery gate;
- shutdown after autostart admission preserves normal restart/stop intent semantics;
- no second logical attempt appears through daemon `Restart=on-failure`;
- attempt state transitions and Network Session creation cannot lose the one-logical-attempt invariant across crash boundaries.

### UX contract tests

Cover:

- primary human status uses stable product lifecycle state plus concise profile/mode/autostart information;
- `revalidating`/bounded reconciliation maps to `Reconnecting`, not terminal failure;
- terminal teardown maps to `Disconnected` with a stable high-level reason;
- detailed diagnostics remain inspectable separately;
- successful connect/disconnect output omits transaction/routing/DNS/firewall/runtime-config detail;
- normal successful automatic recovery emits no manual Linux repair guidance;
- redaction remains consistent.

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
- installed-package acceptance.

### Installed-package acceptance

The package gate exercises real service boundaries for:

- boot boundary with autostart off;
- boot boundary with autostart on;
- successful autostart connection;
- no same-boot autostart when policy was only just enabled;
- terminal autostart failure with no same-boot retry;
- daemon restart while connected independent of autostart;
- package upgrade while connected;
- explicit disconnect;
- one terminal runtime failure;
- concise primary status and preserved detailed diagnostics.

Use only repository-controlled example data and sanitized E2E seams.

## Security and privacy implications

The new durable secret-bearing state is the minimal boot connect manifest. This is necessary because the privileged packaged daemon cannot read user-owned XDG profile storage after boot under the existing hardening model.

The volatile `in_progress` attempt also carries a minimal pinned request solely to preserve crash-consistent admission before normal Network Session continuation exists. It has the same private handling requirements and is removed by reboot.

Neither artifact broadens network ownership. They authorize only entry into the already-defined normal Connect lifecycle. All actual networking authority remains subject to exact ownership, collision avoidance, Privacy Envelope, verification, evidence-driven reconciliation, and bounded terminal teardown.

The dedicated polkit action protects policy mutation. Stored connection material is never exposed through primary status.

No new daemon capabilities or systemd hardening relaxations are required.

## Documentation/product contract

After implementation the canonical documentation consistently states:

```text
connect now
-> remains across daemon restart/crash/package upgrade in the current boot
-> does not imply reboot reconnect

autostart enable
-> configures a later boot, not an immediate/same-boot daemon-restart connect

autostart enabled on a later boot
-> at most one new logical normal Connect lifecycle is admitted
-> crash/restart may continue that same lifecycle
-> success+explicit-disconnect or terminal failure never grants another same-boot automatic connect
```

Normal user documentation leads with import/profile/connect/disconnect/status/autostart. `doctor` and `recover` remain available but are not presented as routine network maintenance.

## Non-goals

- no general-purpose settings framework;
- no daemon-side user profile database;
- no user-home scanning by root daemon;
- no persistence of current Network Session continuation across reboot;
- no special boot networking implementation;
- no automatic reconnect loop after terminal failure;
- no foreign VPN discovery/control;
- no persisted legacy handoff policy in manifest/attempt;
- no removal of detailed diagnostics;
- no unversioned new CLI JSON status contract.

## Acceptance criteria

The implementation is complete when all are true:

- reboot reconnect occurs only from a valid explicit Boot Autostart Manifest configured before that boot;
- enabling autostart does not cause a later daemon restart in the same boot to connect;
- ordinary connection continuation remains strictly same-boot and independent of autostart policy;
- startup continuation/recovery always precedes fresh autostart admission;
- one boot admits at most one logical automatic lifecycle;
- crash before or after Network Session continuation can continue that same lifecycle without losing or duplicating it;
- an admitted request cannot silently change because future autostart policy changed during a crash window;
- explicit disconnect after autostart success and terminal failure both suppress further same-boot automatic connects;
- autostart enters the canonical normal Connect/Network Session path and inherits #259-#262 safety;
- default human status/connect/disconnect output is product-oriented and concise;
- terminal failure presents stable high-level meaning without leaking internal evidence;
- detailed diagnostics and operator recovery surfaces remain available;
- CLI docs, man pages, completions, polkit policy, package/service behavior, and tests describe one consistent lifecycle contract.
