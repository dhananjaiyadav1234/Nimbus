# Scheduling

This documents Phase 2.2: the scheduler that decides which node each
deployment replica runs on, and persists that decision. See
[`docs/workloads.md`](workloads.md) for Phase 2.1 (the deployment model and
container runtime this phase builds on) and
[`docs/architecture.md`](architecture.md) for how this fits the system as a
whole.

## Overview

Phase 2.1 gave Nimbus a `Deployment` — a user's desired workload — and a
`ContainerRuntime` abstraction capable of creating containers, but nothing
connecting the two: no component decided *where* a deployment's replicas
should run. Phase 2.2 is that component.

The scheduler answers exactly one question:

> Given a deployment and the cluster's current state, where should each
> replica be placed?

It answers it by computing a **placement decision** — a set of (replica
index, node) assignments, each with a resource reservation — and persisting
that decision in PostgreSQL. It does not create a container. It does not
talk to Docker, or to a Node Agent, or to anything outside the Control
Plane. See "Scope boundary" below.

## Architecture

```
Deployment (desired state, Phase 2.1)
        │
        ▼
    Scheduler (Phase 2.2)
        │
        ▼
  Placement Decision
        │
        ▼
     PostgreSQL
  (deployment_placements)
```

Separately, and **not connected to the above**:

```
Node Agent
    │
    ▼
Container Runtime (internal/runtime, Phase 2.1)
    │
    ▼
Docker Engine
```

