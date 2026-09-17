# Local development

This walks through running the Nimbus control plane on your machine from a
clean checkout. Everything runs locally — no cloud account, API key, or paid
service is required.

## 1. Prerequisites

- [Git](https://git-scm.com/)
- [Go](https://go.dev/dl/) 1.25 or later (`go version`) — this is the minimum
  required by the `github.com/jackc/pgx/v5` driver, not an arbitrary choice
- [Docker](https://www.docker.com/) with Compose v2 (`docker compose version`) — used both to run PostgreSQL (steps 3+) and, separately, as the container runtime the Node Agent's `internal/runtime/docker` integration tests exercise (step 14)

## 2. Clone the repository

```bash
git clone https://github.com/dhananjaiyadav1234/Nimbus.git
cd Nimbus
```

## 3. Start PostgreSQL

```bash
docker compose -f deployments/docker-compose.yml up -d
```

This starts a single `postgres:16-alpine` container named `nimbus-postgres`,
listening on `localhost:5432`, with its data persisted in the `nimbus_postgres_data`
Docker volume. Check it's healthy:

```bash
docker compose -f deployments/docker-compose.yml ps
```

## 4. Configure environment variables

Copy the example file and adjust it if needed (the defaults match the
Compose file above, so no edits are required for local use):

```bash
cp .env.example .env
```

Load it into your shell before running the control plane:

```bash
set -a && source .env && set +a
```

`.env` is git-ignored; never commit real credentials. See
[`.env.example`](../.env.example) for what each variable does.

## 5. Run the control plane

```bash
go run ./cmd/control-plane
```

On success you'll see structured startup logs ending with the server
listening, e.g.:

```
time=... level=INFO msg="nimbus control plane starting" env=development log_level=info
time=... level=INFO msg="connected to postgresql" address=localhost:5432 database=nimbus
time=... level=INFO msg="http server started" address=0.0.0.0:8080
```

Leave it running in this terminal and use another for the next steps.

## 6. Test `/health`

```bash
curl -i http://localhost:8080/health
```

Expected: `HTTP/1.1 200 OK` with body `{"status":"ok"}`.

## 7. Test `/ready`

```bash
curl -i http://localhost:8080/ready
```

Expected while PostgreSQL is up: `HTTP/1.1 200 OK` with body
`{"status":"ready"}`.

To see the failure path, stop the database and query again:

```bash
docker compose -f deployments/docker-compose.yml stop
curl -i http://localhost:8080/ready
```

Expected: `HTTP/1.1 503 Service Unavailable` with a body like
`{"status":"not ready","error":"database is unavailable"}` — no credentials
or driver internals are exposed. Restart the database afterwards:

```bash
docker compose -f deployments/docker-compose.yml start
```

## 8. Run three Node Agents and watch the cluster

With the control plane still running from step 5, open three more terminals
(or background them — commands below do that for you) and start three
agents, each with its own local identity directory and a distinct simulated
hostname:

```bash
NIMBUS_NODE_DATA_DIR=/tmp/nimbus-node-a NIMBUS_NODE_NAME=node-a \
  go run ./cmd/node-agent &

NIMBUS_NODE_DATA_DIR=/tmp/nimbus-node-b NIMBUS_NODE_NAME=node-b \
  go run ./cmd/node-agent &

NIMBUS_NODE_DATA_DIR=/tmp/nimbus-node-c NIMBUS_NODE_NAME=node-c \
  go run ./cmd/node-agent &
```

Each prints `"node agent started"` then `"node agent registered"`. Confirm
all three registered as distinct nodes:

```bash
curl -s http://localhost:8080/nodes
```

(pipe through `jq` if you have it, or just use `go run ./cmd/nimbus node
list` from step 9 for a readable table). You should see three
entries — `node-a`, `node-b`, `node-c` — all `"Ready"`.

**Failure detection.** Stop one agent (`kill %2` for node-b if it was the
second background job, or `Ctrl+C` in its terminal). Wait at least
`NIMBUS_HEARTBEAT_INTERVAL × NIMBUS_HEARTBEAT_FAILURE_THRESHOLD` (30 seconds
with the defaults), then check again:

```bash
sleep 30
go run ./cmd/nimbus node list
```

`node-b` should now show `NotReady`; `node-a` and `node-c` remain `Ready`.
The control plane's own logs show exactly one `"node marked NotReady"` line
for `node-b` — nothing for the two healthy nodes.

**Recovery.** Restart the stopped agent with the *same* data directory:

```bash
NIMBUS_NODE_DATA_DIR=/tmp/nimbus-node-b NIMBUS_NODE_NAME=node-b \
  go run ./cmd/node-agent &
```

Its next heartbeat (within one `NIMBUS_HEARTBEAT_INTERVAL`) flips it back to
`Ready` — check with `nimbus node list` again. Note it re-used its existing
node ID (from `/tmp/nimbus-node-b/identity.json`) rather than registering as
a fourth node.

See [`docs/cluster-membership.md`](cluster-membership.md) for exactly how
registration, heartbeats, and failure detection work.

## 9. Use the CLI

```bash
go run ./cmd/nimbus node list
```

```
NAME       STATUS     CPU     MEMORY     LAST HEARTBEAT
node-a     Ready      8       16 GiB     2s ago
node-b     Ready      8       16 GiB     4s ago
node-c     Ready      8       16 GiB     1s ago
```

`--control-plane <url>` or `NIMBUS_CONTROL_PLANE_URL` points it at a
non-default Control Plane. With the control plane stopped, it prints
`error: unable to connect to Nimbus control plane` and exits non-zero,
rather than a raw connection error.

## 10. Deploy a workload

With the control plane still running, create a manifest:

```bash
cat > web.yaml <<'YAML'
apiVersion: nimbus/v1
kind: Deployment
metadata:
  name: web
spec:
  image: nginx:latest
  replicas: 2
  resources:
    cpu: 1
    memory: 512Mi
YAML
```

Submit it:

```bash
go run ./cmd/nimbus deploy -f web.yaml
```

```
deployment "web" created (id: 5c9c4c0a-...-f0e21b7c9a41, replicas: 2)
```

Confirm it's there — through the CLI, and directly through the API:

```bash
go run ./cmd/nimbus deployment list
```

```
NAME     IMAGE          REPLICAS     CPU     MEMORY     AGE
web      nginx:latest   2            1       512 MiB    4s ago
```

```bash
curl -s http://localhost:8080/deployments
curl -s http://localhost:8080/deployments/<id-from-the-list-above>
```

Submitting the same manifest again fails with `409 Conflict` (names are
unique — see [`docs/workloads.md`](workloads.md#deployment-identity-and-idempotency)),
not a silent duplicate or update.

Delete it:

```bash
curl -s -X DELETE -o /dev/null -w '%{http_code}\n' http://localhost:8080/deployments/<id>
go run ./cmd/nimbus deployment list   # empty again
```

**This does not start, stop, or touch any container.** Phase 2.1 persists
desired state only — see [`docs/workloads.md`](workloads.md#scope-boundary)
for exactly what is and isn't implemented yet, and why.

## 11. Stop everything

Stop the agents (`kill %1 %2 %3`, or `Ctrl+C` in each terminal), then the
control plane (`Ctrl+C` in its terminal). The control plane's shutdown log
now includes the membership monitor:

```
time=... level=INFO msg="shutdown signal received"
time=... level=INFO msg="http server shutting down" timeout=15s
time=... level=INFO msg="http server stopped"
time=... level=INFO msg="membership monitor stopped"
time=... level=INFO msg="closing database connection" address=localhost:5432
time=... level=INFO msg="database connection closed"
time=... level=INFO msg="nimbus control plane stopped"
```

Each agent, on `Ctrl+C`, logs its own shutdown and exits with no goroutines
left running — no deregistration call is made (see
[`docs/cluster-membership.md`](cluster-membership.md#graceful-shutdown)); a
stopped agent's node simply becomes `NotReady` once its heartbeat is missed.

## 12. Run automated tests

```bash
go test ./...
```

Most packages are pure unit tests and need nothing running. Two packages
also have **integration tests that exercise a real PostgreSQL database**
(`internal/cluster`'s repository tests, `internal/database`'s migration
tests) — they skip themselves cleanly, with a clear reason printed, if
PostgreSQL isn't reachable. To actually run them, start PostgreSQL first
(step 3) and load `.env` (step 4) so `NIMBUS_DATABASE_*` points at it; the
integration tests create and use their own `nimbus_test` database (override
its name with `NIMBUS_TEST_DATABASE_NAME`), so they never touch your
`nimbus` development database or its data.

```bash
go test -race ./...
```

runs the same suite with Go's race detector — this repository's concurrency
(heartbeats, the membership monitor, graceful shutdown) is expected to pass
cleanly under `-race`.

## 13. Stop PostgreSQL

```bash
docker compose -f deployments/docker-compose.yml down
```

Data persists in the Docker volume across `down`/`up`. To also delete the
data, add `-v`:

```bash
docker compose -f deployments/docker-compose.yml down -v
```

## 14. Run the container runtime integration tests

Separately from the PostgreSQL-backed Control Plane, the Node Agent's
container runtime (`internal/runtime/docker`) has its own integration tests
against a real Docker Engine — Docker Desktop or any other reachable Docker
Engine works, and it does not need to be the same Docker Engine PostgreSQL
runs alongside (it isn't a container here at all; it's the thing being
tested):

```bash
docker pull nginx:latest
docker pull busybox:latest

go test ./internal/runtime/docker/...
```

Like the PostgreSQL integration tests, these skip themselves cleanly with a
clear printed reason if no Docker Engine is reachable — they never
silently pass, and `go test ./...` never fails just because Docker happens
to be stopped. See [`docs/workloads.md`](workloads.md#running-the-docker-integration-tests)
for what they cover.
