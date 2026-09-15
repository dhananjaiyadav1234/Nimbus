# scripts/

Placeholder for local developer tooling (e.g. lint/format wrappers) added in
later phases. Through Phase 1.2, every command Nimbus needs is a plain `go`
or `docker compose` invocation, documented in
[`docs/development.md`](../docs/development.md) — including database
migrations, which run automatically on Control Plane startup
(`internal/database.Migrate`) rather than through a separate script.
