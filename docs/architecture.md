# Architecture

This document describes the Nimbus architecture **as it exists today**
(through Phase 1.2) and, separately, how it is expected to grow. Nothing
under "Planned" is implemented — it exists only to explain why the current
code is organized the way it is.

## Current architecture

```
                 Developer / nimbus CLI
                          │
                          ▼
                Nimbus Control Plane
        (cmd/control-plane, internal/controlplane)
                          │
              ┌───────────┼───────────┐
              │  registration/heartbeat│
              ▼           │           ▼
        Node Agent A      │      Node Agent B  ...
   (cmd/node-agent, internal/nodeagent)
                          │
                          ▼
                     PostgreSQL
             (deployments/docker-compose.yml)
```

The **control plane** is one binary and one process; there is exactly one of
it (see **No premature distribution**, below). Each **node agent** is a
separate binary/process — one per physical machine in a real deployment, or
one per simulated node on a single development machine (see
[`docs/cluster-membership.md`](cluster-membership.md#local-development-multiple-agents)).
The **`nimbus` CLI** is a third, even smaller binary that only ever reads
from the control plane's HTTP API.

On startup, the control plane:

1. Loads configuration from environment variables (`internal/config`).
2. Initializes structured logging (`internal/logging`).
3. Connects to PostgreSQL and verifies connectivity (`internal/database`).
4. Applies database migrations (`internal/database.Migrate`) — safe to
   re-run on every startup; see **Database** below.
5. Serves HTTP (`internal/controlplane`):
   - `GET /health`, `GET /ready` — liveness/readiness (Phase 1.1).
   - `POST /nodes/register`, `POST /nodes/{id}/heartbeat`, `GET /nodes`,
     `GET /nodes/{id}` — cluster membership (Phase 1.2). See
     [`docs/cluster-membership.md`](cluster-membership.md) for the full
     contract.
6. Starts the membership monitor (`internal/cluster.Monitor`) — its one
   background goroutine, sweeping for nodes that stopped heartbeating.
7. Shuts down gracefully on `SIGINT`/`SIGTERM`: stops accepting new
   requests, lets in-flight requests finish, cancels and waits for the
   membership monitor, closes the database, and logs each step.

A node agent, on startup: loads or creates its local identity, discovers its
machine's OS/architecture/CPU/memory, registers with the control plane
(retrying transient failures with bounded backoff), then heartbeats on a
ticker until it's told to stop. See
[`docs/cluster-membership.md`](cluster-membership.md) for the full lifecycle,
including how it recognises and recovers from the control plane forgetting
about it.

### Components

| Package | Responsibility |
|---|---|
| `cmd/control-plane` | Control plane entry point: builds dependencies and wires them together. No business logic. |
| `cmd/node-agent` | Node agent entry point: same role, for the agent process. |
| `cmd/nimbus` | CLI entry point: parses `node list`, calls the control plane's API, prints a table. |
| `internal/config` | Reads and validates *all* environment-derived configuration — for both the control plane (`Load`) and the node agent (`LoadNodeAgent`) — in one place. |
| `internal/logging` | Builds the `log/slog` logger used everywhere. |
| `internal/database` | Owns the PostgreSQL connection pool (connect, ping-based check, close) and the migration runner. |
| `internal/database/dbtest` | Test-only helper: provisions an isolated, migrated PostgreSQL database for integration tests. Not imported by any production binary. |
| `internal/health` | Defines the `Checker` interface the readiness probe depends on. |
| `internal/cluster` | Cluster-membership domain: the `Node` model and state machine, `Repository` (all SQL), `Service` (registration/heartbeat/failure-detection rules), `Monitor` (the background sweep). See [`docs/cluster-membership.md`](cluster-membership.md). |
| `internal/clusterapi` | The JSON wire contract (`RegisterRequest`, `NodeDTO`, ...) shared by the control plane's handlers, the node agent's client, and the CLI — one definition, not three independently-drifting copies. |
| `internal/controlplane` | HTTP server, routing, and handlers (health/ready + nodes); owns its own start/shutdown lifecycle. Depends on `cluster.Service` only through the narrow `NodeService` interface. |
| `internal/nodeagent` | Node agent behaviour: local identity (`identity.go`), machine discovery (`machineinfo*.go`, split by build tag per platform), the Control Plane HTTP client (`client.go`), retry backoff (`backoff.go`), and the orchestrating `Agent` (`agent.go`). |
| `internal/cli` | The `nimbus` CLI's HTTP client and terminal-table formatting — talks to the control plane's API only, never PostgreSQL. |

## Database

One table, `nodes` (see [`docs/cluster-membership.md`](cluster-membership.md#node-model)
for the full schema), created by `internal/database/migrations/0001_create_nodes.sql`.
Migrations are embedded into the control plane binary (`//go:embed`) and
applied by `internal/database.Migrate` on every startup: a `schema_migrations`
table tracks which have already run, each migration applies inside its own
transaction, and a PostgreSQL advisory lock serialises the whole process
against another Nimbus instance migrating the same database concurrently.
This is a small, hand-rolled runner rather than an external migration
framework — Phase 1.2 has exactly one migration to run.

## Why this is modular

Each future capability in the roadmap (see the root [README](../README.md))
is naturally a new `internal/` package plus, where it needs an HTTP surface,
new routes registered in `internal/controlplane`. The design choices that
make that possible without disturbing existing code:

- **`internal/controlplane` depends on interfaces, not concrete types** —
  `health.Checker` for `GET /ready`, `NodeService` for the `/nodes*` routes.
  Both let the HTTP layer be tested with a stub and no PostgreSQL, and let a
  future dependency (a scheduler's queue, say) be added to a handler without
  changing how existing ones are wired.
- **No SQL outside `internal/cluster/repository.go`.** `cluster.Service`
  depends on the `Repository` *interface*; a Phase 2 scheduler can depend on
  `cluster.Service` for node capacity and status without coupling itself to
  HTTP, PostgreSQL, or even knowing SQL exists.
- **`internal/clusterapi` is the one shared wire contract.** The control
  plane, the node agent, and the CLI all import it rather than each
  hand-rolling their own copy of the same JSON shape — a field renamed there
  fails to compile everywhere it matters, instead of silently drifting.
- **`cmd/*/main.go` files only construct and wire dependencies.** Adding a
  component means constructing it in the relevant `main.go` and passing it
  to whatever already-existing piece needs it — none of them accumulate
  business logic over time.

## No premature distribution

Phase 1.2 deliberately keeps exactly **one** Control Plane, **one**
PostgreSQL, and **many** Node Agents. There is no leader election, no
consensus protocol (Raft or otherwise), and no support for multiple Control
Plane instances — those are real architectural decisions for a later phase,
not something to back into accidentally now.

## Planned architecture (not implemented)

Future phases are expected to add, roughly in this order:

- **Phase 2 — Workload deployment & scheduling**: a scheduler component that
  consumes `cluster.Service` to place containerized workloads on registered,
  `Ready` nodes.
- **Phase 3 — Self-healing & reliability**: a reconciliation loop that
  compares desired vs. actual workload state (the node-level health and
  membership tracked since Phase 1.2 is a building block for this, not this
  itself).
- **Phase 4 — Networking & observability**: service discovery, load
  balancing, and metrics/tracing.
- **Phase 5 — Advanced features & release**: deployment strategies (rolling,
  blue/green, etc.) and scaling.

None of container scheduling, workload placement, replicas, reconciliation,
self-healing, service discovery, load balancing, a dashboard, authentication,
or autoscaling exist in the codebase today. They are listed here only to
explain the intent behind the current package boundaries.
