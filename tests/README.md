# tests/

Nimbus keeps unit tests next to the code they cover (`*_test.go` files inside
each package under `internal/`), which is the idiomatic Go layout and keeps a
test in sync with the package it exercises.

This directory is reserved for tests that don't belong to a single package —
for example, black-box integration or end-to-end suites that start the control
plane binary and a real PostgreSQL instance. Phase 1.1 has no such suite yet;
its automated tests are:

- [`internal/controlplane/handlers_test.go`](../internal/controlplane/handlers_test.go) — `/health` and `/ready`
- [`internal/controlplane/server_test.go`](../internal/controlplane/server_test.go) — server construction and shutdown
- [`internal/config/config_test.go`](../internal/config/config_test.go) — configuration loading and validation

Run all of them with:

```bash
go test ./...
```
