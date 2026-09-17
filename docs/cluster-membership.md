# Cluster membership

This document describes Phase 1.2: node registration, heartbeats, failure
detection, and recovery. It assumes you've read
[`docs/architecture.md`](architecture.md) for the Phase 1.1 foundation this
builds on.

## Architecture

```
                    Nimbus Control Plane
                            │
          ┌─────────────────┼─────────────────┐
          │                 │                 │
   Registration API   Heartbeat API     Node Query API
   POST /nodes/register  POST /nodes/{id}/heartbeat   GET /nodes, GET /nodes/{id}
          │                 │                 │
          └─────────────────┼─────────────────┘
                            │
                   internal/cluster.Service
                            │
                            ▼
                  internal/cluster.Repository
                            │
                            ▼
                        PostgreSQL
                       (`nodes` table)

                            ▲
                            │ sweeps on a ticker
                            │
                 internal/cluster.Monitor
              (Control Plane background goroutine)
```

A `Node Agent` (`cmd/node-agent`) is a separate process — one per physical or
simulated node — that calls the Registration and Heartbeat APIs. It never
talks to PostgreSQL. Neither does the `nimbus` CLI:

```
Node Agent  ──POST /nodes/register, POST /nodes/{id}/heartbeat──▶  Control Plane
nimbus CLI  ──────────────────GET /nodes──────────────────────▶  Control Plane
```

**PostgreSQL is the only authoritative store of cluster membership.** There
is no in-memory membership list anywhere in the Control Plane — every
request in the diagram above reads or writes the `nodes` table directly
(see `internal/cluster/repository.go`). A Control Plane restart therefore
never loses track of which nodes are registered, what state they're in, or
when they last heartbeated; `GET /nodes` after a restart returns exactly what
it returned before.

## Layering

Following the existing Phase 1.1 pattern (`internal/controlplane` depending
on `health.Checker` rather than a concrete database handle), the node HTTP
handlers depend on a narrow `NodeService` interface, not the concrete
`*cluster.Service`:

```
internal/controlplane/nodes.go  (HTTP: parsing, status codes, JSON)
              │  depends on NodeService interface
              ▼
internal/cluster.Service         (registration/heartbeat/failure-detection rules)
              │  depends on Repository interface
              ▼
internal/cluster.Repository      (SQL — the only package that issues any)
              │
              ▼
          PostgreSQL
```

No SQL exists outside `internal/cluster/repository.go`. This is what will let
a Phase 2 scheduler consume `cluster.Service` (node capacity, status) without
coupling itself to HTTP at all.

## Node model

```go
type Node struct {
    ID                  uuid.UUID
    Hostname            string
    Status              Status // "Registering" | "Ready" | "NotReady"
    OS, Architecture     string
    CPUCapacity          int64
    MemoryCapacityBytes  int64
    AgentVersion         string
    LastHeartbeatAt     *time.Time // nil only for a node that predates Phase 1.2 tooling
    RegisteredAt         time.Time
    UpdatedAt            time.Time
}
```

## Node identity

Every node has a **client-generated UUID**, never a hostname:

- The Node Agent generates its own `uuid.New()` the first time it runs, and
  persists it to `<NIMBUS_NODE_DATA_DIR>/identity.json`.
- Every subsequent run of that same agent (from the same data directory)
  loads that file and reuses the same ID — a crash-and-restart therefore
  updates the *same* database row rather than creating a new one.
- Every registration request includes this ID (`node_id`). The Control
  Plane's registration handler (`cluster.Service.Register`) treats a
  supplied `node_id` as "this exact node," whether it has seen it before or
  not — the very first registration for an ID and the hundredth look
  identical from the server's point of view (see **Idempotency** below).

This is why running several Node Agents on one development machine works:
each agent's `NIMBUS_NODE_DATA_DIR` holds a different `identity.json`, so
each has a different node ID, even though — because it's the same physical
machine — `runtime.GOOS`, `runtime.GOARCH`, and CPU/memory capacity are
identical across all of them. Only the *identity* and, optionally, the
*hostname reported* (see `NIMBUS_NODE_NAME` below) differ.

**Assumption: one agent process per data directory, at a time.** Identity
creation (`internal/nodeagent/identity.go`) is not safe against two agent
processes racing to initialize the *same* `NIMBUS_NODE_DATA_DIR`
concurrently — the file write is atomic (write-temp-then-rename), but the
"does `identity.json` already exist?" check and that write are two separate
steps, so two processes starting for the first time against one empty data
directory at the same instant could each generate a different UUID and the
last one to rename wins, leaving the two processes holding different
in-memory identities than what's on disk. This is not a supported
configuration — always give each agent its own data directory, as every
example in this document does — and Nimbus does not detect or warn about a
violation of that assumption.

