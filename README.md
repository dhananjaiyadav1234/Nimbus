# Nimbus

Nimbus is a lightweight, self-hosted distributed container orchestration
platform. Its long-term goal is to let you deploy, manage, schedule, monitor,
and recover containerized workloads across multiple machines, without relying
on a large existing orchestrator.

Nimbus is conceptually similar to modern container orchestration systems, but
it is not, and does not aim to be, a Kubernetes reimplementation.

## Current status: Phase 2.1 — Workload & Container Runtime

**Phase 2.1 persists desired deployments and introduces the container
runtime abstraction. Scheduling and automatic placement are not yet
implemented.**

**Implemented**

- Foundation *(Phase 1.1)*: modular Go codebase, a runnable **Nimbus Control
  Plane** HTTP service, PostgreSQL via Docker Compose, centralized
  environment-variable configuration, structured logging (`log/slog`),
  graceful `SIGINT`/`SIGTERM` shutdown, `GET /health` / `GET /ready`.
- Cluster membership *(Phase 1.2)*: a **Node Agent** (`cmd/node-agent`)
  discovers its machine, registers with a stable agent-persisted node ID,
  and heartbeats; the Control Plane persists membership in PostgreSQL,
  detects failed nodes, and recovers them — see
  [`docs/cluster-membership.md`](docs/cluster-membership.md).
- ✓ **Deployment model** — a `Deployment` (name, image, replicas, CPU,
  memory) is Nimbus's representation of desired workload state, persisted
  in PostgreSQL with a name that's guaranteed unique even under concurrent
  creation (enforced by a database constraint, not application logic).
- ✓ **Deployment manifests** — a small YAML/JSON contract
  (`apiVersion: nimbus/v1`, `kind: Deployment`) submitted via
  `nimbus deploy -f`, validated both client-side (fast, obvious mistakes)
  and server-side (the actual source of truth).
- ✓ **Deployment API** — `POST/GET/DELETE /deployments`,
  `GET /deployments/{id}` on the Control Plane; `409 Conflict` on a
  duplicate name, never a silent duplicate.
- ✓ **Container runtime abstraction** — `internal/runtime.ContainerRuntime`,
  implemented against a real Docker Engine
  (`internal/runtime/docker`, via Docker's official Go client — never a
  shelled-out `docker` command) and independently integration-tested.
  **Not yet connected to deployment creation** — there is no scheduler yet
  to decide which node should run anything; see
  [`docs/workloads.md`](docs/workloads.md) for exactly what this does and
  does not mean today.
- ✓ **CLI** — `nimbus node list`, `nimbus deployment list`,
  `nimbus deploy -f <file>`, all talking to the Control Plane's HTTP API
  only — never PostgreSQL, never Docker, directly.

**Coming next**

- Scheduling: placing deployments onto nodes and creating containers for
  them (Phase 2.2)
- Desired-state reconciliation and self-healing (Phase 3)
- Service discovery, load balancing, observability (Phase 4)
- Scaling and advanced deployment strategies (Phase 5)

See [`docs/architecture.md`](docs/architecture.md) for a precise
current-vs-planned breakdown, [`docs/cluster-membership.md`](docs/cluster-membership.md)
for cluster membership, and [`docs/workloads.md`](docs/workloads.md) for
deployments and the container runtime.

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
              (nodes + deployments)

  Node Agent ── ContainerRuntime ── Docker Engine
  (implemented, tested, not yet connected to the above)
```

See [`docs/architecture.md`](docs/architecture.md) for component-level detail
and how future phases are expected to extend this.

### Getting started

See [`docs/development.md`](docs/development.md) for exact, copy-pasteable
commands to clone the repo, start PostgreSQL, run the Control Plane, run
several Node Agents to simulate a small cluster, watch a node fail and
recover, submit a deployment manifest with the `nimbus` CLI, and run the
test suite (including the Docker-backed container runtime tests). Everything
runs locally — no cloud account, API key, or paid service is required.

## Roadmap

| Phase | Focus |
|---|---|
| **Phase 1 — Foundation & Cluster** | Project foundation *(1.1, done)*, cluster and node management *(1.2, done)* |
| **Phase 2 — Workload Deployment & Scheduling** | Workload model & container runtime *(2.1, done — this repo is here)*, resource-aware scheduling *(2.2, next)* |
| Phase 3 — Self-Healing & Reliability | Desired-state reconciliation, self-healing |
| Phase 4 — Networking & Observability | Service discovery, load balancing, observability |
| Phase 5 — Advanced Features & Release | Scaling, advanced deployment strategies |

## License

No license has been chosen yet.
