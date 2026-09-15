// Package cluster owns Nimbus's cluster-membership domain: the Node model,
// its state machine, and the service that enforces registration and
// heartbeat rules on top of a Repository. PostgreSQL (via Repository) is the
// only source of truth — there is no authoritative in-memory membership
// list, so a Control Plane restart never loses knowledge of a node.
package cluster

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Status is a node's position in the cluster-membership state machine.
type Status string

// The three states a node can be in. Registering is the state assigned the
// instant a brand-new node is persisted; Ready and NotReady are entered only
// once a heartbeat has been received or missed, per the transition rules
// documented on Service.
const (
	StatusRegistering Status = "Registering"
	StatusReady       Status = "Ready"
	StatusNotReady    Status = "NotReady"
)

// Valid reports whether s is one of the three recognised statuses.
func (s Status) Valid() bool {
	switch s {
	case StatusRegistering, StatusReady, StatusNotReady:
		return true
	default:
		return false
	}
}

// Node is a single member of the Nimbus cluster, as recorded by the Control
// Plane. It is the row shape of the `nodes` table plus nothing else —
// handlers and the CLI translate it to and from wire formats, but the type
// itself carries no HTTP or JSON concerns.
type Node struct {
	ID                  uuid.UUID
	Hostname            string
	Status              Status
	OS                  string
	Architecture        string
	CPUCapacity         int64
	MemoryCapacityBytes int64
	AgentVersion        string
	// LastHeartbeatAt is set at registration time (Service.Register treats a
	// successful registration as an implicit first heartbeat — see
	// docs/cluster-membership.md) and updated by every heartbeat after that,
	// so it is non-nil for every node created through the normal API. It
	// remains a pointer, rather than a plain time.Time, because the column
	// is nullable at the schema level and the zero value of time.Time would
	// otherwise be indistinguishable from a real, very old timestamp.
	LastHeartbeatAt *time.Time
	RegisteredAt    time.Time
	UpdatedAt       time.Time
}

// RegisterInput carries everything a node supplies about itself at
// registration time. NodeID is the empty UUID for a first-time registration;
// an agent that already has an identity sets it so the Service can recognise
// and update the same row instead of creating a new one.
type RegisterInput struct {
	NodeID              uuid.UUID
	Hostname            string
	OS                  string
	Architecture        string
	CPUCapacity         int64
	MemoryCapacityBytes int64
	AgentVersion        string
}

// Validate applies the field-level rules from the registration contract. It
// returns a *ValidationError naming every problem at once, so a caller with
// several mistakes fixes them in one round trip rather than one per retry.
func (in RegisterInput) Validate() error {
	var err ValidationError

	if in.Hostname == "" {
		err.add("hostname must not be empty")
	}
	if in.OS == "" {
		err.add("os must not be empty")
	}
	if in.Architecture == "" {
		err.add("architecture must not be empty")
	}
	if in.AgentVersion == "" {
		err.add("agent_version must not be empty")
	}
	if in.CPUCapacity <= 0 {
		err.add("cpu_capacity must be greater than zero")
	}
	if in.MemoryCapacityBytes <= 0 {
		err.add("memory_capacity_bytes must be greater than zero")
	}

	if len(err.Problems) > 0 {
		return &err
	}
	return nil
}

// ValidationError reports every problem found with a client-supplied request
// in one value, so handlers can return a single, complete 400 response.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) add(problem string) {
	e.Problems = append(e.Problems, problem)
}

func (e *ValidationError) Error() string {
	if len(e.Problems) == 1 {
		return e.Problems[0]
	}
	return fmt.Sprintf("%d validation problems: %v", len(e.Problems), e.Problems)
}
