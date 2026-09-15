# Local development

This walks through running the Nimbus control plane on your machine from a
clean checkout. Everything runs locally — no cloud account, API key, or paid
service is required.

## 1. Prerequisites

- [Git](https://git-scm.com/)
- [Go](https://go.dev/dl/) 1.25 or later (`go version`) — this is the minimum
  required by the `github.com/jackc/pgx/v5` driver, not an arbitrary choice
- [Docker](https://www.docker.com/) with Compose v2 (`docker compose version`)

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

## 8. Stop the control plane

Press `Ctrl+C` in the terminal running `go run ./cmd/control-plane`. It
handles `SIGINT`/`SIGTERM`, stops accepting new requests, finishes any
in-flight ones, closes the database connection, and logs each step before
exiting.

## 9. Run automated tests

```bash
go test ./...
```

These are unit tests and do not require PostgreSQL to be running — the
readiness dependency is a mockable interface (see
[`internal/health/health.go`](../internal/health/health.go)).

## 10. Stop PostgreSQL

```bash
docker compose -f deployments/docker-compose.yml down
```

Data persists in the Docker volume across `down`/`up`. To also delete the
data, add `-v`:

```bash
docker compose -f deployments/docker-compose.yml down -v
```
