# Nimbus

Nimbus is a lightweight, self-hosted distributed container orchestration
platform. Its long-term goal is to let you deploy, manage, schedule, monitor,
and recover containerized workloads across multiple machines, without relying
on a large existing orchestrator.

Nimbus is conceptually similar to modern container orchestration systems, but
it is not, and does not aim to be, a Kubernetes reimplementation.

## Current status: Phase 1.2 — Node Registration & Cluster Membership

**Implemented**

- Foundation *(Phase 1.1)*: modular Go codebase, a runnable **Nimbus Control
  Plane** HTTP service, PostgreSQL via Docker Compose, centralized
  environment-variable configuration, structured logging (`log/slog`),
  graceful `SIGINT`/`SIGTERM` shutdown, `GET /health` / `GET /ready`.
- ✓ **Node registration** — a **Node Agent** (`cmd/node-agent`) discovers its
  machine and registers with the Control Plane; registration is idempotent
  by a stable, agent-persisted node ID (never a hostname).
- ✓ **Node heartbeats** — agents heartbeat on a Control-Plane-assigned
  interval; the Control Plane records its own receipt time, immune to clock
  skew between machines.
- ✓ **Cluster membership** — every node's identity, status, and capacity is
  persisted in PostgreSQL (`internal/cluster`), the sole source of truth; a
  Control Plane restart never loses it.
- ✓ **Node failure detection** — a background monitor marks a node
  `NotReady` after a configurable number of missed heartbeat intervals.
- ✓ **Node recovery** — a `NotReady` node's next successful heartbeat moves
  it straight back to `Ready`.
- ✓ **Basic node CLI** — `nimbus node list` (`cmd/nimbus`), reading from the
  Control Plane's API only.

**Coming next**

- Deployment scheduling and container lifecycle management (Phase 2)
- Desired-state reconciliation and self-healing (Phase 3)
- Service discovery, load balancing, observability (Phase 4)
- Scaling and advanced deployment strategies (Phase 5)

See [`docs/architecture.md`](docs/architecture.md) for a precise
current-vs-planned breakdown and [`docs/cluster-membership.md`](docs/cluster-membership.md)
for exactly how registration, heartbeats, and failure detection work.

### Architecture (current)

```
                 Developer / nimbus CLI
                          │
                          ▼
                Nimbus Control Plane
                          │
              ┌───────────┼───────────┐
              ▼           │           ▼
        Node Agent A      │      Node Agent B  ...
                          │
                          ▼
                     PostgreSQL
```

See [`docs/architecture.md`](docs/architecture.md) for component-level detail
and how future phases are expected to extend this.

### Getting started

See [`docs/development.md`](docs/development.md) for exact, copy-pasteable
commands to clone the repo, start PostgreSQL, run the Control Plane, run
several Node Agents to simulate a small cluster, watch a node fail and
recover, use the `nimbus` CLI, and run the test suite. Everything runs
locally — no cloud account, API key, or paid service is required.

## Roadmap

| Phase | Focus |
|---|---|
| **Phase 1 — Foundation & Cluster** | Project foundation *(1.1, done)*, cluster and node management *(1.2, done — this repo is here)* |
| Phase 2 — Workload Deployment & Scheduling | Container deployment, resource-aware scheduling |
| Phase 3 — Self-Healing & Reliability | Desired-state reconciliation, self-healing |
| Phase 4 — Networking & Observability | Service discovery, load balancing, observability |
| Phase 5 — Advanced Features & Release | Scaling, advanced deployment strategies |

## License

No license has been chosen yet.