**A caveat on identity ownership.** Because registration is authenticated by
nothing but the `node_id` value itself (see **Security baseline** below),
two agents that happen to present the *same* `node_id` — whether from a
copied `identity.json`, a bug, or deliberate misuse — are not detected as a
conflict. The second registration silently overwrites the first node's
hostname, OS, architecture, and capacity, exactly as an intentional
re-registration would (see **Idempotency**, above); Nimbus has no way to
tell "the same node restarted" apart from "a different node claimed this
ID." Resolving that requires agent authentication, which is explicitly
future-phase work.

### Hostname override for local simulation

`NIMBUS_NODE_NAME`, if set, replaces the real `os.Hostname()` value the agent
reports. This is the one place Nimbus distinguishes **node identity**
(the UUID, always unique) from **physical hostname** (which several
simulated agents on one machine would otherwise share) — see
[Local development](#local-development-multiple-agents) below.

## Node lifecycle

```
                 registration
                      │
                      ▼
               ┌────────────┐
               │ Registering│   (defined for completeness — see note below)
               └──────┬─────┘
                      │
                      ▼
                 ┌─────────┐
                 │  Ready  │◀────────────────┐
                 └────┬────┘                 │
                      │                 heartbeat received
             heartbeat timeout                │
             (interval × threshold)           │
                      │                       │
                      ▼                       │
                ┌──────────┐──────────────────┘
                │ NotReady │
                └──────────┘
```

**Design decision — registration goes straight to `Ready`.** A successful
`POST /nodes/register` call is itself proof the agent can reach the Control
Plane over the network — the same proof a heartbeat provides. Rather than
leave a node in `Registering` until some separate first heartbeat arrives
(introducing a window where a freshly-registered, perfectly healthy node
looks unready for no operational reason), `cluster.Service.Register` persists
the node directly as `Ready`, with `LastHeartbeatAt` set to the registration
time. `Registering` remains a valid, `CHECK`-constrained status in the schema
and the `Status` type — it exists so a future phase can insert an async
handshake step between "row created" and "considered live" without a schema
migration — but Phase 1.2's registration handler never produces it.

Setting `LastHeartbeatAt = RegisteredAt` also matters for failure detection:
it means every `Ready` node always has a non-nil `LastHeartbeatAt`, so the
membership monitor's timeout math (below) needs no special case for
"never heartbeated yet," and a node that registers and then dies before its
first real heartbeat gets a full timeout's grace period rather than being
flagged immediately.

### Idempotency (registration)

```
Agent starts → generates/loads node ID X → POST /nodes/register {node_id: X, ...}
    → Control Plane: row X doesn't exist → INSERT, RegisteredAt = now
Agent restarts → loads the *same* node ID X → POST /nodes/register {node_id: X, ...}
    → Control Plane: row X exists → UPDATE metadata, RegisteredAt UNCHANGED
```

This is implemented as one atomic `INSERT ... ON CONFLICT (id) DO UPDATE`
(`internal/cluster/repository.go`'s `Upsert`), not a `SELECT` followed by a
branch — so concurrent registration requests for the same ID (two agent
processes racing at startup, or a retried request arriving twice) converge
on one row instead of racing each other. `RegisteredAt` is excluded from the
`UPDATE SET` clause, so it is set once, at first insert, and never touched
again. Every other field — hostname, OS, architecture, CPU/memory capacity,
agent version — is overwritten on every re-registration; only the identity
(`id`) and its original `RegisteredAt` survive.

## Registration API

```
POST /nodes/register
```

Request:

```json
{
  "node_id": "5c9c4c0a-...-f0e21b7c9a41",
  "hostname": "worker-01",
  "os": "darwin",
  "architecture": "arm64",
  "cpu_capacity": 8,
  "memory_capacity_bytes": 17179869184,
  "agent_version": "0.1.0"
}
```

`node_id` is omitted only by a caller with no identity yet; the Control
Plane then generates one. The Node Agent, having its own persisted identity,
always sends it.

Response (`200 OK`):

```json
{
  "node_id": "5c9c4c0a-...-f0e21b7c9a41",
  "status": "Ready",
  "heartbeat_interval_seconds": 10
}
```

Validation (`400 Bad Request` on any failure, every problem reported at
once): `hostname`, `os`, `architecture`, `agent_version` must be non-empty;
`cpu_capacity` and `memory_capacity_bytes` must be greater than zero; a
supplied `node_id` must be a valid UUID. Validation runs in
`cluster.RegisterInput.Validate` — before anything touches PostgreSQL — and
its messages are safe to return to the client directly (they never include
SQL or driver text).

## Heartbeat API

```
POST /nodes/{id}/heartbeat
```

Request (body is entirely optional):

```json
{ "timestamp": "2026-09-15T12:00:00Z" }
```

Response (`200 OK`): `{"status": "Ready"}`. Unknown `id`: `404 Not Found`,
`{"error": "node not found"}` — **heartbeats never create a node**;
registration must happen first.

**The optional `timestamp` field is never the source of truth.** The Control
Plane always records `last_heartbeat_at` as *its own* clock reading at the
moment the request is handled (`cluster.Service.Heartbeat` calls `s.now()`,
not anything from the request body) — this is what makes failure detection
immune to clock skew between the Control Plane and however many machines are
running agents. A future phase may log or validate the agent-supplied
timestamp for diagnostics; it must never decide liveness.

A heartbeat unconditionally sets `status = Ready`. If the node had been
`NotReady`, this is the recovery transition, and it's the one heartbeat
outcome the Control Plane logs at `INFO` (`"node became Ready"`) — see
**Logging** below.

## Node query API

```
GET /nodes            → { "nodes": [ NodeDTO, ... ] }   — ordered by hostname ASC
GET /nodes/{id}        → NodeDTO, or 404 if unknown
```

Both always query PostgreSQL directly (`cluster.Service.List` /
`.Get` → `Repository`) — never a cached list — so they're accurate
immediately after a Control Plane restart. `NodeDTO` mirrors `Node` with
JSON-friendly field names; see `internal/clusterapi/types.go` for the exact
shape both the CLI and the Node Agent share with the Control Plane.

## Heartbeat timing and failure detection

Two settings, both on the Control Plane (`internal/config`'s `ClusterConfig`):

| Variable | Default | Meaning |
|---|---|---|
| `NIMBUS_HEARTBEAT_INTERVAL` | `10s` | How often agents are told to heartbeat, and the membership monitor's sweep cadence |
| `NIMBUS_HEARTBEAT_FAILURE_THRESHOLD` | `3` | Consecutive missed intervals allowed before a node is considered dead |

**Timeout calculation:** `timeout = HEARTBEAT_INTERVAL × FAILURE_THRESHOLD`
— 30 seconds with the defaults above. A node is never marked `NotReady` for
missing a single heartbeat; it takes a full `timeout` with *no* heartbeat at
all. This is deliberate: a single late heartbeat (a slow GC pause, a
momentary network blip) must not flap a node's status.

The Control Plane's `POST /nodes/register` response tells every agent the
current `heartbeat_interval_seconds` directly, so the interval only needs to
be correct in one place (the Control Plane's config) — agents adopt it
rather than needing their own copy kept in sync.

### The membership monitor

`internal/cluster/monitor.go`'s `Monitor` is the Control Plane's one
background goroutine. Every `HEARTBEAT_INTERVAL` tick, it runs one
failure-detection sweep:

```go
// conceptually, inside cluster.Service.DetectStaleNodes(ctx, now):
cutoff := now.Add(-timeout)
UPDATE nodes SET status = 'NotReady', updated_at = now
WHERE status = 'Ready' AND (last_heartbeat_at IS NULL OR last_heartbeat_at < cutoff)
RETURNING *   -- only the rows that actually changed
```

The `IS NULL` branch is defensive: no current code path ever persists a
`Ready` node with a NULL `last_heartbeat_at` (registration always sets it —
see **Node lifecycle**, above), but the column is nullable at the schema
level, and a plain `last_heartbeat_at < cutoff` comparison evaluates to
`NULL` (not true) against a NULL value in SQL — so without this clause, a
row that ever ended up in that state would be silently exempt from failure
detection forever, with no error or warning.

This is one atomic SQL statement, not a read-then-write — it can't race a
concurrent heartbeat for the same node (whichever write commits first wins;
Postgres' row-level locking under `UPDATE` makes the outcome well-defined
either way). `now` is always passed in as a parameter — `Service.now`
defaults to `time.Now` but is overridable, and `Monitor.sweep` takes `now`
explicitly — specifically so failure-detection timing can be tested with
fixed timestamps (`internal/cluster/service_test.go`,
`internal/cluster/monitor_test.go`) instead of a test sleeping through a real
30-second timeout.

**Logging discipline:** the monitor logs only the nodes that actually
transitioned (`INFO "node marked NotReady"`, with `node_id`, `hostname`,
`last_heartbeat_at`, `timeout`). An unchanged cluster produces zero log lines
per sweep — there is no "checking node..." noise, by design (see the
`sweep` implementation).

### Recovery

`NotReady → Ready` happens the instant that node's next heartbeat succeeds —
no separate reconciliation step, no waiting for the next monitor sweep. The
monitor only ever moves nodes *into* `NotReady`; `Heartbeat` is the only path
back to `Ready`.

## Unknown-node recovery (agent side)

If the Control Plane's database is reset (or a node's row is otherwise
gone), its heartbeats start getting `404 Not Found`. The Node Agent
(`internal/nodeagent/agent.go`) treats that specific response — and only
that response — as "my membership no longer exists," and re-registers
immediately using its same persisted identity, rather than waiting for the
next scheduled heartbeat. Because registration is idempotent by ID (above),
this re-creates the same node ID cleanly. An ordinary heartbeat failure
(network error, `500`, timeout) is *not* treated this way — the agent simply
retries on the next regular interval, per **Retries** below; re-registering
on every transient failure would otherwise spam the Control Plane.

## Retries (Node Agent)

Both initial registration and the unknown-node re-registration path use a
bounded exponential backoff (`internal/nodeagent/backoff.go`): 1s, 2s, 4s,
8s, ... capped at 30s, no jitter (deterministic, and Phase 1.2 has at most a
handful of agents — a thundering herd is not a concern this phase needs to
solve). The agent never busy-loops against an unreachable Control Plane. An
ordinary heartbeat failure is simply left for the next regular tick — it is
not retried within the same interval.

## Graceful shutdown

**Control Plane** (`cmd/control-plane/main.go`), on `SIGINT`/`SIGTERM`:

```
stop accepting requests (http.Server.Shutdown)
        │
        ▼
cancel the membership monitor's context
        │
        ▼
wait for its goroutine to actually return (sync.WaitGroup)
        │
        ▼
close the database
```

This extends the existing Phase 1.1 shutdown sequence rather than
introducing a second one — the monitor's cancellation and wait are just two
more steps in the same `run()` function that already stops the HTTP server
and closes the database.

**Node Agent** (`cmd/node-agent/main.go`), on `SIGINT`/`SIGTERM`: the
heartbeat ticker's `select` sees the cancelled context and returns
immediately; any in-flight HTTP call is aborted via the same context (Go's
`net/http` cancels a request whose context is done); no goroutine is left
running. Phase 1.2 does not implement a deregistration call on shutdown — an
agent that stops simply goes `NotReady` once its heartbeat is missed for a
full timeout, the same as if it had crashed. That is an intentional scope
boundary, not an oversight.

## Local development: multiple agents {#local-development-multiple-agents}

```bash
NIMBUS_NODE_DATA_DIR=/tmp/nimbus-node-a NIMBUS_NODE_NAME=node-a \
  go run ./cmd/node-agent &

NIMBUS_NODE_DATA_DIR=/tmp/nimbus-node-b NIMBUS_NODE_NAME=node-b \
  go run ./cmd/node-agent &

NIMBUS_NODE_DATA_DIR=/tmp/nimbus-node-c NIMBUS_NODE_NAME=node-c \
  go run ./cmd/node-agent &
```

Each gets its own identity file (different `NIMBUS_NODE_DATA_DIR`) and its
own reported hostname (different `NIMBUS_NODE_NAME`), so they register as
three distinct, independently-tracked nodes even though they're all the same
physical machine talking to the same Control Plane. See
[`docs/development.md`](development.md) for the full walkthrough, including
the failure/recovery test.

## Database restart / Control Plane restart

- **Control Plane restart:** every node record, and every node's current
  status, is exactly as PostgreSQL left it — `GET /nodes` before and after
  match. The membership monitor resumes sweeping on the new process's first
  tick.
- **PostgreSQL restart (volume intact):** no data loss; same as above.
- **PostgreSQL data destroyed** (volume removed, or the `nodes` table
  otherwise emptied): every node's next heartbeat gets `404`, which — per
  **Unknown-node recovery** above — triggers automatic re-registration.
  Agents recover cluster membership on their own within one heartbeat
  interval, with no manual intervention.

## Logging

Structured, via the existing `log/slog` setup (`internal/logging`). What's
logged at `INFO`: `"node registered"`, `"node became Ready"` (recovery only,
not every heartbeat), `"node marked NotReady"` (monitor transitions only),
`"node agent started"`, `"node agent registered"`. Deliberately **not**
logged at `INFO`: individual successful heartbeats, or unchanged nodes during
a monitor sweep — both would produce one log line every
`HEARTBEAT_INTERVAL` per node for no operational value.

## Security baseline

The node API is **intentionally unauthenticated** in Phase 1.2, as scoped —
authentication is future-phase work (see the root
[README](../README.md#roadmap)). What Phase 1.2 does do: every input is
validated before touching the database; every query is parameterized
(`database/sql`'s placeholder syntax throughout `internal/cluster/repository.go`
— no string-built SQL); no database error, connection string, or credential
ever reaches an HTTP response (`internal/controlplane/nodes.go` logs the real
error server-side and returns a fixed, generic message); and the CLI's own
error handling (`internal/cli.FriendlyError`) never prints a raw dial error
or stack trace to the terminal. Do not expose an unauthenticated Nimbus
Control Plane beyond a trusted local/development network.
