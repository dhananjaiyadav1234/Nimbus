# Workloads & container runtime

This document describes Phase 2.1: the Deployment domain model, its
PostgreSQL-backed persistence, the Control Plane's `/deployments` API, the
`nimbus` CLI's `deploy`/`deployment list` commands, and the Node Agent's
container runtime abstraction.

**Phase 2.1 persists desired deployments and introduces the container
runtime abstraction.** This document covers the workload model, validation,
persistence, and the `/deployments` API — see [Scope boundary](#scope-boundary)
below for exactly what Phase 2.1 itself does and does not do. Scheduling
and placement are implemented separately, in Phase 2.2, and documented in
[`docs/scheduling.md`](scheduling.md). This document assumes you've read
[`docs/cluster-membership.md`](cluster-membership.md) for the Phase 1.2
foundation this builds on.

## Architecture

```
                       User
                        │
                        ▼
                  Nimbus CLI
                        │  POST/GET /deployments
                        ▼
                Nimbus Control Plane
                        │
                        ▼
                   PostgreSQL
              (`deployments` table)

                                       Node Agent
                                            │
                                 internal/runtime.ContainerRuntime
                                            │
                                   internal/runtime/docker
                                            │
                                       Docker Engine
```

The top half — CLI → Control Plane → PostgreSQL — is fully wired and is
what `nimbus deploy` actually exercises today. The bottom half — Node Agent
→ `ContainerRuntime` → Docker — exists, is fully implemented, and is
independently tested (see [Container runtime](#container-runtime) below),
but **nothing connects the two halves yet**. That gap is deliberate — see
[Scope boundary](#scope-boundary).

## Deployment manifest

```yaml
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
```

This is both the YAML file format `nimbus deploy -f` reads and, marshaled
to JSON, the `POST /deployments` request body — one type
(`deploymentapi.Manifest`) carries both `yaml` and `json` struct tags,
rather than two independently-drifting definitions. Equivalently, as JSON:

```json
{
  "apiVersion": "nimbus/v1",
  "kind": "Deployment",
  "metadata": { "name": "web" },
  "spec": {
    "image": "nginx:latest",
    "replicas": 2,
    "resources": { "cpu": 1, "memory": "512Mi" }
  }
}
```

### Field reference

| Field | Meaning |
|---|---|
| `apiVersion` | Must be exactly `nimbus/v1`. |
| `kind` | Must be exactly `Deployment`. |
| `metadata.name` | Unique identity (see below). `[a-z0-9]([-a-z0-9]*[a-z0-9])?`, up to 63 characters — the same shape as a DNS label (RFC 1035), chosen for the same practical reason Kubernetes-style tooling converges on it: it's safe to use directly in a container name (see [Container naming and labels](#container-naming-and-labels)) without escaping. |
| `spec.image` | Non-empty, no whitespace/control characters, up to 255 characters. Not validated against full Docker image reference grammar — a deliberately light check, not a registry client. |
| `spec.replicas` | `0` to `1000` inclusive. The upper bound is a sanity ceiling, not a capacity figure. (Phase 2.2's scheduler, documented separately in [`docs/scheduling.md`](scheduling.md), is what actually places them — creating a deployment here never does.) |
| `spec.resources.cpu` | Integer CPU count (whole cores), `> 0`, capped at `1024` (sanity ceiling, same reasoning as replicas). Kubernetes-style milli-CPU units are deliberately not implemented — nothing in Phase 2.1 needs sub-core granularity. |
| `spec.resources.memory` | Human-readable quantity, e.g. `512Mi`, `1Gi`. `spec.resources` itself must be present. |

All of the above is validated **server-side**, in `internal/deployment`,
regardless of what the CLI already checked — see
[Validation happens twice, on purpose](#validation-happens-twice-on-purpose).

## Resource units

Memory is normalized to a canonical **byte count** exactly once, by
`internal/deployment.ParseMemory`, and stored that way everywhere past the
API boundary — the database column, the domain model, and the container
runtime all deal in `int64` bytes, never the string `"512Mi"`.

Supported suffixes are the IEC binary units — `Ki`, `Mi`, `Gi`, `Ti`
(1024-based) — or a bare integer, interpreted as bytes directly:

| Input | Bytes |
|---|---|
| `512Mi` | `536870912` |
| `1Gi` | `1073741824` |
| `536870912` | `536870912` |

Decimal suffixes (`K`, `M`, `G`, `T`, 1000-based, as Kubernetes also
accepts) are **not** supported — nothing in the Phase 2.1 contract asks for
them, the one documented example (`memory: 512Mi`) is unambiguous with
binary-only support, and accepting both systems would invite exactly the
unit confusion this normalization step exists to prevent. `ParseMemory`
also rejects a numeric part large enough to overflow `int64` once
multiplied (checked before multiplying, not after) and enforces a 1 TiB
sanity ceiling.

CPU has no suffix or parsing step at all: it is a plain integer number of
whole cores, exactly as written in the manifest.

## Deployment identity and idempotency

Unlike node registration (Phase 1.2), a Deployment's **name is the
identity a client provides**, and its **ID is always server-generated** —
there's no client-persisted identity file to give the client its own copy
of an existing ID (there is no long-running "deployment agent" the way
there's a Node Agent). This is why:

- `POST /deployments` is **not** idempotent-by-resubmission: calling it
  twice with the same name gets you `201 Created` once and `409 Conflict`
  the second time. There is no implicit upsert.
- Uniqueness is enforced by PostgreSQL's `UNIQUE` constraint on `name`, not
  application logic — verified under real concurrent load in
  `internal/deployment/repository_test.go`
  (`TestPostgresRepositoryConcurrentCreateOfSameNameExactlyOneSucceeds`: 20
  concurrent creates of the same name, exactly 1 succeeds, exactly 1 row
  results). An application-level "check then insert" would have a race
  window between the check and the insert; a single `INSERT` relying on the
  database's own constraint does not.

## Validation happens twice, on purpose

1. **CLI, client-side** (`deploymentapi.Manifest.ValidateEnvelope`): checks
   only the manifest *envelope* — `apiVersion`, `kind`, that `metadata.name`
   and `spec.resources` are present — before any network call. This catches
   an obviously-wrong file fast, locally, with no round trip.
2. **Control Plane, server-side** (`ValidateEnvelope` again, then
   `internal/deployment.CreateInput.Validate` for the deployment's own
   fields): runs unconditionally on every request, regardless of what a
   client already checked. The Control Plane is the single source of truth
   for these rules — the CLI's check is a UX nicety, never a substitute,
   and both business validation and memory-string parsing live exactly
   once, server-side, so the CLI never carries a copy of those rules that
   could drift from the server's.

## Deployment lifecycle

```
POST /deployments  →  validate  →  PostgreSQL row created  →  201 Created
DELETE /deployments/{id}  →  PostgreSQL row removed  →  204 No Content
```

**`DELETE` removes only the desired-state row.** It does **not** stop any
container or contact any node — nothing creates a container yet (see
"Scope boundary" below). Its placement rows, if Phase 2.2's scheduler had
created any, are removed automatically by the database itself
(`deployment_placements.deployment_id` is `ON DELETE CASCADE` — see
[`docs/scheduling.md`](scheduling.md)), not by any application-level
cleanup logic here. Once Phase 3 (reconciliation) exists, deletion will
also need to drive real container cleanup through that
desired-state/reconciliation loop — deliberately not implemented here as a
shortcut now, since that would create exactly the kind of architectural
debt this phased structure exists to avoid.

## Control Plane API

```
POST   /deployments        create (manifest body)       → 201, or 409 if the name exists
GET    /deployments         list, ordered by name ASC     → 200
GET    /deployments/{id}    get one                       → 200, or 404
DELETE /deployments/{id}    delete                        → 204, or 404
```

Not implemented in Phase 2.1 (deliberately — see
[Scope boundary](#scope-boundary)): `POST /deployments/{id}/scale`,
`/restart`, `/rollback`.

Request bodies are capped at 64 KiB (`internal/controlplane/deployments.go`'s
`maxDeploymentBodyBytes`) — a manifest is a handful of short fields; there
is no reason to read anything close to that much into memory before even
validating it.

Errors follow the existing convention (see
[`docs/cluster-membership.md`](cluster-membership.md)): a structured
`{"error": "..."}` body, never a raw database error or stack trace.
`400` for an invalid manifest (envelope or field-level), `404` for an
unknown deployment, `409` for a duplicate name, `500` for an unexpected
database failure (logged server-side with full detail, reported to the
client generically).

## Container runtime

`internal/runtime.ContainerRuntime` is the one boundary every
Docker-specific detail stays behind:

```go
type ContainerRuntime interface {
    CreateContainer(ctx context.Context, spec ContainerSpec) (string, error)
    StartContainer(ctx context.Context, id string) error
    StopContainer(ctx context.Context, id string) error
    RemoveContainer(ctx context.Context, id string) error
    InspectContainer(ctx context.Context, id string) (ContainerStatus, error)
}
```

`internal/runtime/docker.Runtime` implements it against a real Docker
Engine, via Docker's official Go client talking to the Engine API — never
the `docker` CLI, never `exec.Command`. `internal/runtime.FakeRuntime` is an
in-memory implementation for tests that need a `ContainerRuntime` without a
Docker daemon (the same role `internal/database/dbtest` plays for
PostgreSQL-dependent tests, and `internal/cluster`'s `fakeRepository` plays
for the node-membership `Repository`).

### Container naming and labels

```
nimbus-<deployment-name>-<instance>
```

e.g. `nimbus-web-0`. `instance` distinguishes one deployment's replicas
from each other — Phase 2.2's scheduler (see
[`docs/scheduling.md`](scheduling.md)) now assigns a real placement per
replica index, though nothing yet turns that placement into an actual
container carrying this name; that connection is future work (Phase 3).

Every Nimbus-created container also carries labels
(`internal/runtime.ContainerLabels`):

```
nimbus.managed=true
nimbus.deployment=<deployment-id>
nimbus.deployment-name=<deployment-name>
```

so ownership and provenance can be queried without parsing container names.
Nothing reads these back yet — there is no reconciliation loop until Phase
3 — but they are applied now precisely so that loop has something reliable
to query later, instead of needing a data migration to retrofit them.

### Image pulling is out of scope

`CreateContainer` does **not** pull `spec.Image` implicitly. The image must
already be present on the Docker Engine; Docker reports a clear "No such
image" error otherwise, which `CreateContainer` returns wrapped, not
swallowed. The five-method `ContainerRuntime` interface Phase 2.1 specifies
has no `PullImage` method, and adding implicit-pull behaviour (streaming
pull progress, retry-on-registry-failure, etc.) is a real feature, not a
one-line addition — left for whichever future phase actually needs it.

### Dependency note: an older Docker client, deliberately

`internal/runtime/docker` depends on `github.com/moby/moby/client` (Docker's
current official Go SDK — the project was renamed from `docker/docker` to
`moby/moby` upstream). It is pinned to `v0.6.0` — the latest version
available at the time this phase was implemented, per Go's standard
practice of exact, reproducible dependency pins rather than always
tracking `@latest`. The client negotiates the Docker Engine API version at
connection time
(`client.WithAPIVersionNegotiation()`), so it keeps working across Docker
Engine upgrades without needing a matching client upgrade.

## Local development

### Deploying a manifest

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

go run ./cmd/nimbus deploy -f web.yaml
go run ./cmd/nimbus deployment list
```

See [`docs/development.md`](development.md) for the full walkthrough,
including verifying the row lands in PostgreSQL and retrieving/deleting it
through the API.

### Running the Docker integration tests

`internal/runtime/docker`'s tests are real integration tests against a
Docker Engine — not a fake, not a mock. They skip themselves cleanly, with
a clear printed reason, if Docker is not reachable (the same contract
`internal/database/dbtest` gives PostgreSQL-dependent tests):

```bash
# Docker Desktop (or another Docker Engine) must be running.
docker pull nginx:latest    # long-running default CMD; used by the tests
docker pull busybox:latest  # tiny image; used where "exists" is enough

go test ./internal/runtime/docker/...
```

Every container the tests create is named `nimbus-test-<test-name>-<nanos>`
and is stopped and removed in the test's own cleanup — a test run leaves no
containers behind (verified with `docker ps -a --filter name=nimbus-test`
returning nothing after a run).

### Manually exercising the runtime

With Docker running:

```bash
go test ./internal/runtime/docker/... -run TestDockerRuntimeFullLifecycle -v
```

walks Create → Inspect → Start → Inspect → Stop → Inspect → Remove →
Inspect (now `ErrContainerNotFound`) against a real container, and a
second test (`TestDockerRuntimeAppliesNimbusLabels`) confirms the Nimbus
labels above are actually present on the created container, as reported by
Docker itself — not merely round-tripped through Nimbus's own code.

## Scope boundary

Explicitly **not** implemented in Phase 2.1 — each belongs to a specific
later phase:

| Not implemented (as of Phase 2.1) | Belongs to | Status |
|---|---|---|
| Scheduler, placement, bin packing, node scoring, replica assignment | Phase 2.2 | ✓ now implemented — see [`docs/scheduling.md`](scheduling.md) |
| `POST /deployments/{id}/scale`, `/restart`, `/rollback` | Phase 2.2+ | still not implemented |
| Reconciliation, desired-vs-actual comparison, automatic container restart, node-failure rescheduling | Phase 3 | still not implemented |
| Service discovery, DNS, load balancing, overlay networking | Phase 4 | still not implemented |
| Autoscaling (CPU/memory-triggered replica changes) | Phase 5 | still not implemented |
| Authentication | Phase 5 | still not implemented |

Most importantly: **`POST /deployments` does not create a container**, and
still doesn't after Phase 2.2 — the row lands in PostgreSQL and nothing
else happens. Scheduling a deployment (`POST /deployments/{id}/schedule`,
Phase 2.2) doesn't create one either: it only decides and persists *where*
a replica would go. Wiring either of those straight to Docker would skip a
deliberate phase boundary and bake in exactly the architectural debt this
phased structure is designed to prevent — see
[`docs/scheduling.md`](scheduling.md#scope-boundary) for the current,
authoritative version of this boundary.
