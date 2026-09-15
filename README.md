# Nimbus

Nimbus is a lightweight, self-hosted distributed container orchestration
platform. Its long-term goal is to let you deploy, manage, schedule, monitor,
and recover containerized workloads across multiple machines, without relying
on a large existing orchestrator.

Nimbus is conceptually similar to modern container orchestration systems, but
it is not, and does not aim to be, a Kubernetes reimplementation.

## Current status: Phase 1.1 — Project Foundation

The current implementation provides a clean, production-quality development
foundation and nothing more:

- A modular Go codebase (`cmd/`, `internal/...`)
- A runnable **Nimbus Control Plane** HTTP service
- PostgreSQL running locally via Docker Compose, with connectivity verified
  at startup
- Centralized, environment-variable-based configuration
- Structured logging (`log/slog`)
- Graceful shutdown on `SIGINT`/`SIGTERM`
- `GET /health` — liveness probe
- `GET /ready` — readiness probe (checks database connectivity)
- Unit tests for configuration, the HTTP handlers, and the server lifecycle

No cluster, scheduling, workload execution, node agents, self-healing,
service discovery, load balancing, or authentication exist yet — those are
future-phase work. See [`docs/architecture.md`](docs/architecture.md) for a
precise current-vs-planned breakdown.

### Architecture (current)

```
Developer
   |
   v
Nimbus Control Plane
   |
   v
PostgreSQL
```

See [`docs/architecture.md`](docs/architecture.md) for component-level detail
and how future phases are expected to extend this.

### Getting started

See [`docs/development.md`](docs/development.md) for exact, copy-pasteable
commands to clone the repo, start PostgreSQL, configure environment
variables, run the control plane, exercise `/health` and `/ready`, and run
the test suite. Everything runs locally — no cloud account, API key, or paid
service is required.

## Roadmap

| Phase | Focus |
|---|---|
| **Phase 1 — Foundation & Cluster** | Project foundation *(this repo is at 1.1)*, then cluster and node management |
| Phase 2 — Workload Deployment & Scheduling | Container deployment, resource-aware scheduling |
| Phase 3 — Self-Healing & Reliability | Desired-state reconciliation, self-healing |
| Phase 4 — Networking & Observability | Service discovery, load balancing, observability |
| Phase 5 — Advanced Features & Release | Scaling, advanced deployment strategies |

## License

No license has been chosen yet.
