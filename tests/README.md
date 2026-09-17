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
Nothing through Phase 1.2 needs that yet; every requirement so far is covered
at the package level:

- [`internal/config`](../internal/config/config_test.go) — configuration
  loading and validation, for both the Control Plane and the Node Agent
- [`internal/cluster`](../internal/cluster) — the node state machine,
  registration/heartbeat/failure-detection rules (unit, with a fake
  repository) and the real SQL behind them (integration, against PostgreSQL)
- [`internal/database`](../internal/database/migrate_test.go) — migrations
- [`internal/controlplane`](../internal/controlplane) — HTTP handlers for
  `/health`, `/ready`, and every `/nodes*` route
- [`internal/nodeagent`](../internal/nodeagent) — local identity, machine
  discovery, the Control Plane HTTP client, retry backoff, and the agent's
  orchestration loop
- [`internal/cli`](../internal/cli) — the `nimbus` CLI's HTTP client and
  table formatting

Run all of them with:

```bash
go test ./...
go test -race ./...
```
