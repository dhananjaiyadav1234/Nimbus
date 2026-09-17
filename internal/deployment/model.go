// Package deployment owns Nimbus's workload domain: the Deployment model —
// a user's desired containerized workload — persisted in PostgreSQL, the
// sole source of truth, exactly as internal/cluster owns node membership.
//
// Phase 2.1 only persists desired state and validates it. Nothing in this
// package or its callers creates a container: there is no scheduler yet to
// decide which node should run a Deployment's replicas, and connecting
// POST /deployments straight to a node's Docker daemon would bypass that
// architecture entirely. See docs/workloads.md.
package deployment

import (
	"time"

	"github.com/google/uuid"
)

// Naming and sizing bounds, centralized here so every layer (validation,
// schema constraints, documentation) cites the same constants instead of
// scattering magic numbers.
const (
	// MaxNameLength matches the DNS label length limit (RFC 1035) that
	// Kubernetes and most container tooling also settle on — not because
	// Nimbus imitates Kubernetes, but because it is a well-understood,
	// widely-compatible bound for a name that may end up in container
	// names, labels, and (in a later phase) DNS.
	MaxNameLength = 63
	// MaxImageLength is generous for any real registry/image/tag/digest
	// reference while still rejecting pathological input.
	MaxImageLength = 255
	// MaxReplicas bounds desired replica count. There is no scheduler yet
	// to place any of them, so this is purely a sanity limit against
	// accidental or malicious absurd values (not a capacity planning
	// figure) — 1000 is far beyond what a single-node-class Phase 2
	// deployment would ever need.
	MaxReplicas = 1000
	// MaxCPU bounds the "cpu" field for the same reason as MaxReplicas: a
	// sanity ceiling, not a real capacity limit enforced anywhere yet.
	MaxCPU = 1024
	// MaxMemoryBytes bounds parsed memory quantities: 1 TiB, far beyond any
	// plausible single container today, chosen to catch fat-fingered or
	// malicious input (and see resource.go for the overflow protection
	// applied before this bound is even checked).
	MaxMemoryBytes = 1 << 40
)

// Deployment is a single desired workload, as recorded by the Control
// Plane. It is the row shape of the `deployments` table plus nothing else —
// handlers and the CLI translate it to and from wire formats; the type
// itself carries no HTTP, YAML, or JSON concerns.
type Deployment struct {
	ID          uuid.UUID
	Name        string
	Image       string
	Replicas    int
	CPU         int64
	MemoryBytes int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CreateInput carries everything needed to create a new Deployment. Unlike
// cluster.RegisterInput's node_id, there is no client-supplied identity
// here: a Deployment's identity is its (unique) Name, and its ID is always
// server-generated — see docs/workloads.md for why that asymmetry with node
// registration is intentional.
type CreateInput struct {
	Name        string
	Image       string
	Replicas    int
	CPU         int64
	MemoryBytes int64
}