`internal/scheduler` depends on `internal/deployment` (to read a
deployment's desired replica count and resource request) and reads the
`nodes` table directly for Ready-node capacity — it does **not** depend on
`internal/cluster`'s Go types, `internal/runtime`, `internal/runtime/docker`,
or anything in `github.com/moby/moby/...`. See "Docker" below and
`internal/controlplane`'s own package doc for how the HTTP layer wires
`SchedulerService` alongside `NodeService`/`DeploymentService`.

## Node eligibility

Only nodes with `status = 'Ready'` are scheduling candidates:

```
Ready        → eligible
Registering  → excluded
NotReady     → excluded
```

This is not a second node-status system: the scheduler's repository reads
the exact same `nodes.status` column `internal/cluster` owns, filtered in
the same SQL statement that locks those rows for the scheduling transaction
(see "Concurrency" below) — there is no separate notion of readiness
anywhere in `internal/scheduler`.

## Resource accounting

Every Ready node has a CPU and memory **capacity** (set at registration,
`internal/cluster`) and, at scheduling time, some amount already
**allocated** to existing placements:

```
available CPU    = CPU capacity    - allocated CPU
available memory = memory capacity - allocated memory
```

A node is eligible for a replica only when **both** resources fit:

```
available CPU    >= requested CPU
available memory >= requested memory
```

**Allocation is cluster-wide, not per-deployment.** A node's "allocated CPU"
is the sum of `cpu` across *every* placement on that node, from *every*
deployment — not just the one currently being scheduled. A deployment being
scheduled a second time (see "Idempotency") still sees its own
already-placed replicas' reservations, exactly as it sees every other
deployment's. This is computed once, inside the scheduling transaction, by
summing `deployment_placements` grouped by `node_id`
(`internal/scheduler/repository.go`'s `clusterWideAllocation`).

All accounting arithmetic (`capacity - allocated`, `allocated + requested`)
is overflow-checked (`internal/scheduler/resources.go`); an operation that
would not fit in an `int64` is rejected rather than silently wrapping.

## Scheduling algorithm

**Deterministic best-fit / least-waste.**

For each replica still missing a placement, in ascending replica-index
order:

1. Filter to Ready nodes (already done before the algorithm runs — see
   above).
2. Reject any node that cannot fit the request (`NodeState.CanFit`).
3. Score every remaining candidate:

   ```
   remainingCPU    = availableCPU    - requestedCPU
   remainingMemory = availableMemory - requestedMemory

   cpuRatio    = remainingCPU    / node's total CPU capacity
   memoryRatio = remainingMemory / node's total memory capacity

   score = cpuRatio + memoryRatio
   ```

   Ratios (not raw remaining amounts) are used so nodes of very different
   sizes are compared fairly: 1 CPU free out of 2 is a tighter fit than 1
   CPU free out of 64, even though the raw numbers are identical. Lower
   score wins — it means less headroom is left behind, i.e. the tightest
   fit.
4. Pick the lowest-scoring candidate. Reserve the request against that
   node's *working* state (see "Multiple replicas" below) and record the
   placement.
5. Continue to the next replica.

If no candidate can be found for any replica, scheduling fails with
`ErrInsufficientCapacity` and **nothing is persisted** — see "Atomicity."

### Worked example

```
node-a: CPU 4, Memory 4 GiB
node-b: CPU 4, Memory 4 GiB
node-c: CPU 2, Memory 2 GiB

deployment "web": 3 replicas, 1 CPU / 512Mi each
```

All three nodes can fit the first replica. Scores (remaining ratios,
summed):

```
node-a: (4-1)/4 + (4096-512)/4096 = 0.75 + 0.875 = 1.625
node-b: same as node-a = 1.625
node-c: (2-1)/2 + (2048-512)/2048 = 0.5  + 0.75   = 1.25   <- lowest, wins
```

Replica 0 → node-c. node-c's working state is now CPU 1/2, memory
1536Mi/2048Mi available. Replica 1 is scored again against the *updated*
state — node-c no longer has the lowest score (it's now tighter than
node-a/node-b for a 1 CPU / 512Mi request in a different way; in this
example node-a and node-b, both still at their original size, score lower
than node-c's shrunk headroom would for another 1 CPU request relative to
node-c's now-smaller capacity fraction). node-a and node-b remain tied with
each other; the deterministic tie-break (below) picks whichever has the
lexicographically smaller UUID. Replica 2 goes to the other of node-a/b, or
back to node-c if it still has room — the exact final assignment depends on
each node's actual UUID, which is why this document shows the *method*,
not a hard-coded outcome.

## Determinism

Given identical inputs (same nodes, same capacities, same existing
placements, same deployment), `decidePlacements`
(`internal/scheduler/scheduler.go`) always produces the same output. This
holds because the algorithm:

- Never iterates a Go map when order matters (node state is carried in
  slices throughout).
- Never depends on goroutine completion order (it has no goroutines).
- Never uses a random number.
- Sorts its node list once, by ID ascending, before scoring anything, and
  every subsequent sort (`sort.SliceStable` on score) is *stable*, so equal
  scores never reorder relative to that fixed starting order.
- Relies on `time.Now()` only for the timestamp stamped onto new placement
  rows, never for ordering or scoring decisions — and even that is injected
  (`Service.now`, mirroring `cluster.Service`/`deployment.Service`) so tests
  control it explicitly.

### Tie-break

Two candidates with an identical score are broken by **ascending node
UUID** (string comparison). Because the working node list is sorted by ID
ascending before scoring, and the score-sort is stable, this falls out of
the sort itself rather than needing an explicit UUID comparison inside the
scoring function — see `selectBestFit`'s doc comment.

## Multiple replicas

A single `Schedule` call may need to place several replicas at once. Each
placement updates a **working copy** of node state
(`NodeState.Reserve`, which returns a new value rather than mutating) before
the next replica is considered — so replica 1 sees the node exactly as
replica 0 left it, not the original, pre-scheduling state. `decidePlacements`
threads this working state through its loop explicitly; nothing about it is
implicit or re-derived from the database mid-loop.

## Atomicity

If any replica in a scheduling attempt cannot be placed, the **entire
attempt fails** and **zero new placements are written** — not the replicas
that could have fit before the failing one.

This holds at two levels:

- `decidePlacements` itself: on failure it returns `nil, error`, never a
  partial slice (see `scheduler.go`).
- `Repository.Schedule`: the entire read (locking nodes and the
  deployment), decide call, and write happen inside one PostgreSQL
  transaction. A `decide` error means the transaction is rolled back before
  any `INSERT` runs; nothing is ever half-committed.

## Idempotency

Calling `Schedule` again for a deployment whose replicas are all already
placed is a **successful no-op**: it returns the existing placements
unchanged and creates nothing new. This is enforced at two independent
levels:

1. **Application logic**: `decidePlacements` only ever considers replica
   indexes missing from `existing`; if none are missing, it returns an
   empty slice immediately.
2. **Database constraint**: `deployment_placements` has
   `UNIQUE (deployment_id, replica_index)`. Even if the application logic
   above had a bug, PostgreSQL itself would refuse a duplicate row for the
   same replica.

The row-locking strategy below (see "Concurrency") is what makes (1) safe
to rely on as the primary mechanism rather than racing toward (2) as a
"let the constraint sort it out" approach — (2) exists as a last-resort
guarantee, not the everyday code path.

## Partial scheduling

A deployment can be scheduled once, receive only some of its replicas (say,
because capacity was tight), and be scheduled again later once more
capacity exists. The second call places only the still-missing replica
indexes; the first call's placements are **never recreated or moved** —
same row, same ID, same `created_at`. Nimbus does not reconcile or rebalance
existing placements — see "Node failures" below.

## Concurrency

**The invariant that matters most:** Nimbus must never persist a placement
state that exceeds a node's CPU or memory capacity, even when multiple
`Schedule` calls run concurrently — for the same deployment, or for
different deployments competing for the same node.

`Repository.Schedule` (`internal/scheduler/repository.go`) enforces this
with PostgreSQL row locks, not an application-level mutex:

1. **Lock the deployment row**: `SELECT id FROM deployments WHERE id = $1
   FOR UPDATE`. This serializes every concurrent `Schedule` call for the
   *same* deployment — only one such transaction can hold this lock at a
   time — and, as a side effect, detects a deleted/unknown deployment.
2. **Lock every Ready node row**, in a fixed order: `SELECT id, ... FROM
   nodes WHERE status = 'Ready' ORDER BY id ASC FOR UPDATE`. This is always
   the *complete* current Ready set, never a caller-chosen subset, because
   the algorithm must be free to consider any Ready node for any replica.
   Locking this set is what serializes concurrent `Schedule` calls for
   *different* deployments that might otherwise both read "4 CPU available"
   and both reserve 3.
3. Only after both locks are held does the transaction read cluster-wide
   allocation (`SUM` over `deployment_placements`) and call `decide` — so
   that read reflects every prior transaction's committed writes, and no
   other transaction can write a conflicting placement until this one
   commits or rolls back.
4. Insert the new placements and commit, releasing both locks.

PostgreSQL's default **READ COMMITTED** isolation is sufficient here
specifically *because* of the explicit row locks above — the locks, not the
isolation level, are what prevent the classic "read 4, read 4, reserve 3,
reserve 3, now 6 is allocated on a 4-CPU node" race. A stricter isolation
level (`SERIALIZABLE`) was deliberately not used: it would add retry-on
serialization-failure complexity for a guarantee the row locks already
provide.

This design deliberately serializes *every* `Schedule` call against every
other one that touches any Ready node — which, in the worst case, is all of
them, since any node could be a candidate for any deployment. This is a
correctness-first, simplicity-first choice appropriate to Nimbus's scale
target (see [`docs/architecture.md`](architecture.md#no-premature-distribution));
a cluster with many nodes and heavy concurrent scheduling traffic would
eventually want a less coarse-grained locking scheme, but that is future
work, not a Phase 2.2 requirement — see "Known limitations."

### Deadlock avoidance

Every `Schedule` transaction acquires locks in the same fixed order:
**the deployment row, then every Ready node row ascending by ID.** Two
transactions can never deadlock by acquiring these in opposite orders,
because there is only one order used anywhere in this codebase — the
classic "always lock resources in a global, consistent order" idiom. Two
transactions scheduling different deployments but overlapping on some Ready
nodes simply queue behind each other in that same order; there is no cycle
to form. `TestPostgresRepositoryConcurrentScheduleOfSameDeploymentProducesExactlyOnePlacementPerReplica`
and `TestPostgresRepositoryConcurrentScheduleAcrossDeploymentsNeverOverAllocatesNode`
(`internal/scheduler/repository_test.go`) each launch 20 concurrent
`Schedule` calls against a real PostgreSQL database and assert the
invariant above holds — both pass cleanly under `go test -race`.

## Node failures

**Phase 2.2 does not automatically reschedule workloads when nodes fail.**
If a node goes `NotReady` (Phase 1.2's existing failure detection), its
existing placements are left exactly as they are — Nimbus does not move
them, does not create replacement placements elsewhere, and does not
retry. Building that behavior requires comparing desired state against
*actual* running-container state, which does not exist yet (Phase 2.1
creates no containers; see [`docs/workloads.md`](workloads.md)). That
comparison, and the reconciliation loop that would act on it, is Phase 3.

## Docker

**Phase 2.2 produces placement decisions only. It does not execute
containers.** A placement is a row saying "replica N of deployment D is
reserved on node X" — nothing about `internal/scheduler` creates, starts,
stops, or inspects a container, and nothing in it imports
`internal/runtime`, `internal/runtime/docker`, or
`github.com/moby/moby/...`. Verified directly:

```bash
grep -Rni "moby/moby\|runtime/docker\|os/exec\|exec.Command" internal/scheduler internal/controlplane
```

returns no matches. The `ContainerRuntime` abstraction built in Phase 2.1
is real, tested, and ready — but nothing calls it yet. Connecting a
placement to an actual container creation call on the right Node Agent is
deliberately left for a future phase, so that boundary can be designed
deliberately rather than bolted onto the scheduler.

## API

```
POST /deployments/{id}/schedule      run scheduling for a deployment
GET  /deployments/{id}/placements    inspect its current placement decision
```

`POST /deployments` (Phase 2.1) remains persistence-only — creating a
deployment never triggers scheduling; a client must call
`POST /deployments/{id}/schedule` explicitly.

`POST /deployments/{id}/schedule` response (`200 OK`):

```json
{
  "deploymentId": "5c9c4c0a-...-f0e21b7c9a41",
  "placements": [
    { "replicaIndex": 0, "nodeId": "...", "cpu": 1, "memoryBytes": 536870912 }
  ]
}
```

Status codes: `200` success (including the idempotent no-op case), `404`
unknown deployment, `409` insufficient cluster capacity, `400` malformed
deployment ID, `500` unexpected internal error (never leaking SQL text,
stack traces, or connection details — see
`internal/controlplane/scheduler.go`).

`GET /deployments/{id}/placements` response (`200 OK`, same shape, possibly
an empty `placements` array if scheduling hasn't run yet): `404` unknown
deployment, `400` malformed ID.

## CLI

```bash
nimbus deployment schedule <name|id>
nimbus deployment placements <name|id>
```

Both accept either a deployment's name or its ID; a name is resolved to an
ID via `GET /deployments` (the same endpoint `deployment list` already
uses) before calling the ID-based scheduling routes — this is the CLI's
only scheduling-adjacent logic, an HTTP-level convenience, not scheduling
logic itself. Like every other `nimbus` command, these talk to the Control
Plane's HTTP API only — never PostgreSQL, never Docker.

## Scope boundary

Phase 2.2 is responsible for exactly:

```
Deployment desired state → Scheduler → Placement decision → Persisted placement
```

It is explicitly **not** responsible for, and contains no code for:

- Creating, starting, stopping, or inspecting a Docker container (Node
  Agent/`ContainerRuntime` boundary — built in Phase 2.1, connected in a
  later phase).
- Reconciliation, desired-vs-actual comparison, automatic container
  restart, or automatic rescheduling after a node failure (Phase 3).
- Service discovery, DNS, load balancing, or any networking (Phase 4).
- Autoscaling, rolling deployments, rollback, or authentication/RBAC
  (Phase 5).

## Known limitations

- **Coarse-grained locking**: every `Schedule` call locks every Ready node,
  not just the ones it ends up using — see "Concurrency" above. Correct and
  simple; not the most concurrent design possible at much larger scale.
- **No rebalancing**: once placed, a replica's placement never moves except
  by a future phase's explicit action. A cluster that grows more nodes
  after a deployment was tightly packed onto fewer nodes stays exactly as
  packed until something re-schedules it (which nothing does yet).
- **Best-fit, not spread**: the scoring model deliberately minimizes
  leftover headroom (packs tightly) rather than spreading replicas across
  nodes for failure-isolation. A future phase may want a spread-aware
  scoring option; Phase 2.2 does not attempt to guess at that requirement
  now.
