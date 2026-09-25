# tests/

Nimbus keeps tests next to the code they cover (`*_test.go` files inside each
package under `internal/`), which is the idiomatic Go layout and keeps a test
in sync with the package it exercises. Within each package, unit tests (no
external dependencies) and integration tests (against a real PostgreSQL
database, via [`internal/database/dbtest`](../internal/database/dbtest)) sit
side by side — see [`docs/development.md`](../docs/development.md) for how to
run each kind.

This directory is reserved for tests that don't belong to a single package —
for example, a black-box end-to-end suite that starts the actual
`control-plane` and `node-agent` binaries against a real PostgreSQL instance.
Nothing through Phase 2.2 needs that yet; every requirement so far is covered
at the package level:

- [`internal/config`](../internal/config/config_test.go) — configuration
  loading and validation, for both the Control Plane and the Node Agent
- [`internal/cluster`](../internal/cluster) — the node state machine,
  registration/heartbeat/failure-detection rules (unit, with a fake
  repository) and the real SQL behind them (integration, against PostgreSQL)
- [`internal/deployment`](../internal/deployment) — manifest/resource
  validation, the `Deployment` service (unit, with a fake repository), and
  the real SQL behind it, including the mandatory concurrent-creation test
  (integration, against PostgreSQL — see
  [`docs/workloads.md`](../docs/workloads.md))
- [`internal/scheduler`](../internal/scheduler) — the deterministic
  best-fit algorithm (unit), resource-accounting overflow safety (unit),
  and the real transactional locking behind it, including the mandatory
  concurrent-scheduling tests — same deployment and across different
  deployments — proving no over-allocation and no duplicate placements
  under 20-goroutine concurrency (integration, against PostgreSQL — see
  [`docs/scheduling.md`](../docs/scheduling.md))
- [`internal/database`](../internal/database/migrate_test.go) — migrations,
  for the `nodes`, `deployments`, and `deployment_placements` tables
- [`internal/controlplane`](../internal/controlplane) — HTTP handlers for
  `/health`, `/ready`, every `/nodes*` route, every `/deployments*` route,
  and every `/deployments/{id}/schedule` and `/deployments/{id}/placements`
  route
- [`internal/nodeagent`](../internal/nodeagent) — local identity, machine
  discovery, the Control Plane HTTP client, retry backoff, and the agent's
  orchestration loop
- [`internal/runtime`](../internal/runtime) — the `ContainerRuntime`
  interface's own contract (unit, via `FakeRuntime`) — and
  [`internal/runtime/docker`](../internal/runtime/docker) — the same
  contract against a real Docker Engine (integration; skips cleanly if
  Docker isn't reachable, exactly like the PostgreSQL integration tests)
- [`internal/cli`](../internal/cli) — the `nimbus` CLI's HTTP client, YAML
  manifest parsing, and table formatting

Run all of them with:

```bash
go test ./...
go test -race ./...
```

The PostgreSQL and Docker integration tests both skip themselves — with a
clear printed reason, never a silent pass — if their respective server isn't
reachable; see [`docs/development.md`](../docs/development.md) for how to
start each one.
