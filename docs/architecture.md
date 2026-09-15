# Architecture

This document describes the Nimbus architecture **as it exists today**
(Phase 1.1) and, separately, how it is expected to grow. Nothing under
"Planned" is implemented — it exists only to explain why the current code is
organized the way it is.

## Current architecture

```
Developer
   |
   v
Nimbus Control Plane  (cmd/control-plane, internal/controlplane)
   |
   v
PostgreSQL  (deployments/docker-compose.yml)
```

A single binary, the **control plane**, is the entire system in this phase.
It:

1. Loads configuration from environment variables (`internal/config`).
2. Initializes structured logging (`internal/logging`).
3. Connects to PostgreSQL and verifies connectivity (`internal/database`).
4. Serves two HTTP endpoints (`internal/controlplane`):
   - `GET /health` — liveness; confirms the HTTP server itself is running.
   - `GET /ready` — readiness; confirms PostgreSQL is reachable.
5. Shuts down gracefully on `SIGINT`/`SIGTERM`: stops accepting new requests,
   lets in-flight requests finish (bounded by a timeout), closes the database
   connection, and logs each step.

### Components

| Package | Responsibility |
|---|---|
| `cmd/control-plane` | Process entry point: builds dependencies and wires them together. No business logic. |
| `internal/config` | Reads and validates all environment-derived configuration in one place. |
| `internal/logging` | Builds the `log/slog` logger used everywhere. |
| `internal/database` | Owns the PostgreSQL connection pool: connect, ping-based check, close. |
| `internal/health` | Defines the `Checker` interface the readiness probe depends on. |
| `internal/controlplane` | HTTP server, routing, and handlers; owns its own start/shutdown lifecycle. |

There are no application tables, migrations, or repositories yet — Phase 1.1
only establishes that the control plane *can* reach PostgreSQL reliably.

## Why this is modular

Each future capability in the roadmap (see the root [README](../README.md))
is naturally a new `internal/` package plus, where it needs an HTTP surface,
new routes registered in `internal/controlplane`. Two design choices make
that possible without disturbing Phase 1.1 code:

- **`internal/controlplane` depends on `health.Checker`, an interface, not a
  concrete database type.** `GET /ready` can gain more dependencies (a future
  scheduler's queue, an agent registry, etc.) by checking additional
  `Checker` implementations, without changing how the database is wired.
- **`internal/database` exposes its underlying `*sql.DB` via `DB.SQL()`.**
  A future migrations step or repository layer can be built as sibling files
  in that package, or new packages that take a `*database.DB`, instead of
  changing how connections are established.
- **`cmd/control-plane/main.go` only constructs and wires dependencies.**
  Adding a component (for example, a future node registry or scheduler) means
  constructing it in `main.go` and passing it to whatever already-existing
  piece needs it — main.go does not accumulate business logic over time.

## Planned architecture (not implemented)

Future phases are expected to add, roughly in this order:

- **Phase 2 — Workload deployment & scheduling**: a scheduler component and
  the ability to run containerized workloads on registered nodes.
- **Phase 3 — Self-healing & reliability**: a reconciliation loop that
  compares desired vs. actual state and a node agent that reports node
  health.
- **Phase 4 — Networking & observability**: service discovery, load
  balancing, and metrics/tracing.
- **Phase 5 — Advanced features & release**: deployment strategies (rolling,
  blue/green, etc.) and scaling.

None of node agents, heartbeats, cluster membership, scheduling, service
discovery, or load balancing exist in the codebase today. They are listed
here only to explain the intent behind the current package boundaries.
