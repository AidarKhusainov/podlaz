# Architecture

## 1. Architectural goal

TunWarden must separate unprivileged user interaction from privileged Linux networking operations.

The architecture must make high-impact operations explicit, observable, reversible, and testable.

The early architecture has two execution modes:

1. **Proxy-only mode:** starts and supervises Xray without changing system routes, DNS, firewall, or TUN state.
2. **TUN full-tunnel mode:** applies Linux networking changes only through the daemon-owned transaction model.

The current foundation TUN work implements read-only planning, transaction-state persistence, and a daemon-owned executor slice for TUN interface, route, policy-rule, systemd-resolved DNS, and TunWarden-owned nftables mutation. User-visible `connect --mode tun` must not claim an active full-tunnel connection until the daemon can generate and verify a real TUN-mode Xray runtime config and basic connectivity; starting proxy-only Xray config under TUN mode is forbidden.

## 2. High-level components

```text
+-----------------------+
| tunwarden CLI         |
| unprivileged user     |
+-----------+-----------+
            |
            | Unix socket / D-Bus API
            v
+-----------------------+
| tunwardend            |
| privileged daemon     |
+-----------+-----------+
            |
            +----------------------------+
            |                            |
            v                            v
+-----------------------+      +-----------------------+
| Network Orchestrator  |      | Core Engine Manager   |
| routes/DNS/TUN/nft    |      | Xray, later others    |
+-----------+-----------+      +-----------+-----------+
            |                              |
            v                              v
+-----------------------+      +-----------------------+
| Linux system state    |      | Core processes        |
| iproute2/resolved/nft |      | xray                  |
+-----------------------+      +-----------------------+
```

## 3. Process model

### 3.1 CLI

The CLI must be unprivileged.

Responsibilities:

- parse user commands;
- render status and diagnostics;
- manage user-owned configuration and state;
- submit selected user intent to the daemon;
- print plans and errors;
- collect and render local read-only snapshots where explicitly allowed;
- never directly mutate routes, DNS, nftables, or TUN state.

### 3.2 Daemon

The daemon must run under systemd and own privileged runtime behavior.

Responsibilities:

- validate user requests;
- own active connection state;
- manage core process lifecycle;
- perform network transactions;
- handle recovery;
- expose a restricted local API.

The daemon should be the only long-lived owner of privileged mutable state.

### 3.3 Core process

Xray should be treated as a child engine process, not as the application supervisor.

The core must not be the only holder of network state. TunWarden must know what system-level changes were applied.

Core process safety requirements are owned by [State and security requirements](./state-and-security.md).

### 3.4 Current code layout

The current foundation build uses this package layout:

```text
cmd/tunwarden              user-facing CLI entrypoint
cmd/tunwardend             daemon entrypoint
internal/app/cli           CLI command dispatch
internal/app/daemon        daemon process skeleton
internal/api               shared API contracts
internal/client            CLI-side daemon client adapters
internal/daemon            daemon coordination
internal/doctor            safe local diagnostics
internal/engine            core engine lifecycle coordination
internal/logs              read-only journald/system-log integration
internal/network           transaction and network planning model
internal/network/planner   pure network planning logic
internal/network/snapshot  read-only host networking snapshot model and collectors
internal/network/executor  narrow platform adapters
internal/profile           normalized VPN profile model and user-owned profile storage
internal/recovery          recovery plan and future cleanup behavior
internal/render            CLI output rendering helpers
internal/service           daemon-owned product orchestration
internal/state             runtime and durable state ownership helpers
internal/sub               subscription source model
```

This layout is expected to evolve, but the CLI/daemon boundary and planner/snapshot/executor split should remain stable architectural constraints.

Package dependency direction is owned by [Package boundaries](./package-boundaries.md).

## 4. Privilege boundary

TunWarden must not use a SUID GUI/client binary as the primary privilege model.

Preferred model:

```text
systemd service: tunwardend.service
user command: tunwarden
IPC: Unix socket or D-Bus
optional authorization: polkit
```

The daemon API must be intentionally small.

Initial API operations:

