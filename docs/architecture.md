# Architecture

## 1. Architectural goal

TunWarden must separate unprivileged user interaction from privileged Linux networking operations.

The architecture must make high-impact operations explicit, observable, reversible, and testable.

The early architecture has two execution modes:

1. **Proxy-only mode:** starts and supervises Xray without changing system routes, DNS, firewall, or TUN state.
2. **TUN full-tunnel mode:** applies Linux networking changes only through the transaction model.

The current foundation TUN work implements read-only planning plus transaction-state persistence. It does not yet apply TUN/full-tunnel networking changes.

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

- parse user commands,
- render status and diagnostics,
- manage user-owned configuration and user state through documented paths,
- submit selected user intent to daemon,
- print plans and errors,
- collect and render local read-only snapshots where explicitly allowed by the foundation build,
- never directly mutate routes, DNS, nftables, or TUN state.

### 3.2 Daemon

The daemon must run under systemd and own privileged runtime behavior.

Responsibilities:

- validate user requests,
- manage privileged operations,
- own active connection state,
- manage core process lifecycle,
- perform network transactions,
- handle recovery,
- expose a restricted local API.

The daemon should be the only long-lived owner of privileged mutable state.

### 3.3 Core process

Xray should be treated as a child engine process, not as the application supervisor.

Responsibilities:

- execute proxy protocols,
- apply generated runtime config,
- expose logs/stats if configured,
- terminate cleanly on daemon request.

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

In the foundation build, `internal/app/cli` may call user-owned profile storage, local read-only proxy-only planning, local read-only TUN snapshot planning, local read-only diagnostics, read-only system-log inspection, and dry-run recovery planning directly. Privileged or daemon-owned behavior must move behind the daemon client/API boundary once it is implemented.

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

The CLI may provide:

```bash
tunwarden logs
tunwarden logs --follow
tunwarden logs --core
tunwarden logs --network
```

Logs must follow the redaction policy in [State and security requirements](./state-and-security.md).

## 6. Snapshot model

System snapshots are read-only inputs to planners. The snapshot package may inspect default routes, server route, DNS backend visibility, NetworkManager advisory state, nftables availability, known TunWarden TUN device names, and stale TunWarden-owned resources.

Snapshot collection must not create TUN devices, mutate routes, mutate DNS, mutate nftables/firewall state, start or stop processes, or write runtime files.

The currently implemented `plan --mode tun` command consumes this snapshot layer and renders a read-only full-tunnel TUN/route dry-run. It is not the same thing as future execution. Actual TUN mutation must still be performed only by daemon-owned executor/transaction code.

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

The current implementation adds transaction persistence, transition helpers, startup scan primitives, daemon status summaries, and local `status`/`doctor`/`recover` visibility. It does not yet apply route, DNS, nftables, TUN, or firewall mutations.

## 8. Planner/executor split

Network logic must be split into snapshots, planners, and executors.

### Snapshot

Read-only code. Does not require root and must degrade gracefully when optional host tools are missing.

Inputs:

- host OS/platform,
- profile server hostname or IP,
- optional test runner/resolver fakes.

Output:

- current default route/interface observations,
- route to the VPN server candidate,
- DNS/NetworkManager/nftables observations,
- known TunWarden-owned resources,
- visibility warnings.

### Planner

Pure or mostly pure code. Does not require root.

Inputs:

- current system snapshot,
- profile,
- daemon settings,
- platform capabilities.

Output:

- desired network plan,
- ordered apply steps,
- ordered rollback steps,
- warnings.

The current TUN dry-run is not yet the final executable network plan. Future TUN planner work must consume the snapshot and produce intended DNS, nftables, kill-switch, and health-check behavior in addition to the already visible TUN/route desired state.

Planner output must be inspectable through `tunwarden plan`.

### Executor

Privileged code. Executes a validated plan.

Executors:

- `TunExecutor`,
- `RouteExecutor`,
- `DnsExecutor`,
- `FirewallExecutor`,
- `CoreExecutor`,
- `NetworkManagerExecutor`.

Executor implementations must be narrow and auditable. They should not contain hidden planning decisions.

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

- Xray.

Future possible engines must implement the same lifecycle boundary without changing the network transaction model.