- `Status()`
- `PlanConnect(profile_id, mode)`
- `Connect(profile_id, mode)`
- `Disconnect()`
- `Reconnect()`
- `Doctor(scope)`
- `RecoverPlan()`
- `RecoverExecute()`
- `ListProfiles()`
- `ImportProfile(source)`
- `ImportSubscription(source)`
- `Import(source)`

## 5. State model

TunWarden must distinguish three levels of state:

1. **User intent/state:** profiles, subscriptions, preferences, selected defaults, and import metadata.
2. **Daemon runtime/state:** active connection snapshot, locks, generated runtime config, child process state, and transaction state.
3. **System networking state:** TUN interfaces, routes, rules, DNS link configuration, and nftables state.

The canonical filesystem layout and ownership rules are defined in [State and security requirements](./state-and-security.md).

Important constraints:

- User intent must not be hidden only in daemon-private directories.
- Daemon runtime state must be enough to recover without the original CLI process.
- System networking state must be identifiable as TunWarden-owned.
- Generated core config is runtime output, not persistent source of truth.

### Logs

Use journald as the primary logging destination.

Logs must follow the redaction policy in [State and security requirements](./state-and-security.md).

## 6. Snapshot model

System snapshots are read-only inputs to planners. The snapshot package may inspect default routes, server route, DNS backend visibility, NetworkManager advisory state, nftables availability, known TunWarden TUN device names, and stale TunWarden-owned resources.

Snapshot collection must not create TUN devices, mutate routes, mutate DNS, mutate nftables/firewall state, start or stop processes, or write runtime files.

The implemented `plan --mode tun` command consumes this snapshot layer and remains read-only. Actual TUN interface, route, policy-rule, systemd-resolved DNS, and TunWarden-owned nftables mutation is performed only by daemon-owned executor/transaction code; the CLI never mutates host networking directly. User-visible TUN connect remains gated on a real TUN-mode core runtime config.

The canonical snapshot contract is owned by [System snapshot model](./system-snapshot.md).

## 7. Transaction model

All full-tunnel network changes must happen through a transaction object.

Proxy-only mode does not need a network transaction because it must not modify system networking. It still needs process lifecycle state for Xray supervision and recovery.

The implemented transaction persistence schema is:

```text
Transaction
  schema_version: tunwarden.transaction.v1
  owner: tunwarden
  id
  profile_id
  mode
  state: planned | applying | applied | verifying | committed | rolling_back | rolled_back | failed
  created_at
  updated_at
  before_snapshot
  desired_plan
  applied_steps
  rollback
  health_result
  failure_reason
  labels
```

The implemented runtime path is:

```text
/run/tunwarden/transactions/<id>.json
```

The transaction file is volatile daemon runtime state. It stores enough non-secret rollback metadata to plan cleanup after daemon restart for TunWarden-owned TUN devices, routes, policy rules, DNS state, nftables state, generated config files, and child processes. It must not store persistent secrets.

Required flow:

```text
1. Build plan
2. Acquire global network lock
3. Snapshot relevant state
4. Write pending transaction to /run/tunwarden/transactions/<id>.json
5. Apply steps in deterministic order
6. Verify health
7. Commit transaction
8. Mark committed or leave enough state for restart inspection
```

If verification fails:

```text
1. Mark transaction as rolling_back
2. Execute rollback steps in reverse order
3. Verify direct connectivity if possible
4. Mark rolled_back or failed
```

On daemon startup:

```text
1. Read /run/tunwarden/transactions/*.json
2. Detect pending, failed, or rolling-back transaction state
3. Detect stale TunWarden-owned system state
4. Expose pending/stale state through status, doctor, and recover
5. Never assume previous daemon shutdown was clean
```

The current implementation adds transaction persistence, transition helpers, startup scan primitives, daemon status summaries, local `status`/`doctor`/`recover` visibility, and daemon-owned apply/verify/rollback for TUN devices, routes, policy rules, systemd-resolved DNS, and TunWarden-owned nftables state. It must still add real TUN-mode Xray runtime config generation and basic connectivity verification before a user-visible full-tunnel `connect --mode tun` flow may commit as an active connection.

## 8. Planner/executor split

Network logic must be split into snapshots, planners, and executors.

### Snapshot

Read-only code. Does not require root and must degrade gracefully when optional host tools are missing.

Inputs:

- host OS/platform;
- profile server hostname or IP;
- optional test runner/resolver fakes.

Output:

- current default route/interface observations;
- route to the VPN server candidate;
- DNS/NetworkManager/nftables observations;
- known TunWarden-owned resources;
- visibility warnings.

### Planner

Pure or mostly pure code. Does not require root.

Inputs:

- current system snapshot;
- profile;
- daemon settings;
- platform capabilities.

Output:

- desired network plan;
- ordered apply steps;
- ordered rollback steps;
- warnings.

The current TUN planner produces inspectable desired state for TUN, routes, policy rules, systemd-resolved DNS, nftables/firewall, and kill-switch behavior. `TunDNSPlan.Servers` is planner-owned desired state; executors must not choose DNS servers themselves.

Planner output must be inspectable through `tunwarden plan` before mutation.

### Executor

Privileged code. Executes a validated plan.

Executors:

- `TunExecutor`,
- `RouteExecutor`,
- `DnsExecutor`,
- `FirewallExecutor`,
- `CoreExecutor`,
- `NetworkManagerExecutor`.

Executor implementations must be narrow and auditable. They should not contain hidden planning decisions. The current executor slice applies TUN, routes, policy rules, systemd-resolved DNS, and TunWarden-owned nftables state from the already-inspected plan, but user-visible full-tunnel connect remains blocked until a real TUN-mode core runtime config and connectivity verification are implemented.

## 9. Engine abstraction

TunWarden starts as Xray-first but should not make networking depend on Xray internals.

```text
VpnEngine
  GenerateConfig(profile, runtime_network_state) -> EngineConfig
  Start(config) -> EngineHandle
  Stop(handle) -> StopResult
  Health(handle) -> EngineHealth
  Logs(handle) -> LogStream
```

Initial engine:

- `XrayEngine`

Future engines:

- `AmneziaWgEngine`,
- `SingBoxEngine` if needed for compatibility/testing.

Future possible engines must implement the same lifecycle boundary without changing the network transaction model.

## 10. Network backends

TunWarden must support backend interfaces rather than hard-coding one environment.

```text
RouteBackend
  iproute2 implementation

DnsBackend
  systemd-resolved implementation
  resolvconf implementation later
  raw resolv.conf fallback only as last resort

FirewallBackend
  nftables implementation
  iptables fallback later only if needed

NetworkEventBackend
  NetworkManager implementation
  rtnetlink implementation
  systemd sleep hook implementation
```

## 11. Configuration generation

Generated core config must be treated as runtime output, not as the source of truth.

Source of truth:

```text
TunWarden profile model
TunWarden routing policy
TunWarden DNS policy
TunWarden runtime state
```

Generated files may live under:

```text
/run/tunwarden/generated/
```

Generated config permissions, atomic writes, and logging rules are owned by [State and security requirements](./state-and-security.md).

## 12. Error handling principles

- Prefer explicit failure over silent partial success.
- Every failed apply step must include the command/operation, stderr, and rollback impact.
- Cleanup must be idempotent.
- Core crashes must not imply system networking cleanup was completed.
- NetworkManager limited connectivity must not automatically be treated as connection failure.
- Proxy-only failure must not trigger full network cleanup unless stale TunWarden-owned network state is detected separately.

## 13. systemd service hardening

The daemon service must start from least privilege. The canonical hardening requirements are defined in [State and security requirements](./state-and-security.md).

A privileged daemon release is blocked until the unit file documents the final hardening choices and justifies any relaxation from the documented baseline.

## 14. Testing architecture

Required test layers:

1. Unit tests for profile parsing and normalization.
2. Unit tests for route/DNS/firewall planners.
3. Unit tests for read-only snapshot collection and fake snapshots.
4. Unit tests for engine config generation.
5. Integration tests in Linux network namespaces.
6. VM tests for Ubuntu/Debian/Fedora.
7. Suspend/resume simulation where possible.
8. Failure injection tests:
   - core crash,
   - daemon crash,
   - DNS apply failure,
   - route apply failure,
   - nft apply failure,
   - Wi-Fi/default route change.

## 15. Future GUI rule

A future GUI must be a client of the daemon API.

It must not:

- run as root,
- directly modify system networking,
- own the connection lifecycle independently from the daemon,
- become required for recovery.
